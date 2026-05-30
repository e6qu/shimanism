// Package aws is the AWS Route 53 passthrough backend. It uses
// aws-sdk-go-v2/service/route53 to drive real Route 53 (or a
// sockerless / per-request endpoint-overridden client for tests).
//
// The shim's domain identifies zones by DNS name. Route 53 identifies
// them by HostedZoneId. The backend resolves name → ID at request time
// via ListHostedZonesByName — the shim holds no name→ID translation
// table (the stateless-shim rule).
//
// Visibility dispatch follows N17: VisibilityPrivate creates a Route
// 53 private hosted zone (CreateHostedZone with VPC); VisibilityPublic
// omits the VPC. For private zones the domain's PrivateVPCs[0] is the
// initial VPC ID; the AWS region comes from the configured client's
// region. Additional VPC associations require AssociateVPCWithHostedZone,
// which is a follow-on operation (not part of the foundational
// intersection).
//
// Two zones with the same DNS name (one public + one private) is a
// Route 53-valid configuration but ambiguous under the domain's
// name-keyed lookup. The backend returns the first match
// ListHostedZonesByName surfaces and documents the caveat.
package aws

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	r53 "github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

// Backend implements domain.DNS via real AWS Route 53.
type Backend struct {
	c *r53.Client
}

// New wraps an already-configured Route 53 client.
func New(client *r53.Client) *Backend { return &Backend{c: client} }

var _ domain.DNS = (*Backend)(nil)

// canonicalize forces lowercase + trailing dot so the shim accepts
// "Example.COM" / "example.com" / "example.com." uniformly.
func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// callerReference must be unique per CreateHostedZone request; Route
// 53 uses it for idempotency. The shim mints a fresh one per call.
func callerReference() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return "shimanism-" + hex.EncodeToString(buf[:])
}

// translateErr maps Route 53 SDK errors to domain errors using the
// canonical Route 53 error short names from the Smithy spec.
func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var nszErr *r53types.NoSuchHostedZone
	if errors.As(err, &nszErr) {
		return domain.NoSuchZone(name)
	}
	var existsErr *r53types.HostedZoneAlreadyExists
	if errors.As(err, &existsErr) {
		return domain.ZoneAlreadyExists(name)
	}
	var notEmpty *r53types.HostedZoneNotEmpty
	if errors.As(err, &notEmpty) {
		return domain.ZoneNotEmpty(name)
	}
	var invalid *r53types.InvalidInput
	if errors.As(err, &invalid) {
		return domain.InvalidArgument(awsapi.ToString(invalid.Message))
	}
	var invalidDomain *r53types.InvalidDomainName
	if errors.As(err, &invalidDomain) {
		return domain.InvalidArgument(awsapi.ToString(invalidDomain.Message))
	}
	var changeBatch *r53types.InvalidChangeBatch
	if errors.As(err, &changeBatch) {
		msg := awsapi.ToString(changeBatch.Message)
		// Route 53 reports "Tried to delete resource record set ... but it was not found"
		// for DELETE actions targeting a non-existent record set.
		if strings.Contains(msg, "but it was not found") {
			return domain.NoSuchRecordSet("", name, "")
		}
		return domain.InvalidArgument(msg)
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchHostedZone":
			return domain.NoSuchZone(name)
		case "HostedZoneAlreadyExists":
			return domain.ZoneAlreadyExists(name)
		case "HostedZoneNotEmpty":
			return domain.ZoneNotEmpty(name)
		case "InvalidInput", "InvalidDomainName":
			return domain.InvalidArgument(ae.ErrorMessage())
		}
	}
	return err
}

// resolveZoneID looks up the Route 53 HostedZone ID for a DNS name.
// Used by every operation that addresses a zone by name. Stateless —
// each call queries Route 53 fresh.
func (b *Backend) resolveZoneID(ctx context.Context, name string) (string, error) {
	canonical := canonicalize(name)
	out, err := b.c.ListHostedZonesByName(ctx, &r53.ListHostedZonesByNameInput{
		DNSName:  awsapi.String(canonical),
		MaxItems: awsapi.Int32(1),
	})
	if err != nil {
		return "", translateErr(err, canonical)
	}
	if len(out.HostedZones) == 0 || canonicalize(awsapi.ToString(out.HostedZones[0].Name)) != canonical {
		return "", domain.NoSuchZone(canonical)
	}
	return awsapi.ToString(out.HostedZones[0].Id), nil
}

func (b *Backend) CreateZone(ctx context.Context, name string, opt domain.CreateZoneOptions) (domain.Zone, error) {
	canonical := canonicalize(name)
	in := &r53.CreateHostedZoneInput{
		Name:            awsapi.String(canonical),
		CallerReference: awsapi.String(callerReference()),
	}
	if opt.Description != "" || opt.Visibility == domain.VisibilityPrivate {
		in.HostedZoneConfig = &r53types.HostedZoneConfig{
			Comment:     awsapi.String(opt.Description),
			PrivateZone: opt.Visibility == domain.VisibilityPrivate,
		}
	}
	if opt.Visibility == domain.VisibilityPrivate {
		if len(opt.PrivateVPCs) == 0 {
			return domain.Zone{}, domain.InvalidArgument("private zone requires at least one VPC")
		}
		region := b.c.Options().Region
		in.VPC = &r53types.VPC{
			VPCId:     awsapi.String(opt.PrivateVPCs[0]),
			VPCRegion: r53types.VPCRegion(region),
		}
	}
	out, err := b.c.CreateHostedZone(ctx, in)
	if err != nil {
		return domain.Zone{}, translateErr(err, canonical)
	}
	z := zoneFromAWS(out.HostedZone, out.DelegationSet)
	if z.Name == "" {
		z.Name = canonical
	}
	z.Visibility = opt.Visibility
	z.Description = opt.Description
	z.Tags = copyTags(opt.Tags)
	if len(opt.Tags) > 0 {
		if err := b.applyTags(ctx, awsapi.ToString(out.HostedZone.Id), opt.Tags, nil); err != nil {
			return domain.Zone{}, err
		}
	}
	return z, nil
}

func (b *Backend) GetZone(ctx context.Context, name string) (domain.Zone, error) {
	canonical := canonicalize(name)
	id, err := b.resolveZoneID(ctx, canonical)
	if err != nil {
		return domain.Zone{}, err
	}
	out, err := b.c.GetHostedZone(ctx, &r53.GetHostedZoneInput{Id: awsapi.String(id)})
	if err != nil {
		return domain.Zone{}, translateErr(err, canonical)
	}
	z := zoneFromAWS(out.HostedZone, out.DelegationSet)
	tags, err := b.readTags(ctx, id)
	if err != nil {
		return domain.Zone{}, err
	}
	z.Tags = tags
	return z, nil
}

func (b *Backend) DeleteZone(ctx context.Context, name string, force bool) error {
	canonical := canonicalize(name)
	id, err := b.resolveZoneID(ctx, canonical)
	if err != nil {
		return err
	}
	if force {
		if err := b.deleteUserRecordSets(ctx, id); err != nil {
			return err
		}
	}
	_, err = b.c.DeleteHostedZone(ctx, &r53.DeleteHostedZoneInput{Id: awsapi.String(id)})
	return translateErr(err, canonical)
}

func (b *Backend) ListZones(ctx context.Context, opt domain.ListZonesOptions) (domain.ListZonesResult, error) {
	in := &r53.ListHostedZonesInput{}
	if opt.PageSize > 0 {
		in.MaxItems = awsapi.Int32(int32(opt.PageSize))
	}
	if opt.PageToken != "" {
		in.Marker = awsapi.String(opt.PageToken)
	}
	out, err := b.c.ListHostedZones(ctx, in)
	if err != nil {
		return domain.ListZonesResult{}, translateErr(err, "")
	}
	res := domain.ListZonesResult{}
	for i := range out.HostedZones {
		z := zoneFromAWS(&out.HostedZones[i], nil)
		if opt.NamePrefix != "" && !strings.HasPrefix(z.Name, canonicalize(opt.NamePrefix)) {
			continue
		}
		if opt.VisibilityFilter != domain.VisibilityUnknown && z.Visibility != opt.VisibilityFilter {
			continue
		}
		res.Zones = append(res.Zones, z)
	}
	if out.IsTruncated {
		res.NextPageToken = awsapi.ToString(out.NextMarker)
	}
	return res, nil
}

func (b *Backend) PutRecordSet(ctx context.Context, zone string, rs domain.RecordSet) error {
	canonical := canonicalize(zone)
	id, err := b.resolveZoneID(ctx, canonical)
	if err != nil {
		return err
	}
	awsRS, err := awsRecordSetFromDomain(rs)
	if err != nil {
		return err
	}
	_, err = b.c.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action:            r53types.ChangeActionUpsert,
				ResourceRecordSet: awsRS,
			}},
		},
	})
	return translateErr(err, rs.Name)
}

func (b *Backend) GetRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) (domain.RecordSet, error) {
	canonicalZone := canonicalize(zone)
	id, err := b.resolveZoneID(ctx, canonicalZone)
	if err != nil {
		return domain.RecordSet{}, err
	}
	canonicalName := canonicalize(name)
	awsRS, err := b.findRecordSet(ctx, id, canonicalName, rtype)
	if err != nil {
		return domain.RecordSet{}, err
	}
	if awsRS == nil {
		return domain.RecordSet{}, domain.NoSuchRecordSet(canonicalZone, canonicalName, rtype)
	}
	return domainRecordSetFromAWS(*awsRS), nil
}

func (b *Backend) DeleteRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) error {
	canonicalZone := canonicalize(zone)
	id, err := b.resolveZoneID(ctx, canonicalZone)
	if err != nil {
		return err
	}
	canonicalName := canonicalize(name)
	// Route 53 requires the existing record contents on DELETE.
	awsRS, err := b.findRecordSet(ctx, id, canonicalName, rtype)
	if err != nil {
		return err
	}
	if awsRS == nil {
		return domain.NoSuchRecordSet(canonicalZone, canonicalName, rtype)
	}
	_, err = b.c.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action:            r53types.ChangeActionDelete,
				ResourceRecordSet: awsRS,
			}},
		},
	})
	return translateErr(err, canonicalName)
}

func (b *Backend) ListRecordSets(ctx context.Context, zone string, opt domain.ListRecordSetsOptions) (domain.ListRecordSetsResult, error) {
	canonicalZone := canonicalize(zone)
	id, err := b.resolveZoneID(ctx, canonicalZone)
	if err != nil {
		return domain.ListRecordSetsResult{}, err
	}
	in := &r53.ListResourceRecordSetsInput{HostedZoneId: awsapi.String(id)}
	if opt.NameFilter != "" {
		in.StartRecordName = awsapi.String(canonicalize(opt.NameFilter))
	}
	if opt.TypeFilter != "" {
		in.StartRecordType = r53types.RRType(opt.TypeFilter)
	}
	if opt.PageSize > 0 {
		in.MaxItems = awsapi.Int32(int32(opt.PageSize))
	}
	if opt.PageToken != "" {
		in.StartRecordIdentifier = awsapi.String(opt.PageToken)
	}
	out, err := b.c.ListResourceRecordSets(ctx, in)
	if err != nil {
		return domain.ListRecordSetsResult{}, translateErr(err, canonicalZone)
	}
	res := domain.ListRecordSetsResult{}
	for _, rs := range out.ResourceRecordSets {
		dr := domainRecordSetFromAWS(rs)
		if opt.NameFilter != "" && dr.Name != canonicalize(opt.NameFilter) {
			continue
		}
		if opt.TypeFilter != "" && dr.Type != opt.TypeFilter {
			continue
		}
		res.RecordSets = append(res.RecordSets, dr)
	}
	if out.IsTruncated {
		res.NextPageToken = awsapi.ToString(out.NextRecordIdentifier)
	}
	return res, nil
}

func (b *Backend) findRecordSet(ctx context.Context, hostedZoneID, name string, rtype domain.RecordType) (*r53types.ResourceRecordSet, error) {
	out, err := b.c.ListResourceRecordSets(ctx, &r53.ListResourceRecordSetsInput{
		HostedZoneId:    awsapi.String(hostedZoneID),
		StartRecordName: awsapi.String(name),
		StartRecordType: r53types.RRType(rtype),
		MaxItems:        awsapi.Int32(1),
	})
	if err != nil {
		return nil, translateErr(err, name)
	}
	if len(out.ResourceRecordSets) == 0 {
		return nil, nil
	}
	got := out.ResourceRecordSets[0]
	if canonicalize(awsapi.ToString(got.Name)) != name || string(got.Type) != string(rtype) {
		return nil, nil
	}
	return &got, nil
}

func (b *Backend) deleteUserRecordSets(ctx context.Context, hostedZoneID string) error {
	out, err := b.c.ListResourceRecordSets(ctx, &r53.ListResourceRecordSetsInput{
		HostedZoneId: awsapi.String(hostedZoneID),
	})
	if err != nil {
		return translateErr(err, "")
	}
	var changes []r53types.Change
	for i, rs := range out.ResourceRecordSets {
		// Skip cloud-managed SOA + apex NS records — Route 53 manages
		// these and refuses to delete them.
		if rs.Type == r53types.RRTypeSoa {
			continue
		}
		if rs.Type == r53types.RRTypeNs {
			// The apex NS record is cloud-managed. Non-apex NS records
			// (delegations) are user-managed.
			zoneName := canonicalize(awsapi.ToString(rs.Name))
			if zoneName == "" || strings.Count(zoneName, ".") <= 1 {
				continue
			}
		}
		changes = append(changes, r53types.Change{
			Action:            r53types.ChangeActionDelete,
			ResourceRecordSet: &out.ResourceRecordSets[i],
		})
	}
	if len(changes) == 0 {
		return nil
	}
	_, err = b.c.ChangeResourceRecordSets(ctx, &r53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(hostedZoneID),
		ChangeBatch:  &r53types.ChangeBatch{Changes: changes},
	})
	return translateErr(err, "")
}

func (b *Backend) readTags(ctx context.Context, hostedZoneID string) (map[string]string, error) {
	out, err := b.c.ListTagsForResource(ctx, &r53.ListTagsForResourceInput{
		ResourceType: r53types.TagResourceTypeHostedzone,
		ResourceId:   awsapi.String(hostedZoneID),
	})
	if err != nil {
		return nil, translateErr(err, "")
	}
	if out.ResourceTagSet == nil || len(out.ResourceTagSet.Tags) == 0 {
		return nil, nil
	}
	tags := make(map[string]string, len(out.ResourceTagSet.Tags))
	for _, t := range out.ResourceTagSet.Tags {
		tags[awsapi.ToString(t.Key)] = awsapi.ToString(t.Value)
	}
	return tags, nil
}

func (b *Backend) applyTags(ctx context.Context, hostedZoneID string, add map[string]string, removeKeys []string) error {
	in := &r53.ChangeTagsForResourceInput{
		ResourceType: r53types.TagResourceTypeHostedzone,
		ResourceId:   awsapi.String(hostedZoneID),
	}
	if len(add) > 0 {
		keys := make([]string, 0, len(add))
		for k := range add {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		in.AddTags = make([]r53types.Tag, 0, len(keys))
		for _, k := range keys {
			v := add[k]
			in.AddTags = append(in.AddTags, r53types.Tag{Key: awsapi.String(k), Value: awsapi.String(v)})
		}
	}
	if len(removeKeys) > 0 {
		sort.Strings(removeKeys)
		in.RemoveTagKeys = removeKeys
	}
	_, err := b.c.ChangeTagsForResource(ctx, in)
	return translateErr(err, "")
}

func zoneFromAWS(hz *r53types.HostedZone, ds *r53types.DelegationSet) domain.Zone {
	z := domain.Zone{}
	if hz == nil {
		return z
	}
	z.Name = canonicalize(awsapi.ToString(hz.Name))
	if hz.Config != nil {
		z.Description = awsapi.ToString(hz.Config.Comment)
		if hz.Config.PrivateZone {
			z.Visibility = domain.VisibilityPrivate
		} else {
			z.Visibility = domain.VisibilityPublic
		}
	} else {
		z.Visibility = domain.VisibilityPublic
	}
	if ds != nil {
		z.NameServers = append(z.NameServers, ds.NameServers...)
	}
	return z
}

// awsRecordSetFromDomain encodes the domain's records into Route 53's
// ResourceRecord list. Each record is wrapped in `<ResourceRecord><Value>...</Value></ResourceRecord>`
// by the SDK; the value strings match what Route 53 stores natively.
func awsRecordSetFromDomain(rs domain.RecordSet) (*r53types.ResourceRecordSet, error) {
	if rs.Name == "" {
		return nil, domain.InvalidArgument("record set name is required")
	}
	if rs.Type == "" {
		return nil, domain.InvalidArgument("record set type is required")
	}
	if rs.TTL <= 0 {
		return nil, domain.InvalidArgument("record set TTL must be positive")
	}
	if len(rs.Records) == 0 {
		return nil, domain.InvalidArgument("record set must have at least one record")
	}
	out := &r53types.ResourceRecordSet{
		Name: awsapi.String(canonicalize(rs.Name)),
		Type: r53types.RRType(rs.Type),
		TTL:  awsapi.Int64(int64(rs.TTL)),
	}
	for _, v := range rs.Records {
		val := v
		if rs.Type == domain.RecordTypeTXT {
			val = encodeTXT(v)
		}
		out.ResourceRecords = append(out.ResourceRecords, r53types.ResourceRecord{Value: awsapi.String(val)})
	}
	return out, nil
}

func domainRecordSetFromAWS(rs r53types.ResourceRecordSet) domain.RecordSet {
	out := domain.RecordSet{
		Name: canonicalize(awsapi.ToString(rs.Name)),
		Type: domain.RecordType(rs.Type),
	}
	if rs.TTL != nil {
		out.TTL = int(*rs.TTL)
	}
	for _, rr := range rs.ResourceRecords {
		val := awsapi.ToString(rr.Value)
		if out.Type == domain.RecordTypeTXT {
			val = decodeTXT(val)
		}
		out.Records = append(out.Records, val)
	}
	return out
}

// encodeTXT wraps a TXT record value in double quotes per the Route 53
// wire format. The domain stores the unquoted contents.
func encodeTXT(v string) string {
	if strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		return v
	}
	return fmt.Sprintf("%q", v)
}

// decodeTXT strips the outer double quotes Route 53 stores around TXT
// record values.
func decodeTXT(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		u, err := strconvUnquote(v)
		if err == nil {
			return u
		}
		return v[1 : len(v)-1]
	}
	return v
}

func strconvUnquote(s string) (string, error) {
	// Local unquote that accepts only the double-quoted form Route 53
	// produces. Reuses strconv when the input is a valid Go string
	// literal; falls back to simple strip when it isn't.
	if u, err := unquoteSimple(s); err == nil {
		return u, nil
	}
	return s, errors.New("not a quoted string")
}

func unquoteSimple(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s, errors.New("not quoted")
	}
	return s[1 : len(s)-1], nil
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

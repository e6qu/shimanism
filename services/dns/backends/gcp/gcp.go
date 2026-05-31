// Package gcp is the GCP Cloud DNS passthrough backend. It uses the
// REST client at google.golang.org/api/dns/v1 (canonical per
// AGENTS.md § Reuse over reinvention) to drive real Cloud DNS or a
// sockerless-pointed client for tests.
//
// Cloud DNS identifies managed zones by a project-unique `Name` field
// that is **separate** from the zone's DnsName. The shim's domain
// identifies zones by DNS name only, so the backend derives the
// Cloud DNS Name deterministically from the canonical DNS name —
// replacing dots with dashes and stripping the trailing dot
// (matching Cloud DNS's [a-z][a-z0-9-]* constraint). No shim-side
// name→id table.
//
// Visibility dispatch follows N17: VisibilityPrivate creates a
// private-visibility zone with PrivateVisibilityConfig.Networks
// derived from PrivateVPCs; VisibilityPublic creates a
// public-visibility zone.
package gcp

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/googleapi"

	dns "google.golang.org/api/dns/v1"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

// Backend implements domain.DNS via real GCP Cloud DNS.
type Backend struct {
	c       *dns.Service
	project string
}

// New wraps an already-configured Cloud DNS service for the given
// project.
func New(svc *dns.Service, project string) *Backend {
	return &Backend{c: svc, project: project}
}

var _ domain.DNS = (*Backend)(nil)

func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// managedZoneName derives the Cloud DNS resource name from a DNS
// zone name. "example.com." → "example-com". Stateless mapping;
// the shim never stores a name→id table.
func managedZoneName(dnsName string) string {
	bare := strings.TrimSuffix(canonicalize(dnsName), ".")
	bare = strings.ReplaceAll(bare, ".", "-")
	// Cloud DNS Name constraints: 1-63 chars, begin with letter,
	// end with letter/digit. Trim a leading digit/dash by prefixing.
	if bare == "" {
		bare = "shim"
	} else if c := bare[0]; c < 'a' || c > 'z' {
		bare = "z" + bare
	}
	if len(bare) > 63 {
		bare = bare[:63]
	}
	return bare
}

// translateErr maps Cloud DNS HTTP errors to domain errors using
// `googleapi.Error.Code` (the HTTP status).
func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch ge.Code {
		case http.StatusNotFound:
			return domain.NoSuchZone(name)
		case http.StatusConflict:
			return domain.ZoneAlreadyExists(name)
		case http.StatusBadRequest:
			return domain.InvalidArgument(ge.Message)
		}
	}
	return err
}

func (b *Backend) CreateZone(ctx context.Context, name string, opt domain.CreateZoneOptions) (domain.Zone, error) {
	canonical := canonicalize(name)
	mz := &dns.ManagedZone{
		Name:        managedZoneName(canonical),
		DnsName:     canonical,
		Description: opt.Description,
		Labels:      copyTags(opt.Tags),
	}
	if opt.Visibility == domain.VisibilityPrivate {
		mz.Visibility = "private"
		if len(opt.PrivateVPCs) > 0 {
			cfg := &dns.ManagedZonePrivateVisibilityConfig{}
			for _, vpc := range opt.PrivateVPCs {
				cfg.Networks = append(cfg.Networks, &dns.ManagedZonePrivateVisibilityConfigNetwork{
					NetworkUrl: vpc,
				})
			}
			mz.PrivateVisibilityConfig = cfg
		}
	} else {
		mz.Visibility = "public"
	}
	out, err := b.c.ManagedZones.Create(b.project, mz).Context(ctx).Do()
	if err != nil {
		return domain.Zone{}, translateErr(err, canonical)
	}
	return zoneFromGCP(out), nil
}

func (b *Backend) GetZone(ctx context.Context, name string) (domain.Zone, error) {
	canonical := canonicalize(name)
	out, err := b.c.ManagedZones.Get(b.project, managedZoneName(canonical)).Context(ctx).Do()
	if err != nil {
		return domain.Zone{}, translateErr(err, canonical)
	}
	return zoneFromGCP(out), nil
}

func (b *Backend) DeleteZone(ctx context.Context, name string, force bool) error {
	canonical := canonicalize(name)
	zoneID := managedZoneName(canonical)
	if force {
		if err := b.deleteUserRecordSets(ctx, zoneID, canonical); err != nil {
			return err
		}
	}
	err := b.c.ManagedZones.Delete(b.project, zoneID).Context(ctx).Do()
	if err != nil {
		// Cloud DNS reports `containerNotEmpty` (HTTP 412) when records remain.
		var ge *googleapi.Error
		if errors.As(err, &ge) && (ge.Code == http.StatusPreconditionFailed ||
			(ge.Code == http.StatusBadRequest && strings.Contains(ge.Message, "not empty"))) {
			return domain.ZoneNotEmpty(canonical)
		}
		return translateErr(err, canonical)
	}
	return nil
}

func (b *Backend) ListZones(ctx context.Context, opt domain.ListZonesOptions) (domain.ListZonesResult, error) {
	call := b.c.ManagedZones.List(b.project).Context(ctx)
	if opt.PageSize > 0 {
		call = call.MaxResults(int64(opt.PageSize))
	}
	if opt.PageToken != "" {
		call = call.PageToken(opt.PageToken)
	}
	out, err := call.Do()
	if err != nil {
		return domain.ListZonesResult{}, translateErr(err, "")
	}
	res := domain.ListZonesResult{}
	for _, mz := range out.ManagedZones {
		z := zoneFromGCP(mz)
		if opt.NamePrefix != "" && !strings.HasPrefix(z.Name, canonicalize(opt.NamePrefix)) {
			continue
		}
		if opt.VisibilityFilter != domain.VisibilityUnknown && z.Visibility != opt.VisibilityFilter {
			continue
		}
		res.Zones = append(res.Zones, z)
	}
	res.NextPageToken = out.NextPageToken
	return res, nil
}

func (b *Backend) PutRecordSet(ctx context.Context, zone string, rs domain.RecordSet) error {
	canonical := canonicalize(zone)
	zoneID := managedZoneName(canonical)
	// Cloud DNS uses Changes.Create with paired (deletions, additions)
	// to atomically replace a record set. List existing first so we
	// can emit the matching deletion, then send the new value as an
	// addition. Stateless — no shim-side caching.
	existing, err := b.findRecordSet(ctx, zoneID, rs.Name, rs.Type)
	if err != nil {
		return err
	}
	change := &dns.Change{
		Additions: []*dns.ResourceRecordSet{gcpRecordSetFromDomain(rs)},
	}
	if existing != nil {
		change.Deletions = []*dns.ResourceRecordSet{existing}
	}
	_, err = b.c.Changes.Create(b.project, zoneID, change).Context(ctx).Do()
	return translateErr(err, rs.Name)
}

func (b *Backend) GetRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) (domain.RecordSet, error) {
	canonicalZone := canonicalize(zone)
	zoneID := managedZoneName(canonicalZone)
	rs, err := b.findRecordSet(ctx, zoneID, name, rtype)
	if err != nil {
		return domain.RecordSet{}, err
	}
	if rs == nil {
		return domain.RecordSet{}, domain.NoSuchRecordSet(canonicalZone, canonicalize(name), rtype)
	}
	return domainRecordSetFromGCP(rs), nil
}

func (b *Backend) DeleteRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) error {
	canonicalZone := canonicalize(zone)
	zoneID := managedZoneName(canonicalZone)
	existing, err := b.findRecordSet(ctx, zoneID, name, rtype)
	if err != nil {
		return err
	}
	if existing == nil {
		return domain.NoSuchRecordSet(canonicalZone, canonicalize(name), rtype)
	}
	_, err = b.c.Changes.Create(b.project, zoneID, &dns.Change{
		Deletions: []*dns.ResourceRecordSet{existing},
	}).Context(ctx).Do()
	return translateErr(err, name)
}

func (b *Backend) ListRecordSets(ctx context.Context, zone string, opt domain.ListRecordSetsOptions) (domain.ListRecordSetsResult, error) {
	canonicalZone := canonicalize(zone)
	zoneID := managedZoneName(canonicalZone)
	call := b.c.ResourceRecordSets.List(b.project, zoneID).Context(ctx)
	if opt.NameFilter != "" {
		call = call.Name(canonicalize(opt.NameFilter))
	}
	if opt.TypeFilter != "" {
		call = call.Type(string(opt.TypeFilter))
	}
	if opt.PageSize > 0 {
		call = call.MaxResults(int64(opt.PageSize))
	}
	if opt.PageToken != "" {
		call = call.PageToken(opt.PageToken)
	}
	out, err := call.Do()
	if err != nil {
		return domain.ListRecordSetsResult{}, translateErr(err, canonicalZone)
	}
	res := domain.ListRecordSetsResult{}
	for _, rs := range out.Rrsets {
		res.RecordSets = append(res.RecordSets, domainRecordSetFromGCP(rs))
	}
	res.NextPageToken = out.NextPageToken
	return res, nil
}

func (b *Backend) findRecordSet(ctx context.Context, zoneID, name string, rtype domain.RecordType) (*dns.ResourceRecordSet, error) {
	canonical := canonicalize(name)
	out, err := b.c.ResourceRecordSets.List(b.project, zoneID).
		Name(canonical).
		Type(string(rtype)).
		Context(ctx).
		Do()
	if err != nil {
		return nil, translateErr(err, name)
	}
	for _, rs := range out.Rrsets {
		if canonicalize(rs.Name) == canonical && rs.Type == string(rtype) {
			return rs, nil
		}
	}
	return nil, nil
}

func (b *Backend) deleteUserRecordSets(ctx context.Context, zoneID, zoneName string) error {
	apex := zoneName
	call := b.c.ResourceRecordSets.List(b.project, zoneID).Context(ctx)
	out, err := call.Do()
	if err != nil {
		return translateErr(err, zoneName)
	}
	var deletions []*dns.ResourceRecordSet
	for _, rs := range out.Rrsets {
		// Skip cloud-managed SOA + apex NS records — Cloud DNS owns them.
		if rs.Type == "SOA" {
			continue
		}
		if rs.Type == "NS" && canonicalize(rs.Name) == apex {
			continue
		}
		deletions = append(deletions, rs)
	}
	if len(deletions) == 0 {
		return nil
	}
	_, err = b.c.Changes.Create(b.project, zoneID, &dns.Change{Deletions: deletions}).Context(ctx).Do()
	return translateErr(err, zoneName)
}

func zoneFromGCP(mz *dns.ManagedZone) domain.Zone {
	z := domain.Zone{
		Name:        canonicalize(mz.DnsName),
		Description: mz.Description,
		NameServers: append([]string(nil), mz.NameServers...),
		Tags:        copyTags(mz.Labels),
	}
	switch mz.Visibility {
	case "private":
		z.Visibility = domain.VisibilityPrivate
	case "public", "":
		z.Visibility = domain.VisibilityPublic
	}
	if mz.CreationTime != "" {
		if t, err := time.Parse(time.RFC3339, mz.CreationTime); err == nil {
			z.CreatedAt = t
		}
	}
	return z
}

func gcpRecordSetFromDomain(rs domain.RecordSet) *dns.ResourceRecordSet {
	out := &dns.ResourceRecordSet{
		Name:    canonicalize(rs.Name),
		Type:    string(rs.Type),
		Ttl:     int64(rs.TTL),
		Rrdatas: append([]string(nil), rs.Records...),
	}
	return out
}

func domainRecordSetFromGCP(rs *dns.ResourceRecordSet) domain.RecordSet {
	out := domain.RecordSet{
		Name:    canonicalize(rs.Name),
		Type:    domain.RecordType(rs.Type),
		TTL:     int(rs.Ttl),
		Records: append([]string(nil), rs.Rrdatas...),
	}
	return out
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = in[k]
	}
	return out
}

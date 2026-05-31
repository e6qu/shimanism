// Package aws_route53 is the AWS Route 53 frontend adapter. It
// implements gen.AWSDnsV20130401Backend (the union of all 8
// per-operation interfaces the codegen emits) by translating between
// the AWS Route 53 wire types and the neutral domain.DNS interface.
//
// Route 53's restXml protocol uses the wrapped error envelope
// (`<ErrorResponse><Error>...</Error></ErrorResponse>`); the generated
// handlers route errors through restxml.WriteBackendErrorWrapped per
// the spec's `aws.protocols#restXml` trait (noErrorWrapping unset).
//
// The Route 53 HostedZoneId is the destination cloud's identifier;
// the shim's domain identifies zones by DNS name. The adapter forges
// a deterministic synthetic ID from the canonical zone name so SDK
// clients that round-trip the ID (e.g. `aws_route53_record` with
// `zone_id = aws_route53_zone.x.zone_id`) keep working. The actual
// resolution of name → backend HostedZoneId stays in the backend
// (see services/dns/backends/aws/aws.go::resolveZoneID).
package aws_route53

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/dns/domain"
	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/dns/gen"
)

// Adapter satisfies gen.AWSDnsV20130401Backend by wrapping a domain.DNS.
type Adapter struct {
	d domain.DNS
}

// New wraps the given domain.DNS implementation as a Route 53 frontend.
func New(d domain.DNS) *Adapter { return &Adapter{d: d} }

var _ gen.AWSDnsV20130401Backend = (*Adapter)(nil)

// Handler builds the HTTP handler chain: SigV4 verifier middleware →
// restxml.Router → per-op gen handlers → this Adapter.
//
// Test-mode signing key — single AccessKey/Secret pair the shim
// trusts whenever SHIMANISM_TEST_UNAUTHENTICATED isn't set. Real-cloud
// lanes wire their own CredentialStore.
func Handler(d domain.DNS) http.Handler {
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{
		Service: "route53",
		Region:  "us-east-1",
	})
	mw := sigv4verifier.Middleware(verifier, restxml.WriteErrorWrapped)
	router := &restxml.Router{}
	gen.RegisterAWSDnsV20130401Routes(router, New(d))
	return mw(router)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// hostedZoneID forges a deterministic 14-char Z<hex> identifier from
// the canonical zone name. Real Route 53 IDs are also a leading "Z"
// followed by uppercase hex characters; SDK clients treat them as
// opaque strings so any conforming shape works. Round-tripping the
// shim's ID through name resolution is unnecessary — the backend
// looks zones up by DNS name (the domain identifier), not by
// HostedZoneId.
func hostedZoneID(name string) string {
	sum := sha256.Sum256([]byte(canonicalize(name)))
	return "Z" + strings.ToUpper(hex.EncodeToString(sum[:6]))
}

func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// stripHostedZonePrefix accepts either "Z123..." or "/hostedzone/Z123..."
// (Route 53 returns the latter in some responses; the SDK and CLI
// emit either form). Returns the bare ID.
func stripHostedZonePrefix(id string) string {
	id = strings.TrimPrefix(id, "/hostedzone/")
	return strings.TrimPrefix(id, "hostedzone/")
}

// changeInfo wraps a synthetic ChangeInfo response. Route 53 is
// asynchronous (changes go through PENDING → INSYNC); the shim is
// synchronous, so every ChangeInfo reports INSYNC immediately.
func changeInfo(id string) *gen.ChangeInfo {
	return &gen.ChangeInfo{
		Id:     "/change/" + id,
		Status: gen.ChangeStatusINSYNC,
	}
}

// nameFromHostedZoneID is the inverse mapping the adapter uses when a
// request addresses the zone by HostedZoneId (GetHostedZone,
// DeleteHostedZone, ChangeResourceRecordSets, ListResourceRecordSets).
// Since the shim's identity is the DNS name, the adapter caches a
// per-request id→name mapping by listing zones and matching. To keep
// the shim stateless we just enumerate via ListZones each call —
// cheap on test fixtures, and the production path can paginate.
func (a *Adapter) nameFromHostedZoneID(ctx context.Context, id string) (string, error) {
	id = stripHostedZonePrefix(id)
	res, err := a.d.ListZones(ctx, domain.ListZonesOptions{})
	if err != nil {
		return "", err
	}
	for _, z := range res.Zones {
		if hostedZoneID(z.Name) == id {
			return z.Name, nil
		}
	}
	return "", domain.NoSuchZone(id)
}

// mapError translates a domain.Error into the Route 53-vocabulary
// ShimError that the generated handler writes through
// restxml.WriteBackendErrorWrapped. Non-domain errors fall through as
// HTTP 500 InternalError.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return err
	}
	switch de.Kind {
	case domain.KindNoSuchZone:
		return &restxml.ShimError{
			HTTPStatus: http.StatusNotFound,
			Code:       "NoSuchHostedZone",
			Message:    "No hosted zone found with the given name or ID",
		}
	case domain.KindZoneAlreadyExists:
		return &restxml.ShimError{
			HTTPStatus: http.StatusConflict,
			Code:       "HostedZoneAlreadyExists",
			Message:    "A hosted zone with the given name already exists",
		}
	case domain.KindZoneNotEmpty:
		return &restxml.ShimError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "HostedZoneNotEmpty",
			Message:    "The hosted zone contains resource record sets in addition to the SOA + NS records",
		}
	case domain.KindNoSuchRecordSet:
		return &restxml.ShimError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidChangeBatch",
			Message:    de.Message,
		}
	case domain.KindInvalidArgument:
		return &restxml.ShimError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidInput",
			Message:    de.Message,
		}
	case domain.KindUnsupported:
		return &restxml.ShimError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidInput",
			Message:    de.Message,
		}
	}
	return err
}

// ----------------------------------------------------------------------
// CreateHostedZone
// ----------------------------------------------------------------------

func (a *Adapter) CreateHostedZone(ctx context.Context, in *gen.CreateHostedZoneRequest) (*gen.CreateHostedZoneResponse, error) {
	name := canonicalize(in.Name)
	opt := domain.CreateZoneOptions{
		Visibility: domain.VisibilityPublic,
	}
	if in.HostedZoneConfig != nil {
		if in.HostedZoneConfig.Comment != nil {
			opt.Description = *in.HostedZoneConfig.Comment
		}
		if in.HostedZoneConfig.PrivateZone != nil && *in.HostedZoneConfig.PrivateZone {
			opt.Visibility = domain.VisibilityPrivate
		}
	}
	if in.VPC != nil {
		opt.Visibility = domain.VisibilityPrivate
		if in.VPC.VPCId != nil {
			opt.PrivateVPCs = []string{*in.VPC.VPCId}
		}
	}
	z, err := a.d.CreateZone(ctx, name, opt)
	if err != nil {
		return nil, mapError(err)
	}
	id := hostedZoneID(z.Name)
	resp := &gen.CreateHostedZoneResponse{
		HostedZone: zoneToHostedZone(z, id),
		ChangeInfo: changeInfo(id),
		Location:   "/2013-04-01/hostedzone/" + id,
	}
	if z.Visibility == domain.VisibilityPublic && len(z.NameServers) > 0 {
		resp.DelegationSet = &gen.DelegationSet{
			NameServers: gen.DelegationSetNameServers{Items: append([]string(nil), z.NameServers...)},
		}
	}
	if in.VPC != nil {
		resp.VPC = in.VPC
	}
	return resp, nil
}

// ----------------------------------------------------------------------
// DeleteHostedZone
// ----------------------------------------------------------------------

func (a *Adapter) DeleteHostedZone(ctx context.Context, in *gen.DeleteHostedZoneRequest) (*gen.DeleteHostedZoneResponse, error) {
	name, err := a.nameFromHostedZoneID(ctx, in.Id)
	if err != nil {
		return nil, mapError(err)
	}
	// Route 53 refuses to delete a zone with user-managed record sets.
	// force=false matches that behaviour.
	if err := a.d.DeleteZone(ctx, name, false); err != nil {
		return nil, mapError(err)
	}
	return &gen.DeleteHostedZoneResponse{
		ChangeInfo: changeInfo(hostedZoneID(name)),
	}, nil
}

// ----------------------------------------------------------------------
// GetHostedZone
// ----------------------------------------------------------------------

func (a *Adapter) GetHostedZone(ctx context.Context, in *gen.GetHostedZoneRequest) (*gen.GetHostedZoneResponse, error) {
	name, err := a.nameFromHostedZoneID(ctx, in.Id)
	if err != nil {
		return nil, mapError(err)
	}
	z, err := a.d.GetZone(ctx, name)
	if err != nil {
		return nil, mapError(err)
	}
	id := hostedZoneID(z.Name)
	resp := &gen.GetHostedZoneResponse{
		HostedZone: zoneToHostedZone(z, id),
	}
	if z.Visibility == domain.VisibilityPublic && len(z.NameServers) > 0 {
		resp.DelegationSet = &gen.DelegationSet{
			NameServers: gen.DelegationSetNameServers{Items: append([]string(nil), z.NameServers...)},
		}
	}
	return resp, nil
}

// ----------------------------------------------------------------------
// ListHostedZones
// ----------------------------------------------------------------------

func (a *Adapter) ListHostedZones(ctx context.Context, in *gen.ListHostedZonesRequest) (*gen.ListHostedZonesResponse, error) {
	opt := domain.ListZonesOptions{
		PageSize:  int(deref(in.MaxItems, 0)),
		PageToken: deref(in.Marker, ""),
	}
	if in.HostedZoneType != nil {
		switch *in.HostedZoneType {
		case gen.HostedZoneTypePRIVATE_HOSTED_ZONE:
			opt.VisibilityFilter = domain.VisibilityPrivate
		}
	}
	res, err := a.d.ListZones(ctx, opt)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &gen.ListHostedZonesResponse{
		IsTruncated: res.NextPageToken != "",
		MaxItems:    int32(opt.PageSize),
		Marker:      opt.PageToken,
	}
	if res.NextPageToken != "" {
		resp.NextMarker = ptr(res.NextPageToken)
	}
	for _, z := range res.Zones {
		resp.HostedZones.Items = append(resp.HostedZones.Items, *zoneToHostedZone(z, hostedZoneID(z.Name)))
	}
	return resp, nil
}

// ----------------------------------------------------------------------
// ChangeResourceRecordSets
// ----------------------------------------------------------------------

func (a *Adapter) ChangeResourceRecordSets(ctx context.Context, in *gen.ChangeResourceRecordSetsRequest) (*gen.ChangeResourceRecordSetsResponse, error) {
	name, err := a.nameFromHostedZoneID(ctx, in.HostedZoneId)
	if err != nil {
		return nil, mapError(err)
	}
	if in.ChangeBatch == nil {
		return nil, mapError(domain.InvalidArgument("ChangeBatch is required"))
	}
	for _, c := range in.ChangeBatch.Changes.Items {
		if c.ResourceRecordSet == nil {
			continue
		}
		rs := resourceRecordSetToDomain(*c.ResourceRecordSet)
		switch c.Action {
		case gen.ChangeActionCREATE, gen.ChangeActionUPSERT:
			if err := a.d.PutRecordSet(ctx, name, rs); err != nil {
				return nil, mapError(err)
			}
		case gen.ChangeActionDELETE:
			if err := a.d.DeleteRecordSet(ctx, name, rs.Name, rs.Type); err != nil {
				return nil, mapError(err)
			}
		default:
			return nil, mapError(domain.InvalidArgument("unsupported change action: " + string(c.Action)))
		}
	}
	return &gen.ChangeResourceRecordSetsResponse{
		ChangeInfo: changeInfo(hostedZoneID(name)),
	}, nil
}

// ----------------------------------------------------------------------
// ListResourceRecordSets
// ----------------------------------------------------------------------

func (a *Adapter) ListResourceRecordSets(ctx context.Context, in *gen.ListResourceRecordSetsRequest) (*gen.ListResourceRecordSetsResponse, error) {
	name, err := a.nameFromHostedZoneID(ctx, in.HostedZoneId)
	if err != nil {
		return nil, mapError(err)
	}
	opt := domain.ListRecordSetsOptions{
		NameFilter: deref(in.StartRecordName, ""),
		PageSize:   int(deref(in.MaxItems, 0)),
		PageToken:  deref(in.StartRecordIdentifier, ""),
	}
	if in.StartRecordType != nil {
		opt.TypeFilter = domain.RecordType(*in.StartRecordType)
	}
	res, err := a.d.ListRecordSets(ctx, name, opt)
	if err != nil {
		return nil, mapError(err)
	}
	resp := &gen.ListResourceRecordSetsResponse{
		IsTruncated: res.NextPageToken != "",
		MaxItems:    int32(opt.PageSize),
	}
	if res.NextPageToken != "" {
		resp.NextRecordIdentifier = ptr(res.NextPageToken)
	}
	for _, rs := range res.RecordSets {
		resp.ResourceRecordSets.Items = append(resp.ResourceRecordSets.Items, recordSetToGen(rs))
	}
	return resp, nil
}

// ----------------------------------------------------------------------
// ChangeTagsForResource
// ----------------------------------------------------------------------

func (a *Adapter) ChangeTagsForResource(ctx context.Context, in *gen.ChangeTagsForResourceRequest) (*gen.ChangeTagsForResourceResponse, error) {
	// Tags-on-zones is in the conformance contract but the domain.DNS
	// interface keeps them as a creation-time `Tags` map per N3 (no
	// post-create mutation surface). Route 53's ChangeTagsForResource
	// against a hosted zone is in intersection; the shim acknowledges
	// the request and returns success. Tag mutation lands when the
	// domain interface adds it as a follow-on operation.
	if _, err := a.nameFromHostedZoneID(ctx, in.ResourceId); err != nil {
		return nil, mapError(err)
	}
	return &gen.ChangeTagsForResourceResponse{}, nil
}

// ----------------------------------------------------------------------
// ListTagsForResource
// ----------------------------------------------------------------------

func (a *Adapter) ListTagsForResource(ctx context.Context, in *gen.ListTagsForResourceRequest) (*gen.ListTagsForResourceResponse, error) {
	name, err := a.nameFromHostedZoneID(ctx, in.ResourceId)
	if err != nil {
		return nil, mapError(err)
	}
	z, err := a.d.GetZone(ctx, name)
	if err != nil {
		return nil, mapError(err)
	}
	rtype := in.ResourceType
	set := &gen.ResourceTagSet{
		ResourceId:   ptr(in.ResourceId),
		ResourceType: &rtype,
	}
	for k, v := range z.Tags {
		key, val := k, v
		set.Tags.Items = append(set.Tags.Items, gen.Tag{Key: &key, Value: &val})
	}
	return &gen.ListTagsForResourceResponse{ResourceTagSet: set}, nil
}

// ----------------------------------------------------------------------
// GetChange
// ----------------------------------------------------------------------

func (a *Adapter) GetChange(ctx context.Context, in *gen.GetChangeRequest) (*gen.GetChangeResponse, error) {
	// Route 53 is asynchronous (changes go PENDING → INSYNC); the shim
	// is synchronous, so every change is INSYNC by the time the client
	// can query it. Echo the requested ID back with INSYNC status.
	return &gen.GetChangeResponse{
		ChangeInfo: &gen.ChangeInfo{
			Id:     "/change/" + strings.TrimPrefix(in.Id, "/change/"),
			Status: gen.ChangeStatusINSYNC,
		},
	}, nil
}

// ----------------------------------------------------------------------
// Domain ↔ Route 53 translation
// ----------------------------------------------------------------------

func zoneToHostedZone(z domain.Zone, id string) *gen.HostedZone {
	hz := &gen.HostedZone{
		Id:   "/hostedzone/" + id,
		Name: z.Name,
	}
	if z.Description != "" || z.Visibility == domain.VisibilityPrivate {
		isPrivate := z.Visibility == domain.VisibilityPrivate
		hz.Config = &gen.HostedZoneConfig{
			Comment:     ptr(z.Description),
			PrivateZone: ptr(isPrivate),
		}
	}
	return hz
}

func resourceRecordSetToDomain(rs gen.ResourceRecordSet) domain.RecordSet {
	out := domain.RecordSet{
		Name: canonicalize(rs.Name),
		Type: domain.RecordType(rs.Type),
		TTL:  int(deref(rs.TTL, 0)),
	}
	for _, r := range rs.ResourceRecords.Items {
		out.Records = append(out.Records, decodeRoute53Value(string(rs.Type), r.Value))
	}
	return out
}

func recordSetToGen(rs domain.RecordSet) gen.ResourceRecordSet {
	out := gen.ResourceRecordSet{
		Name: rs.Name,
		Type: gen.RRType(rs.Type),
		TTL:  ptr(int64(rs.TTL)),
	}
	for _, v := range rs.Records {
		out.ResourceRecords.Items = append(out.ResourceRecords.Items, gen.ResourceRecord{
			Value: encodeRoute53Value(string(rs.Type), v),
		})
	}
	return out
}

// decodeRoute53Value strips the outer quotes Route 53 wraps around
// TXT record values. Mirrors the backend's decodeTXT.
func decodeRoute53Value(rtype, v string) string {
	if rtype != "TXT" {
		return v
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// encodeRoute53Value adds the outer quotes Route 53 expects on TXT
// record values, idempotent.
func encodeRoute53Value(rtype, v string) string {
	if rtype != "TXT" {
		return v
	}
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v
	}
	return `"` + v + `"`
}

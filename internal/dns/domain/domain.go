// Package domain holds shimanism's neutral DNS interface and
// types. The interface is the lingua franca between the three
// frontends (AWS Route 53, GCP Cloud DNS, Azure DNS public +
// private) and the four backends (AWS / GCP / Azure / CoreDNS) +
// the inmem testing backend.
//
// Phase 15.D scoping in docs/phase-15-cd-scoping.md.
//
// **Stateless.** No per-process maps. Zones + record sets live in
// the destination cloud (or the inmem backend's map for tests).
package domain

import (
	"context"
	"time"
)

// ZoneVisibility distinguishes public-internet zones from private
// (cluster / VPC-scoped) zones. Domain field; per-cloud backends
// dispatch on it. Published as normalisation rule N17 in
// `docs/normalizations.md`.
type ZoneVisibility int

const (
	VisibilityUnknown ZoneVisibility = iota
	VisibilityPublic
	VisibilityPrivate
)

func (v ZoneVisibility) String() string {
	switch v {
	case VisibilityPublic:
		return "public"
	case VisibilityPrivate:
		return "private"
	default:
		return "unknown"
	}
}

// RecordType enumerates the in-intersection DNS record types. AAAA,
// A, CNAME, MX, NS, SOA, SRV, TXT are supported across AWS Route
// 53 / GCP Cloud DNS / Azure DNS / Azure Private DNS / CoreDNS.
// Vendor-specific record types (e.g. Route 53 ALIAS) are out of
// intersection.
type RecordType string

const (
	RecordTypeA     RecordType = "A"
	RecordTypeAAAA  RecordType = "AAAA"
	RecordTypeCNAME RecordType = "CNAME"
	RecordTypeMX    RecordType = "MX"
	RecordTypeNS    RecordType = "NS"
	RecordTypeSOA   RecordType = "SOA"
	RecordTypeSRV   RecordType = "SRV"
	RecordTypeTXT   RecordType = "TXT"
)

// Zone describes a managed DNS zone in shimanism's neutral form.
// Backends translate to/from cloud-native representations
// (Route 53 HostedZone, Cloud DNS ManagedZone, Azure DnsZone /
// PrivateDnsZone).
type Zone struct {
	// Name is the zone's DNS name, with trailing dot (e.g.
	// "example.com."). The shim canonicalises on write +
	// round-trips on read.
	Name string

	// Visibility controls whether the zone is public-internet or
	// private/cluster-scoped. See N17 in
	// `docs/normalizations.md`.
	Visibility ZoneVisibility

	// NameServers is the set of authoritative servers the destination
	// cloud assigned (Route 53 returns four; Cloud DNS returns four;
	// Azure DNS returns four). Empty for private zones (private
	// zones resolve via the cloud's resolver, not via public NS).
	NameServers []string

	// Description is a free-text label round-tripped via the
	// destination cloud's native description / labels field. GCP
	// has no native description; the GCP backend stores it as a
	// reserved label `shim-description` per N4.
	Description string

	// Tags map to AWS tags / GCP labels (constraints per N3) /
	// Azure tags.
	Tags map[string]string

	// CreatedAt is when the destination cloud reports the zone
	// was created. Zero when the destination cloud doesn't expose
	// a creation timestamp (e.g. CoreDNS reading a static file).
	CreatedAt time.Time
}

// RecordSet is a set of resource records sharing the same name +
// type within a zone. Cloud APIs vary on whether a "record set"
// is one resource (Route 53 / Azure DNS) or one record per row
// (Cloud DNS uses ResourceRecordSet which is the same set
// concept). The shim uses the set abstraction uniformly.
type RecordSet struct {
	// Name is the record's FQDN with trailing dot (e.g.
	// "www.example.com."). The shim accepts both bare and dotted
	// names and canonicalises to dotted.
	Name string

	// Type names the DNS record type.
	Type RecordType

	// TTL in seconds. Bounds enforced per-cloud; sub-minimum
	// values default to the cloud's minimum, super-maximum
	// values fail with the source cloud's error envelope.
	TTL int

	// Records is the record-type-specific encoding. For A:
	// IPv4 dotted-quad strings. For AAAA: IPv6 strings. For
	// MX: "<priority> <target>." entries. For SRV:
	// "<priority> <weight> <port> <target>." entries. For TXT:
	// the unquoted string contents (the shim re-quotes per the
	// destination cloud's API). For CNAME / NS / SOA: a single
	// FQDN with trailing dot.
	Records []string
}

// CreateZoneOptions controls CreateZone.
type CreateZoneOptions struct {
	Visibility  ZoneVisibility
	Description string
	Tags        map[string]string

	// PrivateVPCs (private-only) is the list of cloud-native VPC /
	// network identifiers the private zone is bound to. The set is
	// passed through as opaque strings; the destination cloud
	// validates. Empty when Visibility == VisibilityPublic.
	//
	// Cross-cloud Apply involving private zones with concrete VPC
	// IDs falls out of intersection (each cloud's VPC namespace is
	// vendor-specific). Listed in `services/dns/APPLY_INTERSECTION.md`.
	PrivateVPCs []string
}

// ListZonesOptions controls ListZones pagination + filtering.
type ListZonesOptions struct {
	// NamePrefix optionally filters to zones whose name starts
	// with the prefix. Empty means no filter.
	NamePrefix string

	// VisibilityFilter optionally restricts to public-only or
	// private-only. VisibilityUnknown means no filter (return both).
	VisibilityFilter ZoneVisibility

	// PageSize is a hint; backends may return fewer or more.
	PageSize int

	// PageToken is the cloud's continuation token; opaque to the
	// shim.
	PageToken string
}

// ListZonesResult is the ListZones response.
type ListZonesResult struct {
	Zones         []Zone
	NextPageToken string
}

// ListRecordSetsOptions controls ListRecordSets pagination +
// filtering.
type ListRecordSetsOptions struct {
	NameFilter string     // empty = no filter
	TypeFilter RecordType // empty = all types
	PageSize   int
	PageToken  string
}

// ListRecordSetsResult is the ListRecordSets response.
type ListRecordSetsResult struct {
	RecordSets    []RecordSet
	NextPageToken string
}

// DNS is the interface every DNS backend implements. The shim's
// frontends (Route 53 / Cloud DNS / Azure DNS) translate cloud-
// native API calls into these methods. The shim's backends
// translate these into destination-cloud-native API calls.
type DNS interface {
	// CreateZone creates a new managed zone. Returns the zone as
	// the destination cloud reports it (including NameServers
	// once the destination cloud has assigned them).
	CreateZone(ctx context.Context, name string, opt CreateZoneOptions) (Zone, error)

	// GetZone returns the named zone's current state.
	GetZone(ctx context.Context, name string) (Zone, error)

	// DeleteZone removes the zone. `force=true` removes any
	// remaining record sets (skipping the SOA + NS records the
	// cloud manages itself); `force=false` returns an error if
	// the zone still has user-managed record sets.
	DeleteZone(ctx context.Context, name string, force bool) error

	// ListZones enumerates managed zones, optionally filtered.
	ListZones(ctx context.Context, opt ListZonesOptions) (ListZonesResult, error)

	// PutRecordSet creates-or-replaces the record set at
	// (zone, rs.Name, rs.Type). Atomic per-set; the shim's
	// frontends collapse cloud APIs that allow per-record edits
	// (Route 53 ChangeResourceRecordSets, Azure RecordSets.Put)
	// into this set-level abstraction.
	PutRecordSet(ctx context.Context, zone string, rs RecordSet) error

	// GetRecordSet returns the named (name, type) set, or
	// NoSuchRecordSet if absent.
	GetRecordSet(ctx context.Context, zone, name string, rtype RecordType) (RecordSet, error)

	// DeleteRecordSet removes the named (name, type) set.
	DeleteRecordSet(ctx context.Context, zone, name string, rtype RecordType) error

	// ListRecordSets enumerates record sets in the zone.
	ListRecordSets(ctx context.Context, zone string, opt ListRecordSetsOptions) (ListRecordSetsResult, error)
}

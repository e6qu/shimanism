// Package azure is the Azure DNS + Private DNS passthrough backend.
// One backend dispatches on `domain.ZoneVisibility` (N17): public
// zones route through armdns (`Microsoft.Network/dnsZones`), private
// zones route through armprivatedns (`Microsoft.Network/privateDnsZones`).
// The same domain.DNS surface drives both ARM resource types — the
// dispatch lives at the backend boundary, not in the domain.
//
// Azure DNS identifies zones by ARM resource ID:
// `/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/<dnsZones|privateDnsZones>/{name}`.
// The shim's domain identifies zones by DNS name only; the backend
// uses the configured `(subscription, resource group)` plus the zone
// name verbatim (Azure accepts the DNS name without trailing dot).
package azure

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

// Backend implements domain.DNS via Azure DNS (public) + Azure
// Private DNS (private). Dispatch on Visibility per N17.
type Backend struct {
	subscriptionID string
	resourceGroup  string
	pubZones       *armdns.ZonesClient
	pubRRSets      *armdns.RecordSetsClient
	privZones      *armprivatedns.PrivateZonesClient
	privRRSets     *armprivatedns.RecordSetsClient
}

// Options carries the per-backend ARM context. SubscriptionID + ResourceGroup
// are required; ClientOptions is propagated to both `armdns` and
// `armprivatedns` client factories.
type Options struct {
	SubscriptionID string
	ResourceGroup  string
	ClientOptions  *arm.ClientOptions
}

// New constructs a backend bound to the given Azure subscription +
// resource group, authenticated with the supplied credential.
func New(cred azcore.TokenCredential, opt Options) (*Backend, error) {
	if opt.SubscriptionID == "" {
		return nil, fmt.Errorf("azure dns backend: SubscriptionID required")
	}
	if opt.ResourceGroup == "" {
		return nil, fmt.Errorf("azure dns backend: ResourceGroup required")
	}
	pubFactory, err := armdns.NewClientFactory(opt.SubscriptionID, cred, opt.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("azure dns ClientFactory: %w", err)
	}
	privFactory, err := armprivatedns.NewClientFactory(opt.SubscriptionID, cred, opt.ClientOptions)
	if err != nil {
		return nil, fmt.Errorf("azure privatedns ClientFactory: %w", err)
	}
	return &Backend{
		subscriptionID: opt.SubscriptionID,
		resourceGroup:  opt.ResourceGroup,
		pubZones:       pubFactory.NewZonesClient(),
		pubRRSets:      pubFactory.NewRecordSetsClient(),
		privZones:      privFactory.NewPrivateZonesClient(),
		privRRSets:     privFactory.NewRecordSetsClient(),
	}, nil
}

var _ domain.DNS = (*Backend)(nil)

// zoneNameFromDomain strips the trailing dot Azure DNS doesn't use.
func zoneNameFromDomain(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.TrimSuffix(n, ".")
}

// canonicalize returns the trailing-dot form the shim's domain uses.
func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// recordSetName encodes the relative record name within a zone. Azure
// uses "@" for the apex; the shim's domain uses the fully-qualified
// name. Convert in both directions.
func recordSetNameForAzure(zone, fqdn string) string {
	zoneBare := strings.TrimSuffix(zone, ".")
	bare := strings.TrimSuffix(fqdn, ".")
	if bare == zoneBare {
		return "@"
	}
	if suffix := "." + zoneBare; strings.HasSuffix(bare, suffix) {
		return strings.TrimSuffix(bare, suffix)
	}
	return bare
}

func recordSetNameFromAzure(zone, relative string) string {
	if relative == "@" {
		return canonicalize(zone)
	}
	return canonicalize(relative + "." + strings.TrimSuffix(zone, "."))
}

// translateErr maps Azure SDK errors to domain errors using
// `azcore/response.ResponseError.ErrorCode`.
func translateErr(err error, name string) error {
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch {
		case re.StatusCode == 404, re.ErrorCode == "NotFound", re.ErrorCode == "ResourceNotFound":
			return domain.NoSuchZone(name)
		case re.StatusCode == 409, re.ErrorCode == "Conflict":
			return domain.ZoneAlreadyExists(name)
		case re.StatusCode == 400, re.ErrorCode == "InvalidArgument", re.ErrorCode == "BadRequest":
			return domain.InvalidArgument(re.ErrorCode + ": " + re.RawResponse.Status)
		}
	}
	return err
}

func (b *Backend) CreateZone(ctx context.Context, name string, opt domain.CreateZoneOptions) (domain.Zone, error) {
	zoneName := zoneNameFromDomain(name)
	tags := tagsToAzure(opt.Tags)
	if opt.Visibility == domain.VisibilityPrivate {
		params := armprivatedns.PrivateZone{
			Location: to.Ptr("global"),
			Tags:     tags,
		}
		poller, err := b.privZones.BeginCreateOrUpdate(ctx, b.resourceGroup, zoneName, params, nil)
		if err != nil {
			return domain.Zone{}, translateErr(err, zoneName)
		}
		res, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return domain.Zone{}, translateErr(err, zoneName)
		}
		// Private DNS doesn't return NS records — clients resolve via the
		// private resolver / VNet link, not via public NS records.
		return privateZoneFromAzure(&res.PrivateZone, opt.Description), nil
	}
	params := armdns.Zone{
		Location: to.Ptr("global"),
		Tags:     tags,
	}
	res, err := b.pubZones.CreateOrUpdate(ctx, b.resourceGroup, zoneName, params, nil)
	if err != nil {
		return domain.Zone{}, translateErr(err, zoneName)
	}
	return publicZoneFromAzure(&res.Zone, opt.Description), nil
}

func (b *Backend) GetZone(ctx context.Context, name string) (domain.Zone, error) {
	zoneName := zoneNameFromDomain(name)
	// Try public first; on NotFound, try private. Same name-resolution
	// caveat as Route 53: a zone present in both public + private form
	// returns the public one. Disambiguation via ListZones(VisibilityFilter).
	pub, err := b.pubZones.Get(ctx, b.resourceGroup, zoneName, nil)
	if err == nil {
		return publicZoneFromAzure(&pub.Zone, ""), nil
	}
	if !isNotFound(err) {
		return domain.Zone{}, translateErr(err, zoneName)
	}
	priv, err := b.privZones.Get(ctx, b.resourceGroup, zoneName, nil)
	if err != nil {
		return domain.Zone{}, translateErr(err, zoneName)
	}
	return privateZoneFromAzure(&priv.PrivateZone, ""), nil
}

func (b *Backend) DeleteZone(ctx context.Context, name string, force bool) error {
	zoneName := zoneNameFromDomain(name)
	// Determine which family by attempting a public Get first.
	if _, err := b.pubZones.Get(ctx, b.resourceGroup, zoneName, nil); err == nil {
		poller, err := b.pubZones.BeginDelete(ctx, b.resourceGroup, zoneName, nil)
		if err != nil {
			return translateErr(err, zoneName)
		}
		_, err = poller.PollUntilDone(ctx, nil)
		return translateErr(err, zoneName)
	} else if !isNotFound(err) {
		return translateErr(err, zoneName)
	}
	poller, err := b.privZones.BeginDelete(ctx, b.resourceGroup, zoneName, nil)
	if err != nil {
		return translateErr(err, zoneName)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return translateErr(err, zoneName)
}

func (b *Backend) ListZones(ctx context.Context, opt domain.ListZonesOptions) (domain.ListZonesResult, error) {
	res := domain.ListZonesResult{}
	// When no visibility filter is set, list both families. A 404 on
	// either family's list endpoint (e.g. some Azure mock surfaces
	// missing the `GET /privateDnsZones` route) is treated as
	// "no zones of that family" rather than propagating as the
	// translateErr → NoSuchZone — listing a non-existent family
	// returns an empty set, not an error, for real Azure too.
	listFailureFatal := opt.VisibilityFilter != domain.VisibilityUnknown
	if opt.VisibilityFilter != domain.VisibilityPrivate {
		pager := b.pubZones.NewListByResourceGroupPager(b.resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if !listFailureFatal && isNotFound(err) {
					break
				}
				return domain.ListZonesResult{}, translateErr(err, "")
			}
			for _, z := range page.Value {
				dz := publicZoneFromAzure(z, "")
				if opt.NamePrefix != "" && !strings.HasPrefix(dz.Name, canonicalize(opt.NamePrefix)) {
					continue
				}
				res.Zones = append(res.Zones, dz)
			}
		}
	}
	if opt.VisibilityFilter != domain.VisibilityPublic {
		pager := b.privZones.NewListByResourceGroupPager(b.resourceGroup, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				if !listFailureFatal && isNotFound(err) {
					break
				}
				return domain.ListZonesResult{}, translateErr(err, "")
			}
			for _, z := range page.Value {
				dz := privateZoneFromAzure(z, "")
				if opt.NamePrefix != "" && !strings.HasPrefix(dz.Name, canonicalize(opt.NamePrefix)) {
					continue
				}
				res.Zones = append(res.Zones, dz)
			}
		}
	}
	return res, nil
}

func (b *Backend) PutRecordSet(ctx context.Context, zone string, rs domain.RecordSet) error {
	zoneName := zoneNameFromDomain(zone)
	relName := recordSetNameForAzure(zoneName, rs.Name)
	// Decide which family by zone visibility — Get the zone first.
	isPrivate, err := b.isPrivate(ctx, zoneName)
	if err != nil {
		return err
	}
	if isPrivate {
		params := armprivatedns.RecordSet{Properties: privRRPropertiesFromDomain(rs)}
		_, err := b.privRRSets.CreateOrUpdate(ctx, b.resourceGroup, zoneName, armprivatedns.RecordType(rs.Type), relName, params, nil)
		return translateErr(err, rs.Name)
	}
	params := armdns.RecordSet{Properties: pubRRPropertiesFromDomain(rs)}
	_, err = b.pubRRSets.CreateOrUpdate(ctx, b.resourceGroup, zoneName, relName, armdns.RecordType(rs.Type), params, nil)
	return translateErr(err, rs.Name)
}

func (b *Backend) GetRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) (domain.RecordSet, error) {
	zoneName := zoneNameFromDomain(zone)
	relName := recordSetNameForAzure(zoneName, name)
	isPrivate, err := b.isPrivate(ctx, zoneName)
	if err != nil {
		return domain.RecordSet{}, err
	}
	if isPrivate {
		res, err := b.privRRSets.Get(ctx, b.resourceGroup, zoneName, armprivatedns.RecordType(rtype), relName, nil)
		if err != nil {
			if isNotFound(err) {
				return domain.RecordSet{}, domain.NoSuchRecordSet(zoneName, name, rtype)
			}
			return domain.RecordSet{}, translateErr(err, name)
		}
		return privDomainRRFromAzure(zoneName, &res.RecordSet, rtype), nil
	}
	res, err := b.pubRRSets.Get(ctx, b.resourceGroup, zoneName, relName, armdns.RecordType(rtype), nil)
	if err != nil {
		if isNotFound(err) {
			return domain.RecordSet{}, domain.NoSuchRecordSet(zoneName, name, rtype)
		}
		return domain.RecordSet{}, translateErr(err, name)
	}
	return pubDomainRRFromAzure(zoneName, &res.RecordSet, rtype), nil
}

func (b *Backend) DeleteRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) error {
	zoneName := zoneNameFromDomain(zone)
	relName := recordSetNameForAzure(zoneName, name)
	isPrivate, err := b.isPrivate(ctx, zoneName)
	if err != nil {
		return err
	}
	if isPrivate {
		_, err := b.privRRSets.Delete(ctx, b.resourceGroup, zoneName, armprivatedns.RecordType(rtype), relName, nil)
		return translateErr(err, name)
	}
	_, err = b.pubRRSets.Delete(ctx, b.resourceGroup, zoneName, relName, armdns.RecordType(rtype), nil)
	return translateErr(err, name)
}

func (b *Backend) ListRecordSets(ctx context.Context, zone string, opt domain.ListRecordSetsOptions) (domain.ListRecordSetsResult, error) {
	zoneName := zoneNameFromDomain(zone)
	isPrivate, err := b.isPrivate(ctx, zoneName)
	if err != nil {
		return domain.ListRecordSetsResult{}, err
	}
	res := domain.ListRecordSetsResult{}
	if isPrivate {
		pager := b.privRRSets.NewListPager(b.resourceGroup, zoneName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return domain.ListRecordSetsResult{}, translateErr(err, zoneName)
			}
			for _, rs := range page.Value {
				rtype := recordTypeFromAzureType(*rs.Type)
				if opt.TypeFilter != "" && rtype != opt.TypeFilter {
					continue
				}
				res.RecordSets = append(res.RecordSets, privDomainRRFromAzure(zoneName, rs, rtype))
			}
		}
		return res, nil
	}
	pager := b.pubRRSets.NewListByDNSZonePager(b.resourceGroup, zoneName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListRecordSetsResult{}, translateErr(err, zoneName)
		}
		for _, rs := range page.Value {
			rtype := recordTypeFromAzureType(*rs.Type)
			if opt.TypeFilter != "" && rtype != opt.TypeFilter {
				continue
			}
			res.RecordSets = append(res.RecordSets, pubDomainRRFromAzure(zoneName, rs, rtype))
		}
	}
	return res, nil
}

// isPrivate probes the zone's visibility by Get'ing it from both
// resource types. Returns true for private, false for public. Stateless.
func (b *Backend) isPrivate(ctx context.Context, zoneName string) (bool, error) {
	if _, err := b.pubZones.Get(ctx, b.resourceGroup, zoneName, nil); err == nil {
		return false, nil
	} else if !isNotFound(err) {
		return false, translateErr(err, zoneName)
	}
	if _, err := b.privZones.Get(ctx, b.resourceGroup, zoneName, nil); err == nil {
		return true, nil
	} else if !isNotFound(err) {
		return false, translateErr(err, zoneName)
	}
	return false, domain.NoSuchZone(zoneName)
}

func isNotFound(err error) bool {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		return re.StatusCode == 404 || re.ErrorCode == "NotFound" || re.ErrorCode == "ResourceNotFound"
	}
	return false
}

// ---------------- Translation: zone ----------------

func publicZoneFromAzure(z *armdns.Zone, description string) domain.Zone {
	out := domain.Zone{Visibility: domain.VisibilityPublic, Description: description}
	if z == nil {
		return out
	}
	if z.Name != nil {
		out.Name = canonicalize(*z.Name)
	}
	if z.Properties != nil {
		for _, ns := range z.Properties.NameServers {
			if ns != nil {
				out.NameServers = append(out.NameServers, *ns)
			}
		}
	}
	out.Tags = tagsFromAzure(z.Tags)
	return out
}

func privateZoneFromAzure(z *armprivatedns.PrivateZone, description string) domain.Zone {
	out := domain.Zone{Visibility: domain.VisibilityPrivate, Description: description}
	if z == nil {
		return out
	}
	if z.Name != nil {
		out.Name = canonicalize(*z.Name)
	}
	out.Tags = tagsFromAzurePriv(z.Tags)
	return out
}

func recordTypeFromAzureType(azureType string) domain.RecordType {
	// Azure returns the full ARM type, e.g. "Microsoft.Network/dnszones/A".
	// Last path segment is the record type.
	if i := strings.LastIndex(azureType, "/"); i >= 0 {
		return domain.RecordType(azureType[i+1:])
	}
	return domain.RecordType(azureType)
}

// ---------------- Translation: records (public) ----------------

func pubRRPropertiesFromDomain(rs domain.RecordSet) *armdns.RecordSetProperties {
	p := &armdns.RecordSetProperties{TTL: to.Ptr(int64(rs.TTL))}
	switch rs.Type {
	case domain.RecordTypeA:
		for _, v := range rs.Records {
			p.ARecords = append(p.ARecords, &armdns.ARecord{IPv4Address: to.Ptr(v)})
		}
	case domain.RecordTypeAAAA:
		for _, v := range rs.Records {
			p.AaaaRecords = append(p.AaaaRecords, &armdns.AaaaRecord{IPv6Address: to.Ptr(v)})
		}
	case domain.RecordTypeCNAME:
		if len(rs.Records) > 0 {
			p.CnameRecord = &armdns.CnameRecord{Cname: to.Ptr(strings.TrimSuffix(rs.Records[0], "."))}
		}
	case domain.RecordTypeMX:
		for _, v := range rs.Records {
			pref, exch, ok := parseMX(v)
			if !ok {
				continue
			}
			p.MxRecords = append(p.MxRecords, &armdns.MxRecord{Preference: to.Ptr(int32(pref)), Exchange: to.Ptr(exch)})
		}
	case domain.RecordTypeNS:
		for _, v := range rs.Records {
			p.NsRecords = append(p.NsRecords, &armdns.NsRecord{Nsdname: to.Ptr(strings.TrimSuffix(v, "."))})
		}
	case domain.RecordTypeSRV:
		for _, v := range rs.Records {
			pri, w, port, target, ok := parseSRV(v)
			if !ok {
				continue
			}
			p.SrvRecords = append(p.SrvRecords, &armdns.SrvRecord{
				Priority: to.Ptr(int32(pri)), Weight: to.Ptr(int32(w)),
				Port: to.Ptr(int32(port)), Target: to.Ptr(target),
			})
		}
	case domain.RecordTypeTXT:
		for _, v := range rs.Records {
			val := v
			p.TxtRecords = append(p.TxtRecords, &armdns.TxtRecord{Value: []*string{to.Ptr(val)}})
		}
	}
	return p
}

func pubDomainRRFromAzure(zone string, rs *armdns.RecordSet, rtype domain.RecordType) domain.RecordSet {
	out := domain.RecordSet{Type: rtype}
	if rs == nil {
		return out
	}
	if rs.Name != nil {
		out.Name = recordSetNameFromAzure(zone, *rs.Name)
	}
	if rs.Properties != nil && rs.Properties.TTL != nil {
		out.TTL = int(*rs.Properties.TTL)
	}
	if rs.Properties == nil {
		return out
	}
	switch rtype {
	case domain.RecordTypeA:
		for _, a := range rs.Properties.ARecords {
			if a != nil && a.IPv4Address != nil {
				out.Records = append(out.Records, *a.IPv4Address)
			}
		}
	case domain.RecordTypeAAAA:
		for _, a := range rs.Properties.AaaaRecords {
			if a != nil && a.IPv6Address != nil {
				out.Records = append(out.Records, *a.IPv6Address)
			}
		}
	case domain.RecordTypeCNAME:
		if rs.Properties.CnameRecord != nil && rs.Properties.CnameRecord.Cname != nil {
			out.Records = append(out.Records, canonicalize(*rs.Properties.CnameRecord.Cname))
		}
	case domain.RecordTypeMX:
		for _, m := range rs.Properties.MxRecords {
			if m != nil && m.Preference != nil && m.Exchange != nil {
				out.Records = append(out.Records, fmt.Sprintf("%d %s", *m.Preference, canonicalize(*m.Exchange)))
			}
		}
	case domain.RecordTypeNS:
		for _, n := range rs.Properties.NsRecords {
			if n != nil && n.Nsdname != nil {
				out.Records = append(out.Records, canonicalize(*n.Nsdname))
			}
		}
	case domain.RecordTypeSRV:
		for _, s := range rs.Properties.SrvRecords {
			if s != nil && s.Priority != nil && s.Weight != nil && s.Port != nil && s.Target != nil {
				out.Records = append(out.Records, fmt.Sprintf("%d %d %d %s", *s.Priority, *s.Weight, *s.Port, canonicalize(*s.Target)))
			}
		}
	case domain.RecordTypeTXT:
		for _, t := range rs.Properties.TxtRecords {
			if t == nil {
				continue
			}
			var v string
			for _, chunk := range t.Value {
				if chunk != nil {
					v += *chunk
				}
			}
			out.Records = append(out.Records, v)
		}
	case domain.RecordTypeSOA:
		// SOA values can be reconstructed from Properties.SoaRecord
		// if needed; the shim treats SOA as read-mostly so an empty
		// Records slice is acceptable for the foundational set.
	}
	return out
}

// ---------------- Translation: records (private) ----------------

func privRRPropertiesFromDomain(rs domain.RecordSet) *armprivatedns.RecordSetProperties {
	p := &armprivatedns.RecordSetProperties{TTL: to.Ptr(int64(rs.TTL))}
	switch rs.Type {
	case domain.RecordTypeA:
		for _, v := range rs.Records {
			p.ARecords = append(p.ARecords, &armprivatedns.ARecord{IPv4Address: to.Ptr(v)})
		}
	case domain.RecordTypeAAAA:
		for _, v := range rs.Records {
			p.AaaaRecords = append(p.AaaaRecords, &armprivatedns.AaaaRecord{IPv6Address: to.Ptr(v)})
		}
	case domain.RecordTypeCNAME:
		if len(rs.Records) > 0 {
			p.CnameRecord = &armprivatedns.CnameRecord{Cname: to.Ptr(strings.TrimSuffix(rs.Records[0], "."))}
		}
	case domain.RecordTypeMX:
		for _, v := range rs.Records {
			pref, exch, ok := parseMX(v)
			if !ok {
				continue
			}
			p.MxRecords = append(p.MxRecords, &armprivatedns.MxRecord{Preference: to.Ptr(int32(pref)), Exchange: to.Ptr(exch)})
		}
	case domain.RecordTypeSRV:
		for _, v := range rs.Records {
			pri, w, port, target, ok := parseSRV(v)
			if !ok {
				continue
			}
			p.SrvRecords = append(p.SrvRecords, &armprivatedns.SrvRecord{
				Priority: to.Ptr(int32(pri)), Weight: to.Ptr(int32(w)),
				Port: to.Ptr(int32(port)), Target: to.Ptr(target),
			})
		}
	case domain.RecordTypeTXT:
		for _, v := range rs.Records {
			val := v
			p.TxtRecords = append(p.TxtRecords, &armprivatedns.TxtRecord{Value: []*string{to.Ptr(val)}})
		}
	}
	return p
}

func privDomainRRFromAzure(zone string, rs *armprivatedns.RecordSet, rtype domain.RecordType) domain.RecordSet {
	out := domain.RecordSet{Type: rtype}
	if rs == nil {
		return out
	}
	if rs.Name != nil {
		out.Name = recordSetNameFromAzure(zone, *rs.Name)
	}
	if rs.Properties != nil && rs.Properties.TTL != nil {
		out.TTL = int(*rs.Properties.TTL)
	}
	if rs.Properties == nil {
		return out
	}
	switch rtype {
	case domain.RecordTypeA:
		for _, a := range rs.Properties.ARecords {
			if a != nil && a.IPv4Address != nil {
				out.Records = append(out.Records, *a.IPv4Address)
			}
		}
	case domain.RecordTypeAAAA:
		for _, a := range rs.Properties.AaaaRecords {
			if a != nil && a.IPv6Address != nil {
				out.Records = append(out.Records, *a.IPv6Address)
			}
		}
	case domain.RecordTypeCNAME:
		if rs.Properties.CnameRecord != nil && rs.Properties.CnameRecord.Cname != nil {
			out.Records = append(out.Records, canonicalize(*rs.Properties.CnameRecord.Cname))
		}
	case domain.RecordTypeMX:
		for _, m := range rs.Properties.MxRecords {
			if m != nil && m.Preference != nil && m.Exchange != nil {
				out.Records = append(out.Records, fmt.Sprintf("%d %s", *m.Preference, canonicalize(*m.Exchange)))
			}
		}
	case domain.RecordTypeSRV:
		for _, s := range rs.Properties.SrvRecords {
			if s != nil && s.Priority != nil && s.Weight != nil && s.Port != nil && s.Target != nil {
				out.Records = append(out.Records, fmt.Sprintf("%d %d %d %s", *s.Priority, *s.Weight, *s.Port, canonicalize(*s.Target)))
			}
		}
	case domain.RecordTypeTXT:
		for _, t := range rs.Properties.TxtRecords {
			if t == nil {
				continue
			}
			var v string
			for _, chunk := range t.Value {
				if chunk != nil {
					v += *chunk
				}
			}
			out.Records = append(out.Records, v)
		}
	}
	return out
}

// ---------------- helpers ----------------

func parseMX(s string) (pref int, exchange string, ok bool) {
	parts := strings.Fields(s)
	if len(parts) != 2 {
		return 0, "", false
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", false
	}
	return n, strings.TrimSuffix(parts[1], "."), true
}

func parseSRV(s string) (priority, weight, port int, target string, ok bool) {
	parts := strings.Fields(s)
	if len(parts) != 4 {
		return 0, 0, 0, "", false
	}
	pri, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, "", false
	}
	w, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, "", false
	}
	p, err := strconv.Atoi(parts[2])
	if err != nil {
		return 0, 0, 0, "", false
	}
	return pri, w, p, strings.TrimSuffix(parts[3], "."), true
}

func tagsToAzure(in map[string]string) map[string]*string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*string, len(in))
	for k, v := range in {
		val := v
		out[k] = &val
	}
	return out
}

func tagsFromAzure(in map[string]*string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func tagsFromAzurePriv(in map[string]*string) map[string]string { return tagsFromAzure(in) }

var _ = time.Time{} // keep time imported for future use

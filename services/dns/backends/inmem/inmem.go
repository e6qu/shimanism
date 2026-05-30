// Package inmem provides an in-memory DNS backend for tests.
// Mirrors the destination cloud's API contract: zones + record
// sets stored in maps, atomic create/replace/delete, NameServers
// fabricated as `ns-1`..`ns-4.<zone>` so callers can verify the
// shim returns what the destination cloud would expose.
package inmem

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

type Backend struct {
	mu    sync.RWMutex
	zones map[string]*zoneState // key: canonical zone name (lowercase, trailing dot)
}

type zoneState struct {
	zone       domain.Zone
	recordSets map[string]domain.RecordSet // key: "<name>|<type>"
}

func New() *Backend {
	return &Backend{zones: map[string]*zoneState{}}
}

func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

func rsKey(name string, rtype domain.RecordType) string {
	return canonicalize(name) + "|" + string(rtype)
}

func (b *Backend) CreateZone(ctx context.Context, name string, opt domain.CreateZoneOptions) (domain.Zone, error) {
	canonical := canonicalize(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.zones[canonical]; ok {
		return domain.Zone{}, domain.ZoneAlreadyExists(canonical)
	}
	now := time.Now().UTC()
	z := domain.Zone{
		Name:        canonical,
		Visibility:  opt.Visibility,
		Description: opt.Description,
		Tags:        copyTags(opt.Tags),
		CreatedAt:   now,
	}
	if opt.Visibility == domain.VisibilityPublic {
		z.NameServers = []string{
			"ns-1." + canonical,
			"ns-2." + canonical,
			"ns-3." + canonical,
			"ns-4." + canonical,
		}
	}
	st := &zoneState{
		zone:       z,
		recordSets: map[string]domain.RecordSet{},
	}
	// Seed cloud-managed SOA + NS records for public zones, mirroring
	// real-cloud behaviour where zones come pre-populated.
	if opt.Visibility == domain.VisibilityPublic {
		st.recordSets[rsKey(canonical, domain.RecordTypeSOA)] = domain.RecordSet{
			Name:    canonical,
			Type:    domain.RecordTypeSOA,
			TTL:     900,
			Records: []string{"ns-1." + canonical + " admin." + canonical + " 1 7200 900 1209600 86400"},
		}
		st.recordSets[rsKey(canonical, domain.RecordTypeNS)] = domain.RecordSet{
			Name:    canonical,
			Type:    domain.RecordTypeNS,
			TTL:     172800,
			Records: append([]string(nil), z.NameServers...),
		}
	}
	b.zones[canonical] = st
	return z, nil
}

func (b *Backend) GetZone(ctx context.Context, name string) (domain.Zone, error) {
	canonical := canonicalize(name)
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.zones[canonical]
	if !ok {
		return domain.Zone{}, domain.NoSuchZone(canonical)
	}
	return st.zone, nil
}

func (b *Backend) DeleteZone(ctx context.Context, name string, force bool) error {
	canonical := canonicalize(name)
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.zones[canonical]
	if !ok {
		return domain.NoSuchZone(canonical)
	}
	if !force {
		// Count user-managed record sets (everything except the
		// cloud-managed SOA + NS).
		for k := range st.recordSets {
			if !strings.HasSuffix(k, "|SOA") && !strings.HasSuffix(k, "|NS") {
				return domain.ZoneNotEmpty(canonical)
			}
		}
	}
	delete(b.zones, canonical)
	return nil
}

func (b *Backend) ListZones(ctx context.Context, opt domain.ListZonesOptions) (domain.ListZonesResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := domain.ListZonesResult{}
	for _, st := range b.zones {
		if opt.NamePrefix != "" && !strings.HasPrefix(st.zone.Name, canonicalize(opt.NamePrefix)) {
			continue
		}
		if opt.VisibilityFilter != domain.VisibilityUnknown && st.zone.Visibility != opt.VisibilityFilter {
			continue
		}
		out.Zones = append(out.Zones, st.zone)
	}
	return out, nil
}

func (b *Backend) PutRecordSet(ctx context.Context, zone string, rs domain.RecordSet) error {
	canonical := canonicalize(zone)
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.zones[canonical]
	if !ok {
		return domain.NoSuchZone(canonical)
	}
	rs.Name = canonicalize(rs.Name)
	st.recordSets[rsKey(rs.Name, rs.Type)] = rs
	return nil
}

func (b *Backend) GetRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) (domain.RecordSet, error) {
	canonicalZone := canonicalize(zone)
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.zones[canonicalZone]
	if !ok {
		return domain.RecordSet{}, domain.NoSuchZone(canonicalZone)
	}
	rs, ok := st.recordSets[rsKey(name, rtype)]
	if !ok {
		return domain.RecordSet{}, domain.NoSuchRecordSet(canonicalZone, canonicalize(name), rtype)
	}
	return rs, nil
}

func (b *Backend) DeleteRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) error {
	canonicalZone := canonicalize(zone)
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.zones[canonicalZone]
	if !ok {
		return domain.NoSuchZone(canonicalZone)
	}
	key := rsKey(name, rtype)
	if _, ok := st.recordSets[key]; !ok {
		return domain.NoSuchRecordSet(canonicalZone, canonicalize(name), rtype)
	}
	delete(st.recordSets, key)
	return nil
}

func (b *Backend) ListRecordSets(ctx context.Context, zone string, opt domain.ListRecordSetsOptions) (domain.ListRecordSetsResult, error) {
	canonicalZone := canonicalize(zone)
	b.mu.RLock()
	defer b.mu.RUnlock()
	st, ok := b.zones[canonicalZone]
	if !ok {
		return domain.ListRecordSetsResult{}, domain.NoSuchZone(canonicalZone)
	}
	out := domain.ListRecordSetsResult{}
	for _, rs := range st.recordSets {
		if opt.NameFilter != "" && rs.Name != canonicalize(opt.NameFilter) {
			continue
		}
		if opt.TypeFilter != "" && rs.Type != opt.TypeFilter {
			continue
		}
		out.RecordSets = append(out.RecordSets, rs)
	}
	return out, nil
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

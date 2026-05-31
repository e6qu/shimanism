// Package coredns is shimanism's K8s peer for DNS: a file-based
// backend that materializes zone state as RFC 1035 master files in a
// configured directory. A CoreDNS server with the `auto` plugin
// pointed at that directory loads each `<zone>.db` file as a zone
// and serves DNS queries directly. This is the canonical "fourth
// backend" for the DNS service per AGENTS.md.
//
// The directory layout is one file per zone:
//
//	<dir>/example.com.db        (zone for "example.com.")
//	<dir>/internal.example.db   (zone for "internal.example.")
//	...
//
// Each file is a self-contained RFC 1035 master file with `$ORIGIN`
// + `$TTL` directives, a SOA + apex NS record, and one entry per
// record set (one RR per Record value within a set). Concurrent
// mutations are serialised per zone via an in-memory mutex map;
// across replicas the filesystem (or a shared volume in K8s) is
// the source of truth — no shim-side cache, per the stateless-shim
// rule.
//
// In a K8s deployment, the directory is a ConfigMap or PVC mounted
// into the CoreDNS pod. The shim writes; CoreDNS reloads via inotify
// (the `auto` plugin's default).
package coredns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

// Backend implements domain.DNS by mutating RFC 1035 master files
// in a configured directory.
type Backend struct {
	dir string

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-zone mutex; protects file edits.
}

// New constructs a Backend rooted at the given directory. The
// directory must exist (in K8s, it's a mounted volume; in tests, a
// `t.TempDir()`).
func New(dir string) (*Backend, error) {
	if dir == "" {
		return nil, fmt.Errorf("coredns backend: dir required")
	}
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("coredns backend: stat %s: %w", dir, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("coredns backend: %s is not a directory", dir)
	}
	return &Backend{dir: dir, locks: map[string]*sync.Mutex{}}, nil
}

var _ domain.DNS = (*Backend)(nil)

// canonicalize forces lowercase + trailing dot.
func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

// zoneFilePath maps a canonical zone name to its `.db` file path.
// `example.com.` → `<dir>/example.com.db`.
func (b *Backend) zoneFilePath(canonical string) string {
	bare := strings.TrimSuffix(canonical, ".")
	return filepath.Join(b.dir, bare+".db")
}

// zoneLock returns the per-zone mutex; creates it lazily.
func (b *Backend) zoneLock(canonical string) *sync.Mutex {
	b.mu.Lock()
	defer b.mu.Unlock()
	if l, ok := b.locks[canonical]; ok {
		return l
	}
	l := &sync.Mutex{}
	b.locks[canonical] = l
	return l
}

// ---------------- Zones ----------------

func (b *Backend) CreateZone(ctx context.Context, name string, opt domain.CreateZoneOptions) (domain.Zone, error) {
	canonical := canonicalize(name)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	if _, err := os.Stat(path); err == nil {
		return domain.Zone{}, domain.ZoneAlreadyExists(canonical)
	} else if !os.IsNotExist(err) {
		return domain.Zone{}, fmt.Errorf("stat %s: %w", path, err)
	}
	now := uint32(time.Now().Unix())
	nameServers := []string{
		"ns1." + canonical,
		"ns2." + canonical,
	}
	rrs := []dns.RR{
		&dns.SOA{
			Hdr: dns.RR_Header{Name: canonical, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: 900},
			Ns:  nameServers[0], Mbox: "admin." + canonical,
			Serial: now, Refresh: 7200, Retry: 900, Expire: 1209600, Minttl: 86400,
		},
		&dns.NS{
			Hdr: dns.RR_Header{Name: canonical, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 172800},
			Ns:  nameServers[0],
		},
		&dns.NS{
			Hdr: dns.RR_Header{Name: canonical, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: 172800},
			Ns:  nameServers[1],
		},
	}
	if err := writeZoneFile(path, canonical, rrs); err != nil {
		return domain.Zone{}, err
	}
	z := domain.Zone{
		Name:        canonical,
		Visibility:  opt.Visibility,
		Description: opt.Description,
		Tags:        opt.Tags,
		NameServers: nameServers,
		CreatedAt:   time.Now().UTC(),
	}
	return z, nil
}

func (b *Backend) GetZone(ctx context.Context, name string) (domain.Zone, error) {
	canonical := canonicalize(name)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.Zone{}, domain.NoSuchZone(canonical)
		}
		return domain.Zone{}, err
	}
	z := domain.Zone{Name: canonical, Visibility: domain.VisibilityPublic}
	for _, rr := range rrs {
		if ns, ok := rr.(*dns.NS); ok && strings.EqualFold(ns.Hdr.Name, canonical) {
			z.NameServers = append(z.NameServers, strings.TrimSuffix(ns.Ns, "."))
		}
	}
	sort.Strings(z.NameServers)
	return z, nil
}

func (b *Backend) DeleteZone(ctx context.Context, name string, force bool) error {
	canonical := canonicalize(name)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.NoSuchZone(canonical)
		}
		return err
	}
	if !force {
		for _, rr := range rrs {
			t := rr.Header().Rrtype
			if t == dns.TypeSOA {
				continue
			}
			if t == dns.TypeNS && strings.EqualFold(rr.Header().Name, canonical) {
				continue
			}
			return domain.ZoneNotEmpty(canonical)
		}
	}
	return os.Remove(path)
}

func (b *Backend) ListZones(ctx context.Context, opt domain.ListZonesOptions) (domain.ListZonesResult, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		return domain.ListZonesResult{}, fmt.Errorf("read dir %s: %w", b.dir, err)
	}
	res := domain.ListZonesResult{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		bare := strings.TrimSuffix(e.Name(), ".db")
		canonical := canonicalize(bare)
		if opt.NamePrefix != "" && !strings.HasPrefix(canonical, canonicalize(opt.NamePrefix)) {
			continue
		}
		z, err := b.GetZone(ctx, canonical)
		if err != nil {
			continue
		}
		if opt.VisibilityFilter != domain.VisibilityUnknown && z.Visibility != opt.VisibilityFilter {
			continue
		}
		res.Zones = append(res.Zones, z)
	}
	return res, nil
}

// ---------------- Record sets ----------------

func (b *Backend) PutRecordSet(ctx context.Context, zone string, rs domain.RecordSet) error {
	canonical := canonicalize(zone)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.NoSuchZone(canonical)
		}
		return err
	}
	rsName := canonicalize(rs.Name)
	rrType, ok := rrTypeOf(rs.Type)
	if !ok {
		return domain.InvalidArgument("unsupported record type: " + string(rs.Type))
	}
	// Drop existing records at (name, type), append new ones.
	filtered := rrs[:0]
	for _, rr := range rrs {
		if strings.EqualFold(rr.Header().Name, rsName) && rr.Header().Rrtype == rrType {
			continue
		}
		filtered = append(filtered, rr)
	}
	rrs = filtered
	for _, raw := range rs.Records {
		rr, err := buildRR(rsName, rs.Type, uint32(rs.TTL), raw)
		if err != nil {
			return err
		}
		rrs = append(rrs, rr)
	}
	return writeZoneFile(path, canonical, rrs)
}

func (b *Backend) GetRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) (domain.RecordSet, error) {
	canonical := canonicalize(zone)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.RecordSet{}, domain.NoSuchZone(canonical)
		}
		return domain.RecordSet{}, err
	}
	rsName := canonicalize(name)
	rrType, ok := rrTypeOf(rtype)
	if !ok {
		return domain.RecordSet{}, domain.InvalidArgument("unsupported record type: " + string(rtype))
	}
	rs := domain.RecordSet{Name: rsName, Type: rtype}
	for _, rr := range rrs {
		if !strings.EqualFold(rr.Header().Name, rsName) || rr.Header().Rrtype != rrType {
			continue
		}
		if rs.TTL == 0 {
			rs.TTL = int(rr.Header().Ttl)
		}
		val, err := rrValue(rr)
		if err != nil {
			return domain.RecordSet{}, err
		}
		rs.Records = append(rs.Records, val)
	}
	if len(rs.Records) == 0 {
		return domain.RecordSet{}, domain.NoSuchRecordSet(canonical, rsName, rtype)
	}
	return rs, nil
}

func (b *Backend) DeleteRecordSet(ctx context.Context, zone, name string, rtype domain.RecordType) error {
	canonical := canonicalize(zone)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.NoSuchZone(canonical)
		}
		return err
	}
	rsName := canonicalize(name)
	rrType, ok := rrTypeOf(rtype)
	if !ok {
		return domain.InvalidArgument("unsupported record type: " + string(rtype))
	}
	out := rrs[:0]
	removed := false
	for _, rr := range rrs {
		if strings.EqualFold(rr.Header().Name, rsName) && rr.Header().Rrtype == rrType {
			removed = true
			continue
		}
		out = append(out, rr)
	}
	if !removed {
		return domain.NoSuchRecordSet(canonical, rsName, rtype)
	}
	return writeZoneFile(path, canonical, out)
}

func (b *Backend) ListRecordSets(ctx context.Context, zone string, opt domain.ListRecordSetsOptions) (domain.ListRecordSetsResult, error) {
	canonical := canonicalize(zone)
	path := b.zoneFilePath(canonical)
	zl := b.zoneLock(canonical)
	zl.Lock()
	defer zl.Unlock()
	rrs, err := readZoneFile(path, canonical)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.ListRecordSetsResult{}, domain.NoSuchZone(canonical)
		}
		return domain.ListRecordSetsResult{}, err
	}
	// Group by (name, type).
	type key struct {
		name string
		typ  uint16
	}
	groups := map[key]*domain.RecordSet{}
	order := []key{}
	for _, rr := range rrs {
		k := key{name: strings.ToLower(rr.Header().Name), typ: rr.Header().Rrtype}
		dt, ok := domainTypeOf(rr.Header().Rrtype)
		if !ok {
			continue // skip unsupported types
		}
		if opt.TypeFilter != "" && dt != opt.TypeFilter {
			continue
		}
		if opt.NameFilter != "" && k.name != strings.ToLower(canonicalize(opt.NameFilter)) {
			continue
		}
		rs, exists := groups[k]
		if !exists {
			rs = &domain.RecordSet{Name: k.name, Type: dt, TTL: int(rr.Header().Ttl)}
			groups[k] = rs
			order = append(order, k)
		}
		val, err := rrValue(rr)
		if err != nil {
			return domain.ListRecordSetsResult{}, err
		}
		rs.Records = append(rs.Records, val)
	}
	res := domain.ListRecordSetsResult{}
	for _, k := range order {
		res.RecordSets = append(res.RecordSets, *groups[k])
	}
	return res, nil
}

// ---------------- file I/O ----------------

// readZoneFile parses the master file at path. Returns os.ErrNotExist
// when missing so callers can map it to NoSuchZone.
func readZoneFile(path, origin string) ([]dns.RR, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zp := dns.NewZoneParser(f, origin, path)
	zp.SetDefaultTTL(3600)
	var rrs []dns.RR
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		rrs = append(rrs, rr)
	}
	if err := zp.Err(); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rrs, nil
}

// writeZoneFile renders rrs to the master-file format. SOA is always
// first; the rest sorted by (name, type) so diffs are stable.
func writeZoneFile(path, origin string, rrs []dns.RR) error {
	sort.SliceStable(rrs, func(i, j int) bool {
		a, b := rrs[i], rrs[j]
		// SOA always first.
		if a.Header().Rrtype == dns.TypeSOA && b.Header().Rrtype != dns.TypeSOA {
			return true
		}
		if b.Header().Rrtype == dns.TypeSOA && a.Header().Rrtype != dns.TypeSOA {
			return false
		}
		if a.Header().Name != b.Header().Name {
			return a.Header().Name < b.Header().Name
		}
		return a.Header().Rrtype < b.Header().Rrtype
	})
	var buf strings.Builder
	fmt.Fprintf(&buf, "$ORIGIN %s\n$TTL 3600\n", origin)
	for _, rr := range rrs {
		buf.WriteString(rr.String())
		buf.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(buf.String()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---------------- type maps ----------------

func rrTypeOf(t domain.RecordType) (uint16, bool) {
	switch t {
	case domain.RecordTypeA:
		return dns.TypeA, true
	case domain.RecordTypeAAAA:
		return dns.TypeAAAA, true
	case domain.RecordTypeCNAME:
		return dns.TypeCNAME, true
	case domain.RecordTypeMX:
		return dns.TypeMX, true
	case domain.RecordTypeNS:
		return dns.TypeNS, true
	case domain.RecordTypeSOA:
		return dns.TypeSOA, true
	case domain.RecordTypeSRV:
		return dns.TypeSRV, true
	case domain.RecordTypeTXT:
		return dns.TypeTXT, true
	}
	return 0, false
}

func domainTypeOf(rrType uint16) (domain.RecordType, bool) {
	switch rrType {
	case dns.TypeA:
		return domain.RecordTypeA, true
	case dns.TypeAAAA:
		return domain.RecordTypeAAAA, true
	case dns.TypeCNAME:
		return domain.RecordTypeCNAME, true
	case dns.TypeMX:
		return domain.RecordTypeMX, true
	case dns.TypeNS:
		return domain.RecordTypeNS, true
	case dns.TypeSOA:
		return domain.RecordTypeSOA, true
	case dns.TypeSRV:
		return domain.RecordTypeSRV, true
	case dns.TypeTXT:
		return domain.RecordTypeTXT, true
	}
	return "", false
}

// buildRR constructs a miekg/dns RR from a domain-level record value.
// The domain encoding mirrors what the other backends use (see
// docs/normalizations.md): plain IPv4/IPv6 for A/AAAA, FQDN with
// trailing dot for CNAME/NS, "<pref> <exch>" for MX, "<pri> <wt>
// <port> <target>" for SRV, raw string for TXT.
func buildRR(name string, dt domain.RecordType, ttl uint32, value string) (dns.RR, error) {
	hdr := dns.RR_Header{Name: name, Class: dns.ClassINET, Ttl: ttl}
	switch dt {
	case domain.RecordTypeA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return nil, domain.InvalidArgument("invalid A record value: " + value)
		}
		hdr.Rrtype = dns.TypeA
		return &dns.A{Hdr: hdr, A: ip.To4()}, nil
	case domain.RecordTypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To16() == nil {
			return nil, domain.InvalidArgument("invalid AAAA record value: " + value)
		}
		hdr.Rrtype = dns.TypeAAAA
		return &dns.AAAA{Hdr: hdr, AAAA: ip.To16()}, nil
	case domain.RecordTypeCNAME:
		hdr.Rrtype = dns.TypeCNAME
		return &dns.CNAME{Hdr: hdr, Target: canonicalize(value)}, nil
	case domain.RecordTypeMX:
		parts := strings.Fields(value)
		if len(parts) != 2 {
			return nil, domain.InvalidArgument("MX record must be '<preference> <exchange>': " + value)
		}
		pref, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, domain.InvalidArgument("MX preference not numeric: " + parts[0])
		}
		hdr.Rrtype = dns.TypeMX
		return &dns.MX{Hdr: hdr, Preference: uint16(pref), Mx: canonicalize(parts[1])}, nil
	case domain.RecordTypeNS:
		hdr.Rrtype = dns.TypeNS
		return &dns.NS{Hdr: hdr, Ns: canonicalize(value)}, nil
	case domain.RecordTypeSRV:
		parts := strings.Fields(value)
		if len(parts) != 4 {
			return nil, domain.InvalidArgument("SRV record must be '<pri> <wt> <port> <target>': " + value)
		}
		pri, err1 := strconv.Atoi(parts[0])
		wt, err2 := strconv.Atoi(parts[1])
		port, err3 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, domain.InvalidArgument("SRV record numeric fields invalid: " + value)
		}
		hdr.Rrtype = dns.TypeSRV
		return &dns.SRV{Hdr: hdr, Priority: uint16(pri), Weight: uint16(wt), Port: uint16(port), Target: canonicalize(parts[3])}, nil
	case domain.RecordTypeTXT:
		hdr.Rrtype = dns.TypeTXT
		return &dns.TXT{Hdr: hdr, Txt: []string{value}}, nil
	}
	return nil, errors.New("unsupported record type for build: " + string(dt))
}

// rrValue extracts the domain-level value string from a miekg/dns RR.
func rrValue(rr dns.RR) (string, error) {
	switch v := rr.(type) {
	case *dns.A:
		return v.A.String(), nil
	case *dns.AAAA:
		return v.AAAA.String(), nil
	case *dns.CNAME:
		return canonicalize(v.Target), nil
	case *dns.MX:
		return fmt.Sprintf("%d %s", v.Preference, canonicalize(v.Mx)), nil
	case *dns.NS:
		return canonicalize(v.Ns), nil
	case *dns.SRV:
		return fmt.Sprintf("%d %d %d %s", v.Priority, v.Weight, v.Port, canonicalize(v.Target)), nil
	case *dns.TXT:
		return strings.Join(v.Txt, ""), nil
	case *dns.SOA:
		return fmt.Sprintf("%s %s %d %d %d %d %d",
			canonicalize(v.Ns), canonicalize(v.Mbox),
			v.Serial, v.Refresh, v.Retry, v.Expire, v.Minttl), nil
	}
	return "", errors.New("unsupported RR type: " + dns.TypeToString[rr.Header().Rrtype])
}

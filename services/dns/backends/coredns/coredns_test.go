package coredns

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

func newTestBackend(t *testing.T) *Backend {
	t.Helper()
	dir := t.TempDir()
	b, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return b
}

func TestZoneLifecycle(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)

	z, err := b.CreateZone(ctx, "example.com.", domain.CreateZoneOptions{})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if z.Name != "example.com." {
		t.Errorf("Name = %q, want example.com.", z.Name)
	}
	if len(z.NameServers) == 0 {
		t.Errorf("NameServers empty")
	}

	// Re-create fails with ZoneAlreadyExists.
	if _, err := b.CreateZone(ctx, "example.com.", domain.CreateZoneOptions{}); !domain.IsKind(err, domain.KindZoneAlreadyExists) {
		t.Errorf("re-create: want ZoneAlreadyExists, got %v", err)
	}

	got, err := b.GetZone(ctx, "example.com.")
	if err != nil {
		t.Fatalf("GetZone: %v", err)
	}
	if got.Name != "example.com." {
		t.Errorf("Get Name = %q", got.Name)
	}

	list, err := b.ListZones(ctx, domain.ListZonesOptions{})
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(list.Zones) != 1 {
		t.Errorf("ListZones len = %d, want 1", len(list.Zones))
	}

	if err := b.DeleteZone(ctx, "example.com.", false); err != nil {
		t.Errorf("DeleteZone (empty): %v", err)
	}
	if _, err := b.GetZone(ctx, "example.com."); !domain.IsKind(err, domain.KindNoSuchZone) {
		t.Errorf("GetZone after delete: want NoSuchZone, got %v", err)
	}
}

func TestRecordSetCRUD(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	if _, err := b.CreateZone(ctx, "rs.test.", domain.CreateZoneOptions{}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	// A record.
	if err := b.PutRecordSet(ctx, "rs.test.", domain.RecordSet{
		Name: "www.rs.test.", Type: domain.RecordTypeA, TTL: 300,
		Records: []string{"1.2.3.4", "5.6.7.8"},
	}); err != nil {
		t.Fatalf("PutRecordSet A: %v", err)
	}
	got, err := b.GetRecordSet(ctx, "rs.test.", "www.rs.test.", domain.RecordTypeA)
	if err != nil {
		t.Fatalf("GetRecordSet A: %v", err)
	}
	if got.TTL != 300 {
		t.Errorf("TTL = %d, want 300", got.TTL)
	}
	if len(got.Records) != 2 {
		t.Errorf("Records len = %d, want 2", len(got.Records))
	}

	// CNAME.
	if err := b.PutRecordSet(ctx, "rs.test.", domain.RecordSet{
		Name: "alias.rs.test.", Type: domain.RecordTypeCNAME, TTL: 600,
		Records: []string{"target.rs.test."},
	}); err != nil {
		t.Fatalf("PutRecordSet CNAME: %v", err)
	}
	got, err = b.GetRecordSet(ctx, "rs.test.", "alias.rs.test.", domain.RecordTypeCNAME)
	if err != nil {
		t.Fatalf("GetRecordSet CNAME: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0] != "target.rs.test." {
		t.Errorf("CNAME round-trip: got %v, want [target.rs.test.]", got.Records)
	}

	// MX.
	if err := b.PutRecordSet(ctx, "rs.test.", domain.RecordSet{
		Name: "rs.test.", Type: domain.RecordTypeMX, TTL: 3600,
		Records: []string{"10 mail.rs.test.", "20 mail2.rs.test."},
	}); err != nil {
		t.Fatalf("PutRecordSet MX: %v", err)
	}
	got, err = b.GetRecordSet(ctx, "rs.test.", "rs.test.", domain.RecordTypeMX)
	if err != nil {
		t.Fatalf("GetRecordSet MX: %v", err)
	}
	if len(got.Records) != 2 {
		t.Errorf("MX records len = %d, want 2", len(got.Records))
	}

	// TXT — value round-trip with quoting.
	if err := b.PutRecordSet(ctx, "rs.test.", domain.RecordSet{
		Name: "_dmarc.rs.test.", Type: domain.RecordTypeTXT, TTL: 300,
		Records: []string{"v=DMARC1; p=none"},
	}); err != nil {
		t.Fatalf("PutRecordSet TXT: %v", err)
	}
	got, err = b.GetRecordSet(ctx, "rs.test.", "_dmarc.rs.test.", domain.RecordTypeTXT)
	if err != nil {
		t.Fatalf("GetRecordSet TXT: %v", err)
	}
	if len(got.Records) != 1 || got.Records[0] != "v=DMARC1; p=none" {
		t.Errorf("TXT round-trip mismatch: %v", got.Records)
	}

	// ListRecordSets returns everything we wrote + SOA + NS.
	list, err := b.ListRecordSets(ctx, "rs.test.", domain.ListRecordSetsOptions{})
	if err != nil {
		t.Fatalf("ListRecordSets: %v", err)
	}
	// 4 user-managed + SOA + NS(apex) = 6.
	if len(list.RecordSets) < 4 {
		t.Errorf("ListRecordSets len = %d, want >=4 user records", len(list.RecordSets))
	}

	// DeleteRecordSet.
	if err := b.DeleteRecordSet(ctx, "rs.test.", "alias.rs.test.", domain.RecordTypeCNAME); err != nil {
		t.Fatalf("DeleteRecordSet: %v", err)
	}
	if _, err := b.GetRecordSet(ctx, "rs.test.", "alias.rs.test.", domain.RecordTypeCNAME); !domain.IsKind(err, domain.KindNoSuchRecordSet) {
		t.Errorf("after delete: want NoSuchRecordSet, got %v", err)
	}
}

func TestForceDeleteZone(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	if _, err := b.CreateZone(ctx, "fd.test.", domain.CreateZoneOptions{}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if err := b.PutRecordSet(ctx, "fd.test.", domain.RecordSet{
		Name: "x.fd.test.", Type: domain.RecordTypeA, TTL: 300,
		Records: []string{"10.0.0.1"},
	}); err != nil {
		t.Fatalf("PutRecordSet: %v", err)
	}

	// force=false fails (user-managed record present).
	if err := b.DeleteZone(ctx, "fd.test.", false); !domain.IsKind(err, domain.KindZoneNotEmpty) {
		t.Errorf("force=false: want ZoneNotEmpty, got %v", err)
	}
	// force=true succeeds.
	if err := b.DeleteZone(ctx, "fd.test.", true); err != nil {
		t.Errorf("force=true: %v", err)
	}
}

// TestZoneFileIsValidMasterFile verifies the written file parses
// back via miekg/dns (which CoreDNS uses too). Ensures the auto
// plugin can load our output.
func TestZoneFileIsValidMasterFile(t *testing.T) {
	ctx := context.Background()
	b := newTestBackend(t)
	if _, err := b.CreateZone(ctx, "rt.test.", domain.CreateZoneOptions{}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if err := b.PutRecordSet(ctx, "rt.test.", domain.RecordSet{
		Name: "api.rt.test.", Type: domain.RecordTypeA, TTL: 300,
		Records: []string{"1.2.3.4"},
	}); err != nil {
		t.Fatalf("PutRecordSet: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(b.dir, "rt.test.db"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(body) == 0 {
		t.Fatalf("zone file empty")
	}
	// Re-parse via the same backend's reader.
	rrs, err := readZoneFile(filepath.Join(b.dir, "rt.test.db"), "rt.test.")
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(rrs) < 3 {
		t.Errorf("re-parse RR count = %d, want >=3 (SOA + NS + A)", len(rrs))
	}
}

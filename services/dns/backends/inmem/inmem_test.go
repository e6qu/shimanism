package inmem_test

import (
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/dns/domain"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func TestInmemDNS_ZoneLifecycle(t *testing.T) {
	t.Parallel()
	b := inmem.New()
	ctx := context.Background()

	z, err := b.CreateZone(ctx, "example.com", domain.CreateZoneOptions{
		Visibility:  domain.VisibilityPublic,
		Description: "test zone",
	})
	if err != nil {
		t.Fatalf("CreateZone: %v", err)
	}
	if z.Name != "example.com." {
		t.Errorf("Name = %q, want example.com.", z.Name)
	}
	if z.Visibility != domain.VisibilityPublic {
		t.Errorf("Visibility = %v, want VisibilityPublic", z.Visibility)
	}
	if len(z.NameServers) != 4 {
		t.Errorf("NameServers = %v, want 4 entries", z.NameServers)
	}

	// Re-create errors with ZoneAlreadyExists.
	if _, err := b.CreateZone(ctx, "example.com", domain.CreateZoneOptions{Visibility: domain.VisibilityPublic}); !domain.IsKind(err, domain.KindZoneAlreadyExists) {
		t.Errorf("re-CreateZone err = %v, want KindZoneAlreadyExists", err)
	}

	// SOA + NS pre-seeded for public zones.
	if soa, err := b.GetRecordSet(ctx, "example.com", "example.com", domain.RecordTypeSOA); err != nil || len(soa.Records) == 0 {
		t.Errorf("GetRecordSet SOA: err=%v records=%v", err, soa.Records)
	}

	// DeleteZone(force=false) succeeds — only SOA + NS present.
	if err := b.DeleteZone(ctx, "example.com", false); err != nil {
		t.Fatalf("DeleteZone(force=false): %v", err)
	}
	if _, err := b.GetZone(ctx, "example.com"); !domain.IsKind(err, domain.KindNoSuchZone) {
		t.Errorf("GetZone after delete: err=%v, want KindNoSuchZone", err)
	}
}

func TestInmemDNS_RecordSetCRUD(t *testing.T) {
	t.Parallel()
	b := inmem.New()
	ctx := context.Background()

	if _, err := b.CreateZone(ctx, "example.com", domain.CreateZoneOptions{Visibility: domain.VisibilityPublic}); err != nil {
		t.Fatalf("CreateZone: %v", err)
	}

	rs := domain.RecordSet{
		Name:    "www.example.com.",
		Type:    domain.RecordTypeA,
		TTL:     300,
		Records: []string{"192.0.2.10", "192.0.2.11"},
	}
	if err := b.PutRecordSet(ctx, "example.com", rs); err != nil {
		t.Fatalf("PutRecordSet: %v", err)
	}

	got, err := b.GetRecordSet(ctx, "example.com", "www.example.com.", domain.RecordTypeA)
	if err != nil {
		t.Fatalf("GetRecordSet: %v", err)
	}
	if got.TTL != 300 || len(got.Records) != 2 {
		t.Errorf("GetRecordSet returned %+v", got)
	}

	// DeleteZone(force=false) now fails — user-managed record.
	if err := b.DeleteZone(ctx, "example.com", false); !domain.IsKind(err, domain.KindZoneNotEmpty) {
		t.Errorf("DeleteZone(force=false) with user record: err=%v, want KindZoneNotEmpty", err)
	}

	// Force-delete succeeds.
	if err := b.DeleteZone(ctx, "example.com", true); err != nil {
		t.Errorf("DeleteZone(force=true): %v", err)
	}
}

func TestInmemDNS_PrivateZone(t *testing.T) {
	t.Parallel()
	b := inmem.New()
	ctx := context.Background()

	z, err := b.CreateZone(ctx, "internal.example.com", domain.CreateZoneOptions{
		Visibility:  domain.VisibilityPrivate,
		PrivateVPCs: []string{"vpc-test-1"},
	})
	if err != nil {
		t.Fatalf("CreateZone (private): %v", err)
	}
	if z.Visibility != domain.VisibilityPrivate {
		t.Errorf("Visibility = %v, want VisibilityPrivate", z.Visibility)
	}
	// Private zones don't get public name servers.
	if len(z.NameServers) != 0 {
		t.Errorf("private zone NameServers = %v, want empty", z.NameServers)
	}
}

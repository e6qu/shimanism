// Conformance: GCP Cloud DNS-shaped frontend exercised by the
// official `google.golang.org/api/dns/v1` SDK. The SDK is pointed at
// the shim via WithEndpoint and signs requests with a JWT the shim's
// GCP bearer verifier trusts.
package conformance_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
	dnsraw "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func newGCPCloudDNSService(t *testing.T, endpoint string) *dnsraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://dns.googleapis.com/",
		15*time.Minute,
	)
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(tokenSource),
	)
	if err != nil {
		t.Fatalf("new Cloud DNS service: %v", err)
	}
	return svc
}

func TestGCPSDK_CloudDNS_ZoneLifecycle(t *testing.T) {
	srv := harness.StartDNSServerGCP(t, inmem.New())
	svc := newGCPCloudDNSService(t, srv.URL)
	ctx := context.Background()
	const project = "shim-conformance"

	// Create a public managed zone.
	created, err := svc.ManagedZones.Create(project, &dnsraw.ManagedZone{
		Name:        "example-com",
		DnsName:     "example.com.",
		Description: "conformance fixture",
		Visibility:  "public",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}
	if created.DnsName != "example.com." {
		t.Errorf("DnsName = %q, want example.com.", created.DnsName)
	}
	if created.Visibility != "public" {
		t.Errorf("Visibility = %q", created.Visibility)
	}
	if len(created.NameServers) == 0 {
		t.Errorf("NameServers empty")
	}

	// Get it back by name.
	got, err := svc.ManagedZones.Get(project, "example-com").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get: %v", err)
	}
	if got.DnsName != "example.com." {
		t.Errorf("after Get, DnsName = %q", got.DnsName)
	}

	// List zones.
	list, err := svc.ManagedZones.List(project).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.List: %v", err)
	}
	if len(list.ManagedZones) != 1 || list.ManagedZones[0].DnsName != "example.com." {
		t.Errorf("ManagedZones.List unexpected: %+v", list.ManagedZones)
	}

	// Add an A record via Changes.Create (the atomic batch path Terraform uses).
	_, err = svc.Changes.Create(project, "example-com", &dnsraw.Change{
		Additions: []*dnsraw.ResourceRecordSet{{
			Name:    "www.example.com.",
			Type:    "A",
			Ttl:     300,
			Rrdatas: []string{"1.2.3.4"},
		}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.Create UPSERT A: %v", err)
	}

	listRR, err := svc.ResourceRecordSets.List(project, "example-com").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ResourceRecordSets.List: %v", err)
	}
	foundA := false
	for _, rs := range listRR.Rrsets {
		if rs.Type == "A" && rs.Name == "www.example.com." {
			foundA = true
			if len(rs.Rrdatas) != 1 || rs.Rrdatas[0] != "1.2.3.4" {
				t.Errorf("A rrdatas: %+v", rs.Rrdatas)
			}
		}
	}
	if !foundA {
		t.Errorf("A record not present after UPSERT: %+v", listRR.Rrsets)
	}

	// Delete the A record so the zone can be deleted.
	_, err = svc.Changes.Create(project, "example-com", &dnsraw.Change{
		Deletions: []*dnsraw.ResourceRecordSet{{
			Name:    "www.example.com.",
			Type:    "A",
			Ttl:     300,
			Rrdatas: []string{"1.2.3.4"},
		}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.Create DELETE A: %v", err)
	}

	if err := svc.ManagedZones.Delete(project, "example-com").Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Delete: %v", err)
	}
}

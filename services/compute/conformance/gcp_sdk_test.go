// Conformance: GCP Compute Engine v1-shaped frontend exercised by the
// official google.golang.org/api/compute/v1 REST SDK. The SDK is
// pointed at the shim via the service endpoint option.
//
// This lane covers the Phase 16.B networking operations:
// networks, firewalls (security groups), subnetworks, and addresses.
package conformance_test

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

// newGCPComputeClient builds a Compute v1 client pointed at the
// shim's endpoint. Signs requests with a HS256 JWT that the shim's
// GCP bearer verifier trusts (same test-key as all other GCP lanes).
func newGCPComputeClient(t *testing.T, endpoint string) *computeraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build GCP compute client: %v", err)
	}
	return svc
}

const gcpProject = "shim-test-project"

func TestGCPSDK_Compute_NetworkLifecycle(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeClient(t, srv.URL)
	ctx := context.Background()

	// Insert network
	op, err := svc.Networks.Insert(gcpProject, &computeraw.Network{
		Name: "test-network",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Networks.Insert: %v", err)
	}
	if op.Status != "DONE" {
		t.Errorf("operation status = %q, want DONE", op.Status)
	}

	// List networks — verify presence
	list, err := svc.Networks.List(gcpProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Networks.List: %v", err)
	}
	found := false
	for _, n := range list.Items {
		if n.Name == "test-network" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Networks.List did not find 'test-network'")
	}

	// Get network
	net, err := svc.Networks.Get(gcpProject, "test-network").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Networks.Get: %v", err)
	}
	if net.Name != "test-network" {
		t.Errorf("Networks.Get name = %q, want test-network", net.Name)
	}

	// Delete network
	delOp, err := svc.Networks.Delete(gcpProject, "test-network").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Networks.Delete: %v", err)
	}
	if delOp.Status != "DONE" {
		t.Errorf("delete operation status = %q, want DONE", delOp.Status)
	}

	// List after delete — should be empty
	list2, err := svc.Networks.List(gcpProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Networks.List after delete: %v", err)
	}
	for _, n := range list2.Items {
		if n.Name == "test-network" {
			t.Errorf("Network 'test-network' still present after delete")
		}
	}
}

func TestGCPSDK_Compute_FirewallLifecycle(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeClient(t, srv.URL)
	ctx := context.Background()

	// Create parent network first
	if _, err := svc.Networks.Insert(gcpProject, &computeraw.Network{Name: "fw-net"}).Context(ctx).Do(); err != nil {
		t.Fatalf("Networks.Insert(fw-net): %v", err)
	}
	t.Cleanup(func() {
		svc.Networks.Delete(gcpProject, "fw-net").Context(ctx).Do()
	})

	// Insert firewall
	_, err := svc.Firewalls.Insert(gcpProject, &computeraw.Firewall{
		Name:         "allow-http",
		Network:      srv.URL + "/compute/v1/projects/" + gcpProject + "/global/networks/fw-net",
		Direction:    "INGRESS",
		SourceRanges: []string{"0.0.0.0/0"},
		Allowed: []*computeraw.FirewallAllowed{{
			IPProtocol: "tcp",
			Ports:      []string{"80"},
		}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Firewalls.Insert: %v", err)
	}
	t.Cleanup(func() {
		svc.Firewalls.Delete(gcpProject, "allow-http").Context(ctx).Do()
	})

	// List firewalls
	list, err := svc.Firewalls.List(gcpProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Firewalls.List: %v", err)
	}
	found := false
	for _, fw := range list.Items {
		if fw.Name == "allow-http" {
			found = true
			if len(fw.Allowed) == 0 {
				t.Errorf("Firewalls.List: allow-http has no allowed rules")
			}
			break
		}
	}
	if !found {
		t.Errorf("Firewalls.List: allow-http not found")
	}

	// Get firewall
	fw, err := svc.Firewalls.Get(gcpProject, "allow-http").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Firewalls.Get: %v", err)
	}
	if fw.Name != "allow-http" {
		t.Errorf("Firewalls.Get name = %q, want allow-http", fw.Name)
	}

	// Delete firewall
	if _, err := svc.Firewalls.Delete(gcpProject, "allow-http").Context(ctx).Do(); err != nil {
		t.Fatalf("Firewalls.Delete: %v", err)
	}
}

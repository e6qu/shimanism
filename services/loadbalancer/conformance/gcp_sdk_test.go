// Conformance: GCP Compute Engine LB-shaped frontend exercised by the
// official google.golang.org/api/compute/v1 SDK via forwarding rules
// and backend services.
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
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

const gcpLBProject = "shim-lb-project"

// newGCPComputeLBClient builds a Compute v1 client pointed at the
// LB shim endpoint.
func newGCPComputeLBClient(t *testing.T, endpoint string) *computeraw.Service {
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

func TestGCPSDK_LB_ForwardingRuleLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerGCP(t, inmem.New())
	svc := newGCPComputeLBClient(t, srv.URL)
	ctx := context.Background()

	// Insert forwarding rule (= create LB)
	op, err := svc.ForwardingRules.Insert(gcpLBProject, "us-central1", &computeraw.ForwardingRule{
		Name:       "my-fr",
		IPProtocol: "TCP",
		PortRange:  "80",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Insert: %v", err)
	}
	if op.Status != "DONE" {
		t.Errorf("operation status = %q, want DONE", op.Status)
	}

	// List forwarding rules
	list, err := svc.ForwardingRules.List(gcpLBProject, "us-central1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.List: %v", err)
	}
	found := false
	for _, fr := range list.Items {
		if fr.Name == "my-fr" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ForwardingRules.List: my-fr not found")
	}

	// Get forwarding rule
	fr, err := svc.ForwardingRules.Get(gcpLBProject, "us-central1", "my-fr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Get: %v", err)
	}
	if fr.Name != "my-fr" {
		t.Errorf("ForwardingRules.Get name = %q", fr.Name)
	}

	// Delete forwarding rule
	delOp, err := svc.ForwardingRules.Delete(gcpLBProject, "us-central1", "my-fr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ForwardingRules.Delete: %v", err)
	}
	if delOp.Status != "DONE" {
		t.Errorf("delete operation status = %q", delOp.Status)
	}
}

func TestGCPSDK_LB_BackendServiceLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerGCP(t, inmem.New())
	svc := newGCPComputeLBClient(t, srv.URL)
	ctx := context.Background()

	// Insert backend service (= create TargetGroup)
	op, err := svc.RegionBackendServices.Insert(gcpLBProject, "us-central1", &computeraw.BackendService{
		Name:     "my-bs",
		Protocol: "TCP",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.Insert: %v", err)
	}
	if op.Status != "DONE" {
		t.Errorf("operation status = %q", op.Status)
	}

	// Get backend service
	bs, err := svc.RegionBackendServices.Get(gcpLBProject, "us-central1", "my-bs").Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.Get: %v", err)
	}
	if bs.Name != "my-bs" {
		t.Errorf("RegionBackendServices.Get name = %q", bs.Name)
	}

	// List backend services
	list, err := svc.RegionBackendServices.List(gcpLBProject, "us-central1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("RegionBackendServices.List: %v", err)
	}
	found := false
	for _, b := range list.Items {
		if b.Name == "my-bs" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("RegionBackendServices.List: my-bs not found")
	}

	// Delete backend service
	if _, err := svc.RegionBackendServices.Delete(gcpLBProject, "us-central1", "my-bs").Context(ctx).Do(); err != nil {
		t.Fatalf("RegionBackendServices.Delete: %v", err)
	}
}

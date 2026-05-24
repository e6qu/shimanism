// Sockerless lane for the apigateway service. See doc/SOCKERLESS_VALIDATION.md.
//
// Phase 14.B: sockerless#177 added GCP API Gateway. Closing this
// lane is the path to closing BUG-8 without real-cloud Track A
// (the alternative was a real GCP project + Workload Identity setup).
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	gcpbackend "github.com/e6qu/shimanism/services/apigateway/backends/gcp"
)

// TestSockerless_GCP_APIGateway_CRUD drives the shim's GCP API
// Gateway backend's CreateGateway → DescribeGateway → ListGateways
// → DeleteGateway end-to-end against a running sockerless GCP sim.
func TestSockerless_GCP_APIGateway_CRUD(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := apigwapi.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("apigw client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpbackend.New(svc, gcpbackend.Config{
		ProjectID: project,
		Region:    "global",
	})

	name := "shim-sk-gw-" + sockHex8apigw()
	// Shim's GCP CreateGateway creates the Api only; the Gateway
	// resource itself materializes once DeployGateway runs (which
	// also creates the ApiConfig).
	if _, err := backend.CreateGateway(ctx, name, domain.CreateGatewayOptions{
		Routes: []domain.Route{
			{Path: "/v1/hello", Method: "GET", Backend: "http://example.invalid/hello"},
		},
	}); err != nil {
		t.Fatalf("CreateGateway: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteGateway(ctx, name) })

	gw, err := backend.DescribeGateway(ctx, name)
	if err != nil {
		t.Fatalf("DescribeGateway: %v", err)
	}
	if gw.Name != name {
		t.Errorf("DescribeGateway.Name = %q, want %q", gw.Name, name)
	}

	list, err := backend.ListGateways(ctx, domain.ListGatewaysOptions{})
	if err != nil {
		t.Fatalf("ListGateways: %v", err)
	}
	found := false
	for _, g := range list.Gateways {
		if g.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListGateways did not contain %q", name)
	}
}

func sockHex8apigw() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

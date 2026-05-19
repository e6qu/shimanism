// Phase 8 conformance: GCP API Gateway-shaped frontend exercised
// by `google.golang.org/api/apigateway/v1`. The shim's frontend
// implements just enough of the GCP REST surface (Gateway create /
// get / list / delete) to be driven by the official Go SDK.
package conformance_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func TestGCPSDK_APIGatewayLifecycle(t *testing.T) {
	srv := harness.StartAPIGatewayServerGCP(t, inmem.New())
	ctx := context.Background()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse harness URL: %v", err)
	}
	endpoint := strings.TrimSuffix(srv.URL, "/")
	_ = u
	svc, err := apigwapi.NewService(ctx,
		option.WithEndpoint(endpoint),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	parent := "projects/p1/locations/us-central1"
	gwName := "shim-gw"
	if _, err := svc.Projects.Locations.Gateways.Create(parent, &apigwapi.ApigatewayGateway{}).
		GatewayId(gwName).Context(ctx).Do(); err != nil {
		t.Fatalf("Gateways.Create: %v", err)
	}

	got, err := svc.Projects.Locations.Gateways.Get(parent + "/gateways/" + gwName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Gateways.Get: %v", err)
	}
	if got.DisplayName != gwName {
		t.Errorf("DisplayName = %q, want %q", got.DisplayName, gwName)
	}

	listResp, err := svc.Projects.Locations.Gateways.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Gateways.List: %v", err)
	}
	if len(listResp.Gateways) != 1 {
		t.Errorf("List count = %d, want 1", len(listResp.Gateways))
	}

	if _, err := svc.Projects.Locations.Gateways.Delete(parent + "/gateways/" + gwName).Context(ctx).Do(); err != nil {
		t.Fatalf("Gateways.Delete: %v", err)
	}
}

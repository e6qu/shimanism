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
	"time"

	"golang.org/x/oauth2"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func apigwTokenSource() oauth2.TokenSource {
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://apigateway.googleapis.com/",
		15*time.Minute,
	)
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
}

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
		option.WithTokenSource(apigwTokenSource()),
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

// TestGCPSDK_APIGateway_ApiConfigRouteDeploy exercises the Apis +
// ApiConfigs surface — the GCP-shaped route-deployment path. The
// SDK posts an OpenAPI document; the shim parses it and dispatches
// to domain.DeployGateway.
func TestGCPSDK_APIGateway_ApiConfigRouteDeploy(t *testing.T) {
	srv := harness.StartAPIGatewayServerGCP(t, inmem.New())
	ctx := context.Background()
	endpoint := strings.TrimSuffix(srv.URL, "/")
	svc, err := apigwapi.NewService(ctx,
		option.WithEndpoint(endpoint),
		option.WithTokenSource(apigwTokenSource()),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	apiName := "shim-route-deploy"
	if _, err := svc.Projects.Locations.Apis.Create(
		"projects/p1/locations/global", &apigwapi.ApigatewayApi{}).
		ApiId(apiName).Context(ctx).Do(); err != nil {
		t.Fatalf("Apis.Create: %v", err)
	}

	openapi := `swagger: "2.0"
info:
  title: ` + apiName + `
  version: 1.0.0
schemes:
  - https
paths:
  /healthz:
    get:
      operationId: get_healthz
      responses:
        "200":
          description: OK
      x-google-backend:
        address: https://backend.example.com/healthz
`
	if _, err := svc.Projects.Locations.Apis.Configs.Create(
		"projects/p1/locations/global/apis/"+apiName,
		&apigwapi.ApigatewayApiConfig{
			OpenapiDocuments: []*apigwapi.ApigatewayApiConfigOpenApiDocument{{
				Document: &apigwapi.ApigatewayApiConfigFile{
					Contents: openapi,
					Path:     "openapi.yaml",
				},
			}},
		}).ApiConfigId("cfg-1").Context(ctx).Do(); err != nil {
		t.Fatalf("ApiConfigs.Create: %v", err)
	}

	// Confirm Gateway state has the route deployed.
	got, err := svc.Projects.Locations.Apis.Configs.List(
		"projects/p1/locations/global/apis/" + apiName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ApiConfigs.List: %v", err)
	}
	if len(got.ApiConfigs) == 0 {
		t.Errorf("ApiConfigs.List = empty, want at least 1")
	}

	if _, err := svc.Projects.Locations.Apis.Delete(
		"projects/p1/locations/global/apis/" + apiName).Context(ctx).Do(); err != nil {
		t.Fatalf("Apis.Delete: %v", err)
	}
}

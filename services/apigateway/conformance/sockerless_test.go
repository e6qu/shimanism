// Sockerless lane for the apigateway service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	"github.com/e6qu/shimanism/internal/harness"
	gcpbackend "github.com/e6qu/shimanism/services/apigateway/backends/gcp"
)

// TestSockerless_GCP_APIGateway_CRUD drives the shim's GCP API
// Gateway backend's full lifecycle against a running sockerless
// GCP sim: CreateGateway (with routes — triggers DeployGateway →
// Api + ApiConfig + Gateway resources) → DescribeGateway →
// ListGateways → DeleteGateway.
//
// Set SOCKERLESS_GCP_ENDPOINT (e.g. localhost:14567) to opt in.
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

// TestSockerless_AWSAPIGatewayFrontendToGCPBackend_CRUD drives the
// full through-shim E2E path for API Gateway:
// aws-sdk-go-v2 API Gateway v2 client → AWS-shaped shim frontend →
// GCP API Gateway backend → sockerless GCP simulator.
func TestSockerless_AWSAPIGatewayFrontendToGCPBackend_CRUD(t *testing.T) {
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
	srv := harness.StartAPIGatewayServerAWS(t, backend)

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	name := "shim-sk-xgw-" + sockHex8apigw()
	apiOut, err := client.CreateApi(ctx, &apigatewayv2.CreateApiInput{
		Name:         awsapi.String(name),
		ProtocolType: apigwtypes.ProtocolTypeHttp,
	})
	if err != nil {
		t.Fatalf("CreateApi through shim: %v", err)
	}
	apiID := awsapi.ToString(apiOut.ApiId)
	t.Cleanup(func() {
		_, _ = client.DeleteApi(ctx, &apigatewayv2.DeleteApiInput{ApiId: awsapi.String(apiID)})
	})

	intOut, err := client.CreateIntegration(ctx, &apigatewayv2.CreateIntegrationInput{
		ApiId:             awsapi.String(apiID),
		IntegrationType:   apigwtypes.IntegrationTypeHttpProxy,
		IntegrationUri:    awsapi.String("https://example.invalid/through-shim"),
		IntegrationMethod: awsapi.String("GET"),
	})
	if err != nil {
		t.Fatalf("CreateIntegration through shim: %v", err)
	}
	if _, err := client.CreateRoute(ctx, &apigatewayv2.CreateRouteInput{
		ApiId:    awsapi.String(apiID),
		RouteKey: awsapi.String("GET /through-shim"),
		Target:   awsapi.String("integrations/" + awsapi.ToString(intOut.IntegrationId)),
	}); err != nil {
		t.Fatalf("CreateRoute through shim: %v", err)
	}
	if _, err := client.CreateDeployment(ctx, &apigatewayv2.CreateDeploymentInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		t.Fatalf("CreateDeployment through shim: %v", err)
	}

	got, err := client.GetApi(ctx, &apigatewayv2.GetApiInput{ApiId: awsapi.String(apiID)})
	if err != nil {
		t.Fatalf("GetApi through shim: %v", err)
	}
	if awsapi.ToString(got.ApiId) != apiID {
		t.Errorf("GetApi.ApiId = %q, want %q", awsapi.ToString(got.ApiId), apiID)
	}
}

func sockHex8apigw() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

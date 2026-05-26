// Sockerless lane for the apigateway service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	"github.com/e6qu/shimanism/internal/harness"
	azureapim "github.com/e6qu/shimanism/services/apigateway/backends/azure"
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

// TestSockerless_Azure_APIGateway_APIM_CRUD exercises the shim's Azure
// APIM apigateway backend against sockerless's Microsoft.ApiManagement
// ARM control plane: CreateGateway → DescribeGateway → ListGateways →
// DeleteGateway.
//
// The parent APIM Service (`Microsoft.ApiManagement/service/<name>`) is
// pre-created via a direct ARM PUT to the sim before invoking the
// backend. Real users of the shim set up the APIM Service via
// Terraform / ARM template; the shim itself only manages Apis within
// it. This mirrors that order of operations.
func TestSockerless_Azure_APIGateway_APIM_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	subscription := sockerlessAzureSubscriptionAPIGW()
	resourceGroup := "shim-sk-rg"
	serviceName := "shim-sk-apim-" + sockHex8apigw()
	opts := sockerlessARMClientOptionsAPIGW(port)
	if err := apimPreCreateService(opts, subscription, resourceGroup, serviceName); err != nil {
		t.Fatalf("pre-create APIM service: %v", err)
	}

	backend, err := azureapim.New(azureapim.Config{
		SubscriptionID: subscription,
		ResourceGroup:  resourceGroup,
		ServiceName:    serviceName,
		Credential:     sockerlessNoOpCredentialAPIGW{},
		ClientOptions:  opts,
	})
	if err != nil {
		t.Fatalf("azureapim.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-gw-" + sockHex8apigw()
	gw, err := backend.CreateGateway(ctx, name, domain.CreateGatewayOptions{})
	if err != nil {
		t.Fatalf("CreateGateway: %v", err)
	}
	if gw.Name != name {
		t.Errorf("CreateGateway.Name = %q, want %q", gw.Name, name)
	}
	t.Cleanup(func() { _ = backend.DeleteGateway(ctx, name) })

	got, err := backend.DescribeGateway(ctx, name)
	if err != nil {
		t.Fatalf("DescribeGateway: %v", err)
	}
	if got.Name != name {
		t.Errorf("DescribeGateway.Name = %q, want %q", got.Name, name)
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

func sockerlessARMClientOptionsAPIGW(port string) *arm.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://localhost:" + port + "/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: "https://management.azure.com",
						Endpoint: "https://localhost:" + port,
					},
				},
			},
			Transport: &http.Client{Transport: transport},
		},
	}
}

func sockerlessAzureSubscriptionAPIGW() string {
	if s := os.Getenv("SOCKERLESS_AZURE_SUBSCRIPTION"); s != "" {
		return s
	}
	return "00000000-0000-0000-0000-000000000000"
}

type sockerlessNoOpCredentialAPIGW struct{}

var sockerlessFarFutureAPIGW = time.Date(2099, time.December, 31, 23, 59, 59, 0, time.UTC)

func (sockerlessNoOpCredentialAPIGW) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "sockerless-test", ExpiresOn: sockerlessFarFutureAPIGW}, nil
}

// apimPreCreateService PUTs an APIM Service via the ServiceClient so
// that subsequent API CRUD inside the shim's backend has a parent to
// attach to. Real users of the shim pre-create the APIM Service via
// Terraform / ARM template; the shim itself only manages Apis within
// it. Mirrors that order of operations in the test.
func apimPreCreateService(opts *arm.ClientOptions, subscription, resourceGroup, serviceName string) error {
	factory, err := armapimanagement.NewClientFactory(subscription, sockerlessNoOpCredentialAPIGW{}, opts)
	if err != nil {
		return err
	}
	poller, err := factory.NewServiceClient().BeginCreateOrUpdate(
		context.Background(), resourceGroup, serviceName,
		armapimanagement.ServiceResource{
			Location: to.Ptr("eastus"),
			Properties: &armapimanagement.ServiceProperties{
				PublisherEmail: to.Ptr("shim@example.invalid"),
				PublisherName:  to.Ptr("shim"),
			},
			SKU: &armapimanagement.ServiceSKUProperties{
				Name:     to.Ptr(armapimanagement.SKUTypeDeveloper),
				Capacity: to.Ptr[int32](1),
			},
		}, nil)
	if err != nil {
		return err
	}
	_, err = poller.PollUntilDone(context.Background(), nil)
	return err
}

// Phase 8 conformance: AWS API Gateway v2-shaped frontend
// exercised by `aws-sdk-go-v2/service/apigatewayv2`. Verifies the
// CreateApi → CreateIntegration → CreateRoute → CreateDeployment
// → GetApi → DeleteApi lifecycle via the restJson1 wire protocol.
package conformance_test

import (
	"context"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func TestAWSSDK_APIGatewayLifecycle(t *testing.T) {
	srv := harness.StartAPIGatewayServerAWS(t, inmem.New())
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
	)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	client := apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	// 1. Create the Api.
	cOut, err := client.CreateApi(ctx, &apigatewayv2.CreateApiInput{
		Name:         awsapi.String("shim-api"),
		ProtocolType: apigwtypes.ProtocolTypeHttp,
	})
	if err != nil {
		t.Fatalf("CreateApi: %v", err)
	}
	apiID := awsapi.ToString(cOut.ApiId)

	// 2. Create an integration pointing at a backend.
	iOut, err := client.CreateIntegration(ctx, &apigatewayv2.CreateIntegrationInput{
		ApiId:             awsapi.String(apiID),
		IntegrationType:   apigwtypes.IntegrationTypeHttpProxy,
		IntegrationUri:    awsapi.String("https://example.com/api"),
		IntegrationMethod: awsapi.String("GET"),
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	intID := awsapi.ToString(iOut.IntegrationId)

	// 3. Create a route bound to the integration.
	if _, err := client.CreateRoute(ctx, &apigatewayv2.CreateRouteInput{
		ApiId:    awsapi.String(apiID),
		RouteKey: awsapi.String("GET /hello"),
		Target:   awsapi.String("integrations/" + intID),
	}); err != nil {
		t.Fatalf("CreateRoute: %v", err)
	}

	// 4. Deploy.
	if _, err := client.CreateDeployment(ctx, &apigatewayv2.CreateDeploymentInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	// 5. Describe — verify the routing table was published.
	if _, err := client.GetApi(ctx, &apigatewayv2.GetApiInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		t.Fatalf("GetApi: %v", err)
	}
	rOut, err := client.GetRoutes(ctx, &apigatewayv2.GetRoutesInput{
		ApiId: awsapi.String(apiID),
	})
	if err != nil {
		t.Fatalf("GetRoutes: %v", err)
	}
	if len(rOut.Items) != 1 {
		t.Errorf("GetRoutes count = %d, want 1", len(rOut.Items))
	}

	// 6. Tear down.
	if _, err := client.DeleteApi(ctx, &apigatewayv2.DeleteApiInput{
		ApiId: awsapi.String(apiID),
	}); err != nil {
		t.Errorf("DeleteApi: %v", err)
	}
}

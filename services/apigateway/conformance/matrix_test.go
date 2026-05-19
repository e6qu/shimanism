// Cross-frontend × cross-backend matrix conformance for
// apigateway: each frontend in {AWS APIGW v2, GCP API Gateway,
// Azure APIM} is driven against each backend in {inmem, envoy,
// aws, gcp, azure}. Backends decide their own skip semantics so
// per-PR CI lights one backend per job.
package conformance_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awscfg "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/apigatewayv2"
	apigwtypes "github.com/aws/aws-sdk-go-v2/service/apigatewayv2/types"
	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/conformance"
)

func TestAPIGatewayMatrix_AWSFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartAPIGatewayServerAWS(t, backend)
			ctx := context.Background()
			cfg, err := awscfg.LoadDefaultConfig(ctx,
				awscfg.WithRegion("us-east-1"),
				awscfg.WithCredentialsProvider(awsapi.AnonymousCredentials{}),
			)
			if err != nil {
				t.Fatalf("load aws config: %v", err)
			}
			client := apigatewayv2.NewFromConfig(cfg, func(o *apigatewayv2.Options) {
				o.BaseEndpoint = awsapi.String(srv.URL)
			})
			name := randomGatewayName("shim-aws")
			t.Cleanup(func() {
				_, _ = client.DeleteApi(ctx, &apigatewayv2.DeleteApiInput{ApiId: awsapi.String(name)})
			})
			if _, err := client.CreateApi(ctx, &apigatewayv2.CreateApiInput{
				Name:         awsapi.String(name),
				ProtocolType: apigwtypes.ProtocolTypeHttp,
			}); err != nil {
				t.Fatalf("CreateApi: %v", err)
			}
			if _, err := client.GetApi(ctx, &apigatewayv2.GetApiInput{ApiId: awsapi.String(name)}); err != nil {
				t.Fatalf("GetApi: %v", err)
			}
		})
	}
}

func TestAPIGatewayMatrix_GCPFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartAPIGatewayServerGCP(t, backend)
			ctx := context.Background()
			svc, err := apigwapi.NewService(ctx,
				option.WithEndpoint(strings.TrimSuffix(srv.URL, "/")),
				option.WithoutAuthentication(),
			)
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			parent := "projects/shim-matrix/locations/us-central1"
			name := randomGatewayName("shim-gcp")
			t.Cleanup(func() {
				_, _ = svc.Projects.Locations.Gateways.Delete(parent + "/gateways/" + name).Context(ctx).Do()
			})
			if _, err := svc.Projects.Locations.Gateways.Create(parent, &apigwapi.ApigatewayGateway{}).
				GatewayId(name).Context(ctx).Do(); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := svc.Projects.Locations.Gateways.Get(parent + "/gateways/" + name).Context(ctx).Do(); err != nil {
				t.Fatalf("Get: %v", err)
			}
		})
	}
}

func TestAPIGatewayMatrix_AzureFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartAPIGatewayServerAzure(t, backend)
			ctx := context.Background()
			shimCloud := cloud.Configuration{
				ActiveDirectoryAuthorityHost: srv.URL,
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {Endpoint: srv.URL, Audience: "https://management.azure.com"},
				},
			}
			opts := &arm.ClientOptions{
				ClientOptions: azcore.ClientOptions{
					Cloud:                           shimCloud,
					Transport:                       &http.Client{},
					InsecureAllowCredentialWithHTTP: true,
				},
			}
			factory, err := armapimanagement.NewClientFactory("00000000-0000-0000-0000-000000000000", fakeAzureCred{}, opts)
			if err != nil {
				t.Fatalf("NewClientFactory: %v", err)
			}
			api := factory.NewAPIClient()
			name := randomGatewayName("shim-az")
			display := name
			path := "/" + name
			poller, err := api.BeginCreateOrUpdate(ctx, "rg", "svc", name, armapimanagement.APICreateOrUpdateParameter{
				Properties: &armapimanagement.APICreateOrUpdateProperties{
					DisplayName: &display,
					Path:        &path,
					Protocols:   []*armapimanagement.Protocol{ptrAzure(armapimanagement.ProtocolHTTPS)},
				},
			}, nil)
			if err != nil {
				t.Fatalf("BeginCreateOrUpdate: %v", err)
			}
			if _, err := poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: 10 * time.Millisecond}); err != nil {
				t.Fatalf("PollUntilDone: %v", err)
			}
			if _, err := api.Get(ctx, "rg", "svc", name, nil); err != nil {
				t.Fatalf("Get: %v", err)
			}
		})
	}
}

// randomGatewayName returns a unique-per-run identifier safe across
// all four frontends. AWS / GCP / Azure all accept lowercase alnum
// + dashes.
func randomGatewayName(prefix string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return prefix + "-fallback"
	}
	return fmt.Sprintf("%s-%s", strings.ToLower(prefix), hex.EncodeToString(buf[:]))
}

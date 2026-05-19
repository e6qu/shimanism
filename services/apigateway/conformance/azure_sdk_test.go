// Phase 8 conformance: Azure API Management ARM frontend exercised
// by the official `armapimanagement` SDK. The SDK is pointed at
// the shim via cloud.Configuration override; the fake credential
// satisfies the bearer-token contract the shim doesn't validate.
package conformance_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/apimanagement/armapimanagement/v3"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

type fakeAzureCred struct{}

func (fakeAzureCred) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "shim-conformance-fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestAzureSDK_APIGatewayLifecycle(t *testing.T) {
	srv := harness.StartAPIGatewayServerAzure(t, inmem.New())
	ctx := context.Background()

	shimCloud := cloud.Configuration{
		ActiveDirectoryAuthorityHost: srv.URL,
		Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Endpoint: srv.URL,
				Audience: "https://management.azure.com",
			},
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

	name := "shim-api"
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

	got, err := api.Get(ctx, "rg", "svc", name, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Properties == nil || got.Properties.DisplayName == nil || *got.Properties.DisplayName != name {
		t.Errorf("Get DisplayName mismatch: %+v", got.Properties)
	}

	pager := api.NewListByServicePager("rg", "svc", nil)
	listCount := 0
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		listCount += len(page.Value)
	}
	if listCount != 1 {
		t.Errorf("list count = %d, want 1", listCount)
	}
}

func ptrAzure[T any](v T) *T { return &v }

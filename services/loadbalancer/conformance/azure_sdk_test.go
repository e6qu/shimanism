// Conformance: Azure Network LB-shaped frontend exercised by the
// official armnetwork/v6 SDK. Pointed at the shim via ARM endpoint
// override; auth uses the test JWT the shim's azurebearer verifier
// trusts.
package conformance_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

const (
	azureLBSubscription  = "00000000-0000-0000-0000-000000000001"
	azureLBResourceGroup = "shim-lb-rg"
)

// azureLBCredential signs JWTs for the ARM audience.
type azureLBCredential struct{}

func (azureLBCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

func newAzureLBClientOptions(endpoint string) *arm.ClientOptions {
	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}
	return &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: httpClient,
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: "https://shim.test/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: "https://management.azure.com/",
						Endpoint: endpoint,
					},
				},
			},
		},
	}
}

func TestAzureSDK_LB_LoadBalancerLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerAzure(t, inmem.New())
	ctx := context.Background()
	opts := newAzureLBClientOptions(srv.URL)
	cred := azureLBCredential{}

	lbClient, err := armnetwork.NewLoadBalancersClient(azureLBSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new LoadBalancers client: %v", err)
	}

	// CreateOrUpdate
	poller, err := lbClient.BeginCreateOrUpdate(ctx, azureLBResourceGroup, "my-lb",
		armnetwork.LoadBalancer{
			Location:   to.Ptr("eastus"),
			SKU:        &armnetwork.LoadBalancerSKU{Name: to.Ptr(armnetwork.LoadBalancerSKUNameStandard)},
			Properties: &armnetwork.LoadBalancerPropertiesFormat{},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	if *created.Name != "my-lb" {
		t.Errorf("LB name = %q, want my-lb", *created.Name)
	}

	// Get
	got, err := lbClient.Get(ctx, azureLBResourceGroup, "my-lb", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got.Name != "my-lb" {
		t.Errorf("Get name = %q", *got.Name)
	}

	// List
	pager := lbClient.NewListPager(azureLBResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListPager: %v", err)
		}
		for _, v := range page.Value {
			if *v.Name == "my-lb" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("List: my-lb not found")
	}

	// Delete
	delPoller, err := lbClient.BeginDelete(ctx, azureLBResourceGroup, "my-lb", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PollUntilDone: %v", err)
	}
}

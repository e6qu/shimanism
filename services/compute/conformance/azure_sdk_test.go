// Conformance: Azure Network-shaped frontend exercised by the official
// armnetwork/v6 SDK. The SDK is pointed at the shim via ClientOptions
// with a custom cloud configuration; auth signs with a JWT the shim's
// azurebearer verifier trusts.
//
// This lane covers Phase 16.B networking operations: VNet lifecycle,
// Subnet lifecycle, NSG lifecycle, and PublicIPAddress lifecycle.
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
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const (
	azureSubscriptionID = "00000000-0000-0000-0000-000000000001"
	azureResourceGroup  = "shim-rg"
)

// azureNetCredential issues JWTs the shim's azurebearer verifier accepts.
type azureNetCredential struct{}

func (azureNetCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

// newAzureNetClientOptions builds ARM client options pointing at the
// shim endpoint with the self-signed TLS cert accepted.
func newAzureNetClientOptions(endpoint string) *arm.ClientOptions {
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
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

func TestAzureSDK_Network_VNetLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzure(t, inmem.New())
	ctx := context.Background()
	opts := newAzureNetClientOptions(srv.URL)
	cred := azureNetCredential{}

	client, err := armnetwork.NewVirtualNetworksClient(azureSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new VirtualNetworks client: %v", err)
	}

	// CreateOrUpdate VNet
	poller, err := client.BeginCreateOrUpdate(ctx, azureResourceGroup, "test-vnet",
		armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
	if *created.Name != "test-vnet" {
		t.Errorf("Name = %q, want test-vnet", *created.Name)
	}

	// Get VNet
	got, err := client.Get(ctx, azureResourceGroup, "test-vnet", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got.Name != "test-vnet" {
		t.Errorf("Get Name = %q, want test-vnet", *got.Name)
	}

	// List VNets
	pager := client.NewListPager(azureResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListPager: %v", err)
		}
		for _, v := range page.Value {
			if *v.Name == "test-vnet" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("List did not find test-vnet")
	}

	// Delete VNet
	delPoller, err := client.BeginDelete(ctx, azureResourceGroup, "test-vnet", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}

	// Verify gone
	_, err = client.Get(ctx, azureResourceGroup, "test-vnet", nil)
	if err == nil {
		t.Errorf("Get after delete should error, got nil")
	}
}

func TestAzureSDK_Network_SubnetLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzure(t, inmem.New())
	ctx := context.Background()
	opts := newAzureNetClientOptions(srv.URL)
	cred := azureNetCredential{}

	vnetClient, err := armnetwork.NewVirtualNetworksClient(azureSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new VirtualNetworks client: %v", err)
	}
	subnetClient, err := armnetwork.NewSubnetsClient(azureSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new Subnets client: %v", err)
	}

	// Create parent VNet
	vnetPoller, err := vnetClient.BeginCreateOrUpdate(ctx, azureResourceGroup, "subnet-vnet",
		armnetwork.VirtualNetwork{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{to.Ptr("10.0.0.0/16")},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("create VNet: %v", err)
	}
	if _, err := vnetPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("create VNet poll: %v", err)
	}
	t.Cleanup(func() {
		p, _ := vnetClient.BeginDelete(ctx, azureResourceGroup, "subnet-vnet", nil)
		if p != nil {
			p.PollUntilDone(ctx, nil)
		}
	})

	// Create Subnet
	subPoller, err := subnetClient.BeginCreateOrUpdate(ctx, azureResourceGroup, "subnet-vnet", "web-subnet",
		armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: to.Ptr("10.0.1.0/24"),
			},
		}, nil)
	if err != nil {
		t.Fatalf("create Subnet: %v", err)
	}
	subCreated, err := subPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create Subnet poll: %v", err)
	}
	if *subCreated.Name != "web-subnet" {
		t.Errorf("Subnet Name = %q, want web-subnet", *subCreated.Name)
	}

	// Get Subnet
	gotSub, err := subnetClient.Get(ctx, azureResourceGroup, "subnet-vnet", "web-subnet", nil)
	if err != nil {
		t.Fatalf("Get subnet: %v", err)
	}
	if *gotSub.Name != "web-subnet" {
		t.Errorf("Subnet Get Name = %q", *gotSub.Name)
	}

	// Delete Subnet
	delPoller, err := subnetClient.BeginDelete(ctx, azureResourceGroup, "subnet-vnet", "web-subnet", nil)
	if err != nil {
		t.Fatalf("delete Subnet: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete Subnet poll: %v", err)
	}
}

func TestAzureSDK_Network_NSGLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzure(t, inmem.New())
	ctx := context.Background()
	opts := newAzureNetClientOptions(srv.URL)
	cred := azureNetCredential{}

	nsgClient, err := armnetwork.NewSecurityGroupsClient(azureSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new SecurityGroups client: %v", err)
	}

	// Create NSG
	nsgPoller, err := nsgClient.BeginCreateOrUpdate(ctx, azureResourceGroup, "web-nsg",
		armnetwork.SecurityGroup{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.SecurityGroupPropertiesFormat{
				SecurityRules: []*armnetwork.SecurityRule{
					{
						Name: to.Ptr("allow-http"),
						Properties: &armnetwork.SecurityRulePropertiesFormat{
							Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
							Direction:                to.Ptr(armnetwork.SecurityRuleDirectionInbound),
							Priority:                 to.Ptr(int32(100)),
							Protocol:                 to.Ptr(armnetwork.SecurityRuleProtocolTCP),
							SourcePortRange:          to.Ptr("*"),
							DestinationPortRange:     to.Ptr("80"),
							SourceAddressPrefix:      to.Ptr("0.0.0.0/0"),
							DestinationAddressPrefix: to.Ptr("*"),
						},
					},
				},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate NSG: %v", err)
	}
	created, err := nsgPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create NSG poll: %v", err)
	}
	if *created.Name != "web-nsg" {
		t.Errorf("NSG Name = %q, want web-nsg", *created.Name)
	}
	t.Cleanup(func() {
		p, _ := nsgClient.BeginDelete(ctx, azureResourceGroup, "web-nsg", nil)
		if p != nil {
			p.PollUntilDone(ctx, nil)
		}
	})

	// Get NSG
	got, err := nsgClient.Get(ctx, azureResourceGroup, "web-nsg", nil)
	if err != nil {
		t.Fatalf("Get NSG: %v", err)
	}
	if *got.Name != "web-nsg" {
		t.Errorf("NSG Get Name = %q", *got.Name)
	}
	if len(got.Properties.SecurityRules) != 1 {
		t.Errorf("SecurityRules count = %d, want 1", len(got.Properties.SecurityRules))
	}

	// List NSGs
	pager := nsgClient.NewListPager(azureResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListPager NSG: %v", err)
		}
		for _, v := range page.Value {
			if *v.Name == "web-nsg" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("List NSGs did not find web-nsg")
	}

	// Delete NSG
	delPoller, err := nsgClient.BeginDelete(ctx, azureResourceGroup, "web-nsg", nil)
	if err != nil {
		t.Fatalf("BeginDelete NSG: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete NSG poll: %v", err)
	}
}

func TestAzureSDK_Network_PublicIPLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzure(t, inmem.New())
	ctx := context.Background()
	opts := newAzureNetClientOptions(srv.URL)
	cred := azureNetCredential{}

	pipClient, err := armnetwork.NewPublicIPAddressesClient(azureSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new PublicIPAddresses client: %v", err)
	}

	// Create PublicIP
	pipPoller, err := pipClient.BeginCreateOrUpdate(ctx, azureResourceGroup, "my-pip",
		armnetwork.PublicIPAddress{
			Location: to.Ptr("eastus"),
			Properties: &armnetwork.PublicIPAddressPropertiesFormat{
				PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate PublicIP: %v", err)
	}
	created, err := pipPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("create PublicIP poll: %v", err)
	}
	if *created.Name != "my-pip" {
		t.Errorf("PublicIP Name = %q, want my-pip", *created.Name)
	}
	t.Cleanup(func() {
		p, _ := pipClient.BeginDelete(ctx, azureResourceGroup, "my-pip", nil)
		if p != nil {
			p.PollUntilDone(ctx, nil)
		}
	})

	// Get PublicIP
	got, err := pipClient.Get(ctx, azureResourceGroup, "my-pip", nil)
	if err != nil {
		t.Fatalf("Get PublicIP: %v", err)
	}
	if *got.Name != "my-pip" {
		t.Errorf("PublicIP Get Name = %q", *got.Name)
	}
	if got.Properties == nil || got.Properties.IPAddress == nil || *got.Properties.IPAddress == "" {
		t.Errorf("PublicIP has no IP address")
	}

	// List
	pager := pipClient.NewListPager(azureResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListPager PublicIP: %v", err)
		}
		for _, v := range page.Value {
			if *v.Name == "my-pip" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("List PublicIPs did not find my-pip")
	}

	// Delete
	delPoller, err := pipClient.BeginDelete(ctx, azureResourceGroup, "my-pip", nil)
	if err != nil {
		t.Fatalf("BeginDelete PublicIP: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("delete PublicIP poll: %v", err)
	}
}

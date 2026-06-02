// Conformance: Azure Compute ARM frontend exercised by the official
// armcompute/v6 SDK. Covers Phase 16.C: VirtualMachines CRUD +
// start/deallocate/restart + vmSizes.list.
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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const (
	azureVMSubscription  = "00000000-0000-0000-0000-000000000002"
	azureVMResourceGroup = "shim-vm-rg"
)

// azureVMCredential signs JWTs for the ARM audience.
type azureVMCredential struct{}

func (azureVMCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

func newAzureVMClientOptions(endpoint string) *arm.ClientOptions {
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

func TestAzureSDK_Compute_VMLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzureVM(t, inmem.New())
	ctx := context.Background()
	opts := newAzureVMClientOptions(srv.URL)
	cred := azureVMCredential{}

	vmClient, err := armcompute.NewVirtualMachinesClient(azureVMSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new VirtualMachines client: %v", err)
	}

	// CreateOrUpdate
	poller, err := vmClient.BeginCreateOrUpdate(ctx, azureVMResourceGroup, "my-vm",
		armcompute.VirtualMachine{
			Location: to.Ptr("eastus"),
			Properties: &armcompute.VirtualMachineProperties{
				HardwareProfile: &armcompute.HardwareProfile{
					VMSize: to.Ptr(armcompute.VirtualMachineSizeTypesStandardD2SV3),
				},
				StorageProfile: &armcompute.StorageProfile{
					ImageReference: &armcompute.ImageReference{
						Publisher: to.Ptr("Canonical"),
						Offer:     to.Ptr("UbuntuServer"),
						SKU:       to.Ptr("18.04-LTS"),
					},
					OSDisk: &armcompute.OSDisk{
						CreateOption: to.Ptr(armcompute.DiskCreateOptionTypesFromImage),
					},
				},
				OSProfile: &armcompute.OSProfile{
					ComputerName:  to.Ptr("my-vm"),
					AdminUsername: to.Ptr("azureuser"),
				},
				NetworkProfile: &armcompute.NetworkProfile{},
			},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone (create): %v", err)
	}
	if *created.Name != "my-vm" {
		t.Errorf("VM name = %q, want my-vm", *created.Name)
	}

	// Get
	got, err := vmClient.Get(ctx, azureVMResourceGroup, "my-vm", nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if *got.Name != "my-vm" {
		t.Errorf("Get name = %q", *got.Name)
	}

	// List
	pager := vmClient.NewListPager(azureVMResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("NewListPager: %v", err)
		}
		for _, v := range page.Value {
			if *v.Name == "my-vm" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("List: my-vm not found")
	}

	// Start
	startPoller, err := vmClient.BeginStart(ctx, azureVMResourceGroup, "my-vm", nil)
	if err != nil {
		t.Fatalf("BeginStart: %v", err)
	}
	if _, err := startPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Start PollUntilDone: %v", err)
	}

	// Deallocate (stop)
	deallocPoller, err := vmClient.BeginDeallocate(ctx, azureVMResourceGroup, "my-vm", nil)
	if err != nil {
		t.Fatalf("BeginDeallocate: %v", err)
	}
	if _, err := deallocPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Deallocate PollUntilDone: %v", err)
	}

	// Restart
	restartPoller, err := vmClient.BeginRestart(ctx, azureVMResourceGroup, "my-vm", nil)
	if err != nil {
		t.Fatalf("BeginRestart: %v", err)
	}
	if _, err := restartPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Restart PollUntilDone: %v", err)
	}

	// Delete
	delPoller, err := vmClient.BeginDelete(ctx, azureVMResourceGroup, "my-vm", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("Delete PollUntilDone: %v", err)
	}
}

func TestAzureSDK_Compute_VMSizes(t *testing.T) {
	srv := harness.StartComputeServerAzureVM(t, inmem.New())
	ctx := context.Background()
	opts := newAzureVMClientOptions(srv.URL)
	cred := azureVMCredential{}

	sizesClient, err := armcompute.NewVirtualMachineSizesClient(azureVMSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new VMSizes client: %v", err)
	}

	pager := sizesClient.NewListPager("eastus", nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("VMSizes.List: %v", err)
		}
		for _, s := range page.Value {
			if s.Name != nil && *s.Name == "t3.micro" {
				found = true
				if *s.NumberOfCores != 2 {
					t.Errorf("t3.micro cores = %d, want 2", *s.NumberOfCores)
				}
			}
		}
	}
	if !found {
		t.Errorf("VMSizes.List: t3.micro not found")
	}
}

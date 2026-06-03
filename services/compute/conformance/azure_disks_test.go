// Conformance: Azure Compute-shaped block storage exercised by the
// official armcompute/v6 Disks + Snapshots clients. Covers Phase 17:
// disks createOrUpdate/get/list/delete and snapshots
// createOrUpdate/get/list/delete.
package conformance_test

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestAzureSDK_Compute_DiskLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzureVM(t, inmem.New())
	ctx := context.Background()
	opts := newAzureVMClientOptions(srv.URL)
	cred := azureVMCredential{}

	client, err := armcompute.NewDisksClient(azureVMSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new Disks client: %v", err)
	}

	// CreateOrUpdate
	poller, err := client.BeginCreateOrUpdate(ctx, azureVMResourceGroup, "shim-disk", armcompute.Disk{
		Location: to.Ptr("eastus"),
		SKU:      &armcompute.DiskSKU{Name: to.Ptr(armcompute.DiskStorageAccountTypesStandardLRS)},
		Properties: &armcompute.DiskProperties{
			DiskSizeGB: to.Ptr[int32](32),
			CreationData: &armcompute.CreationData{
				CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate: %v", err)
	}
	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("disk create poll: %v", err)
	}
	if created.Name == nil || *created.Name != "shim-disk" {
		t.Fatalf("created disk name = %v, want shim-disk", created.Name)
	}
	if created.Properties == nil || created.Properties.DiskSizeGB == nil || *created.Properties.DiskSizeGB != 32 {
		t.Errorf("created disk size = %v, want 32", created.Properties)
	}

	// Get
	got, err := client.Get(ctx, azureVMResourceGroup, "shim-disk", nil)
	if err != nil {
		t.Fatalf("Disks.Get: %v", err)
	}
	if got.Name == nil || *got.Name != "shim-disk" {
		t.Errorf("get disk name = %v, want shim-disk", got.Name)
	}

	// List
	pager := client.NewListByResourceGroupPager(azureVMResourceGroup, nil)
	found := false
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			t.Fatalf("Disks list page: %v", err)
		}
		for _, d := range page.Value {
			if d.Name != nil && *d.Name == "shim-disk" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("disk shim-disk not found in list")
	}

	// Delete
	delPoller, err := client.BeginDelete(ctx, azureVMResourceGroup, "shim-disk", nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("disk delete poll: %v", err)
	}
}

func TestAzureSDK_Compute_SnapshotLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAzureVM(t, inmem.New())
	ctx := context.Background()
	opts := newAzureVMClientOptions(srv.URL)
	cred := azureVMCredential{}

	disksClient, err := armcompute.NewDisksClient(azureVMSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new Disks client: %v", err)
	}
	snapClient, err := armcompute.NewSnapshotsClient(azureVMSubscription, cred, opts)
	if err != nil {
		t.Fatalf("new Snapshots client: %v", err)
	}

	// Create a source disk
	dp, err := disksClient.BeginCreateOrUpdate(ctx, azureVMResourceGroup, "snap-src", armcompute.Disk{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.DiskProperties{
			DiskSizeGB:   to.Ptr[int32](16),
			CreationData: &armcompute.CreationData{CreateOption: to.Ptr(armcompute.DiskCreateOptionEmpty)},
		},
	}, nil)
	if err != nil {
		t.Fatalf("create source disk: %v", err)
	}
	if _, err := dp.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("source disk poll: %v", err)
	}
	diskID := "/subscriptions/" + azureVMSubscription + "/resourceGroups/" + azureVMResourceGroup +
		"/providers/Microsoft.Compute/disks/snap-src"

	// Create a snapshot from the disk
	sp, err := snapClient.BeginCreateOrUpdate(ctx, azureVMResourceGroup, "shim-snap", armcompute.Snapshot{
		Location: to.Ptr("eastus"),
		Properties: &armcompute.SnapshotProperties{
			CreationData: &armcompute.CreationData{
				CreateOption:     to.Ptr(armcompute.DiskCreateOptionCopy),
				SourceResourceID: to.Ptr(diskID),
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate snapshot: %v", err)
	}
	snap, err := sp.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("snapshot create poll: %v", err)
	}
	if snap.Name == nil || *snap.Name != "shim-snap" {
		t.Fatalf("snapshot name = %v, want shim-snap", snap.Name)
	}

	// Get
	got, err := snapClient.Get(ctx, azureVMResourceGroup, "shim-snap", nil)
	if err != nil {
		t.Fatalf("Snapshots.Get: %v", err)
	}
	if got.Name == nil || *got.Name != "shim-snap" {
		t.Errorf("get snapshot name = %v, want shim-snap", got.Name)
	}

	// Delete
	delPoller, err := snapClient.BeginDelete(ctx, azureVMResourceGroup, "shim-snap", nil)
	if err != nil {
		t.Fatalf("BeginDelete snapshot: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("snapshot delete poll: %v", err)
	}
}

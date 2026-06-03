// Conformance: GCP Compute Engine block storage exercised by the official
// google.golang.org/api/compute/v1 REST SDK. Covers Phase 17:
// disks.insert/get/list/delete, snapshots.insert/get/list/delete, and
// instances.attachDisk/detachDisk.
package conformance_test

import (
	"context"
	"strings"
	"testing"

	computeraw "google.golang.org/api/compute/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const gcpZone = "us-central1-a"

func TestGCPSDK_Compute_DiskLifecycle(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeClient(t, srv.URL)
	ctx := context.Background()

	// Insert disk
	if _, err := svc.Disks.Insert(gcpProject, gcpZone, &computeraw.Disk{
		Name:   "shim-disk",
		SizeGb: 20,
		Type:   "zones/" + gcpZone + "/diskTypes/pd-ssd",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("disks.insert: %v", err)
	}

	// Get disk
	disk, err := svc.Disks.Get(gcpProject, gcpZone, "shim-disk").Context(ctx).Do()
	if err != nil {
		t.Fatalf("disks.get: %v", err)
	}
	if disk.Name != "shim-disk" {
		t.Errorf("disk Name = %q, want shim-disk", disk.Name)
	}
	if disk.SizeGb != 20 {
		t.Errorf("disk SizeGb = %d, want 20", disk.SizeGb)
	}
	if disk.Status != "READY" {
		t.Errorf("disk Status = %q, want READY", disk.Status)
	}

	// List disks — verify our disk appears
	list, err := svc.Disks.List(gcpProject, gcpZone).Context(ctx).Do()
	if err != nil {
		t.Fatalf("disks.list: %v", err)
	}
	found := false
	for _, d := range list.Items {
		if d.Name == "shim-disk" {
			found = true
		}
	}
	if !found {
		t.Errorf("disks.list does not contain shim-disk (got %d disks)", len(list.Items))
	}

	// Delete disk
	if _, err := svc.Disks.Delete(gcpProject, gcpZone, "shim-disk").Context(ctx).Do(); err != nil {
		t.Fatalf("disks.delete: %v", err)
	}
	list2, _ := svc.Disks.List(gcpProject, gcpZone).Context(ctx).Do()
	for _, d := range list2.Items {
		if d.Name == "shim-disk" {
			t.Errorf("disk shim-disk still present after delete")
		}
	}
}

func TestGCPSDK_Compute_SnapshotLifecycle(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeClient(t, srv.URL)
	ctx := context.Background()

	// Create source disk
	if _, err := svc.Disks.Insert(gcpProject, gcpZone, &computeraw.Disk{
		Name:   "snap-src-disk",
		SizeGb: 10,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("disks.insert: %v", err)
	}

	// Create snapshot via snapshots.insert with sourceDisk reference
	if _, err := svc.Snapshots.Insert(gcpProject, &computeraw.Snapshot{
		Name:        "shim-snap",
		SourceDisk:  "zones/" + gcpZone + "/disks/snap-src-disk",
		Description: "phase-17 gcp snapshot",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("snapshots.insert: %v", err)
	}

	// Get snapshot
	snap, err := svc.Snapshots.Get(gcpProject, "shim-snap").Context(ctx).Do()
	if err != nil {
		t.Fatalf("snapshots.get: %v", err)
	}
	if snap.Name != "shim-snap" {
		t.Errorf("snapshot Name = %q, want shim-snap", snap.Name)
	}
	if !strings.HasSuffix(snap.SourceDisk, "snap-src-disk") {
		t.Errorf("snapshot SourceDisk = %q, want suffix snap-src-disk", snap.SourceDisk)
	}

	// List snapshots
	list, err := svc.Snapshots.List(gcpProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("snapshots.list: %v", err)
	}
	found := false
	for _, s := range list.Items {
		if s.Name == "shim-snap" {
			found = true
		}
	}
	if !found {
		t.Errorf("snapshots.list does not contain shim-snap")
	}

	// Delete snapshot
	if _, err := svc.Snapshots.Delete(gcpProject, "shim-snap").Context(ctx).Do(); err != nil {
		t.Fatalf("snapshots.delete: %v", err)
	}
}

func TestGCPSDK_Compute_DiskAttachDetach(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	svc := newGCPComputeClient(t, srv.URL)
	ctx := context.Background()

	// Create an instance
	if _, err := svc.Instances.Insert(gcpProject, gcpZone, &computeraw.Instance{
		Name:        "attach-vm",
		MachineType: "zones/" + gcpZone + "/machineTypes/t3.micro",
		Disks: []*computeraw.AttachedDisk{{
			Boot:             true,
			InitializeParams: &computeraw.AttachedDiskInitializeParams{SourceImage: "ami-test"},
		}},
		NetworkInterfaces: []*computeraw.NetworkInterface{{}},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("instances.insert: %v", err)
	}

	// Create a data disk
	if _, err := svc.Disks.Insert(gcpProject, gcpZone, &computeraw.Disk{
		Name:   "data-disk",
		SizeGb: 30,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("disks.insert: %v", err)
	}

	// Attach the data disk to the instance
	if _, err := svc.Instances.AttachDisk(gcpProject, gcpZone, "attach-vm", &computeraw.AttachedDisk{
		Source:     "zones/" + gcpZone + "/disks/data-disk",
		DeviceName: "data-disk",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("instances.attachDisk: %v", err)
	}

	// Verify the disk is now in-use (has a user)
	disk, err := svc.Disks.Get(gcpProject, gcpZone, "data-disk").Context(ctx).Do()
	if err != nil {
		t.Fatalf("disks.get after attach: %v", err)
	}
	if len(disk.Users) == 0 {
		t.Errorf("expected data-disk to have a user after attach, got none")
	}

	// Detach
	if _, err := svc.Instances.DetachDisk(gcpProject, gcpZone, "attach-vm", "data-disk").Context(ctx).Do(); err != nil {
		t.Fatalf("instances.detachDisk: %v", err)
	}
	disk2, _ := svc.Disks.Get(gcpProject, gcpZone, "data-disk").Context(ctx).Do()
	if len(disk2.Users) != 0 {
		t.Errorf("expected data-disk to have no users after detach, got %d", len(disk2.Users))
	}
}

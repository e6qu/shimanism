// Conformance: GCP Compute Engine-shaped instance lifecycle exercised by
// the official google.golang.org/api/compute/v1 REST SDK. Covers
// Phase 16.C: instances.insert/get/list/delete/start/stop/reset +
// machineTypes.list/get + aggregatedList.
package conformance_test

import (
	"context"
	"testing"

	computeraw "google.golang.org/api/compute/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestGCPSDK_Compute_InstanceLifecycle(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	ctx := context.Background()
	svc := newGCPComputeClient(t, srv.URL)

	project, zone := gcpProject, "us-central1-a"

	// instances.insert
	insertOp, err := svc.Instances.Insert(project, zone, &computeraw.Instance{
		Name:        "test-vm",
		MachineType: "zones/us-central1-a/machineTypes/t3.micro",
		Disks: []*computeraw.AttachedDisk{{
			Boot: true,
			InitializeParams: &computeraw.AttachedDiskInitializeParams{
				SourceImage: "ami-12345678",
			},
		}},
		NetworkInterfaces: []*computeraw.NetworkInterface{{}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.insert: %v", err)
	}
	if insertOp.Status != "DONE" {
		t.Errorf("insert op status = %q, want DONE", insertOp.Status)
	}

	// instances.list
	list, err := svc.Instances.List(project, zone).Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.list: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("instances.list: expected ≥1 instance, got 0")
	}

	var found *computeraw.Instance
	for _, i := range list.Items {
		if i.Name == "test-vm" {
			found = i
			break
		}
	}
	if found == nil {
		t.Fatalf("instances.list: test-vm not found")
	}

	// instances.get
	got, err := svc.Instances.Get(project, zone, "test-vm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.get: %v", err)
	}
	if got.Name != "test-vm" {
		t.Errorf("instances.get name = %q, want test-vm", got.Name)
	}

	// instances.stop
	stopOp, err := svc.Instances.Stop(project, zone, "test-vm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.stop: %v", err)
	}
	if stopOp.Status != "DONE" {
		t.Errorf("stop op status = %q, want DONE", stopOp.Status)
	}

	// instances.start
	startOp, err := svc.Instances.Start(project, zone, "test-vm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.start: %v", err)
	}
	if startOp.Status != "DONE" {
		t.Errorf("start op status = %q, want DONE", startOp.Status)
	}

	// instances.reset (reboot)
	resetOp, err := svc.Instances.Reset(project, zone, "test-vm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.reset: %v", err)
	}
	if resetOp.Status != "DONE" {
		t.Errorf("reset op status = %q, want DONE", resetOp.Status)
	}

	// instances.delete
	deleteOp, err := svc.Instances.Delete(project, zone, "test-vm").Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.delete: %v", err)
	}
	if deleteOp.Status != "DONE" {
		t.Errorf("delete op status = %q, want DONE", deleteOp.Status)
	}

	// After delete: list should be empty (or not contain test-vm)
	list2, err := svc.Instances.List(project, zone).Context(ctx).Do()
	if err != nil {
		t.Fatalf("instances.list post-delete: %v", err)
	}
	for _, i := range list2.Items {
		if i.Name == "test-vm" {
			t.Errorf("test-vm still present after delete")
		}
	}
}

func TestGCPSDK_Compute_AggregatedListInstances(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	ctx := context.Background()
	svc := newGCPComputeClient(t, srv.URL)
	project, zone := gcpProject, "us-central1-a"

	_, err := svc.Instances.Insert(project, zone, &computeraw.Instance{
		Name:        "agg-vm",
		MachineType: "zones/us-central1-a/machineTypes/m5.large",
		Disks: []*computeraw.AttachedDisk{{
			Boot:             true,
			InitializeParams: &computeraw.AttachedDiskInitializeParams{SourceImage: "ami-test"},
		}},
		NetworkInterfaces: []*computeraw.NetworkInterface{{}},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	agg, err := svc.Instances.AggregatedList(project).Context(ctx).Do()
	if err != nil {
		t.Fatalf("aggregatedList: %v", err)
	}
	total := 0
	for _, items := range agg.Items {
		total += len(items.Instances)
	}
	if total == 0 {
		t.Errorf("aggregatedList: expected ≥1 instance, got 0")
	}
}

func TestGCPSDK_Compute_MachineTypes(t *testing.T) {
	srv := harness.StartComputeServerGCP(t, inmem.New())
	ctx := context.Background()
	svc := newGCPComputeClient(t, srv.URL)
	project, zone := gcpProject, "us-central1-a"

	// machineTypes.list
	list, err := svc.MachineTypes.List(project, zone).Context(ctx).Do()
	if err != nil {
		t.Fatalf("machineTypes.list: %v", err)
	}
	if len(list.Items) == 0 {
		t.Fatalf("machineTypes.list: expected ≥1 type, got 0")
	}

	// machineTypes.get
	mt, err := svc.MachineTypes.Get(project, zone, "t3.micro").Context(ctx).Do()
	if err != nil {
		t.Fatalf("machineTypes.get t3.micro: %v", err)
	}
	if mt.Name != "t3.micro" {
		t.Errorf("machineTypes.get name = %q, want t3.micro", mt.Name)
	}
	if mt.GuestCpus != 2 {
		t.Errorf("machineTypes.get GuestCpus = %d, want 2", mt.GuestCpus)
	}
}

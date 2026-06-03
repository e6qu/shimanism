package inmem_test

import (
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestVolume_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	// CreateVolume
	vol, err := b.CreateVolume(ctx, domain.CreateVolumeOptions{
		SizeGiB:    20,
		VolumeType: "gp3",
		Zone:       "us-east-1a",
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if vol.ID == "" || vol.SizeGiB != 20 || vol.VolumeType != "gp3" {
		t.Fatalf("CreateVolume: unexpected result %+v", vol)
	}
	if vol.State != domain.VolumeStateAvailable {
		t.Errorf("initial state = %q, want available", vol.State)
	}

	// DescribeVolumes by ID
	res, err := b.DescribeVolumes(ctx, domain.DescribeVolumesOptions{IDs: []string{vol.ID}})
	if err != nil || len(res.Volumes) != 1 {
		t.Fatalf("DescribeVolumes: %v count=%d", err, len(res.Volumes))
	}

	// CreateInstance for attach
	insts, err := b.RunInstances(ctx, domain.RunInstancesOptions{
		ImageID: "ami-test", InstanceType: "t3.micro", MinCount: 1, MaxCount: 1,
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	instID := insts[0].ID

	// AttachVolume
	att, err := b.AttachVolume(ctx, vol.ID, instID, domain.AttachVolumeOptions{DeviceName: "/dev/sdf"})
	if err != nil {
		t.Fatalf("AttachVolume: %v", err)
	}
	if att.State != domain.VolumeAttachmentStateAttached || att.DeviceName != "/dev/sdf" {
		t.Errorf("AttachVolume: unexpected attachment %+v", att)
	}

	// Volume should now be in-use
	res2, _ := b.DescribeVolumes(ctx, domain.DescribeVolumesOptions{IDs: []string{vol.ID}})
	if res2.Volumes[0].State != domain.VolumeStateInUse || res2.Volumes[0].InstanceID != instID {
		t.Errorf("post-attach state = %+v", res2.Volumes[0])
	}

	// DetachVolume
	det, err := b.DetachVolume(ctx, vol.ID, instID)
	if err != nil {
		t.Fatalf("DetachVolume: %v", err)
	}
	if det.State != domain.VolumeAttachmentStateDetached {
		t.Errorf("DetachVolume state = %q", det.State)
	}

	// Volume back to available
	res3, _ := b.DescribeVolumes(ctx, domain.DescribeVolumesOptions{IDs: []string{vol.ID}})
	if res3.Volumes[0].State != domain.VolumeStateAvailable {
		t.Errorf("post-detach state = %q, want available", res3.Volumes[0].State)
	}

	// DeleteVolume
	if err := b.DeleteVolume(ctx, vol.ID); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	if _, err := b.DescribeVolumes(ctx, domain.DescribeVolumesOptions{IDs: []string{vol.ID}}); err != nil {
		t.Fatalf("DescribeVolumes post-delete: %v", err)
	}
	res4, _ := b.DescribeVolumes(ctx, domain.DescribeVolumesOptions{IDs: []string{vol.ID}})
	if len(res4.Volumes) != 0 {
		t.Errorf("expected 0 volumes after delete, got %d", len(res4.Volumes))
	}
}

func TestSnapshot_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	// Create source volume
	vol, _ := b.CreateVolume(ctx, domain.CreateVolumeOptions{SizeGiB: 10})

	// CreateSnapshot
	snap, err := b.CreateSnapshot(ctx, vol.ID, domain.CreateSnapshotOptions{
		Description: "test-snap",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if snap.ID == "" || snap.VolumeID != vol.ID || snap.VolumeSize != 10 {
		t.Fatalf("CreateSnapshot: unexpected %+v", snap)
	}
	if snap.State != domain.SnapshotStateCompleted {
		t.Errorf("snapshot state = %q, want completed", snap.State)
	}

	// DescribeSnapshots by ID
	res, err := b.DescribeSnapshots(ctx, domain.DescribeSnapshotsOptions{IDs: []string{snap.ID}})
	if err != nil || len(res.Snapshots) != 1 {
		t.Fatalf("DescribeSnapshots: %v count=%d", err, len(res.Snapshots))
	}

	// DescribeSnapshots by VolumeID
	byVol, _ := b.DescribeSnapshots(ctx, domain.DescribeSnapshotsOptions{VolumeID: vol.ID})
	if len(byVol.Snapshots) != 1 {
		t.Errorf("DescribeSnapshots by volume: got %d, want 1", len(byVol.Snapshots))
	}

	// CreateVolume from snapshot
	vol2, err := b.CreateVolume(ctx, domain.CreateVolumeOptions{SizeGiB: 10, SnapshotID: snap.ID})
	if err != nil {
		t.Fatalf("CreateVolume from snapshot: %v", err)
	}
	if vol2.SnapshotID != snap.ID {
		t.Errorf("volume SnapshotID = %q, want %q", vol2.SnapshotID, snap.ID)
	}

	// DeleteSnapshot
	if err := b.DeleteSnapshot(ctx, snap.ID); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	res2, _ := b.DescribeSnapshots(ctx, domain.DescribeSnapshotsOptions{IDs: []string{snap.ID}})
	if len(res2.Snapshots) != 0 {
		t.Errorf("expected 0 snapshots after delete, got %d", len(res2.Snapshots))
	}
}

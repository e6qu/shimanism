// Conformance: AWS EC2-shaped block storage frontend exercised by the
// official aws-sdk-go-v2/service/ec2 SDK. Covers Phase 17:
// CreateVolume, DescribeVolumes, AttachVolume, DetachVolume, DeleteVolume,
// CreateSnapshot, DescribeSnapshots, DeleteSnapshot.
package conformance_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

func TestAWSSDK_EBS_VolumeLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVolume
	create, err := cli.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(20),
		VolumeType:       ec2types.VolumeTypeGp3,
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volID := aws.ToString(create.VolumeId)
	if volID == "" {
		t.Fatal("CreateVolume: empty VolumeId")
	}
	if create.Size == nil || *create.Size != 20 {
		t.Errorf("CreateVolume Size = %v, want 20", create.Size)
	}
	if create.State != ec2types.VolumeStateAvailable {
		t.Errorf("CreateVolume State = %v, want available", create.State)
	}
	t.Cleanup(func() {
		cli.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volID)}) //nolint:errcheck
	})

	// DescribeVolumes by ID
	desc, err := cli.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		VolumeIds: []string{volID},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes: %v", err)
	}
	if len(desc.Volumes) != 1 || aws.ToString(desc.Volumes[0].VolumeId) != volID {
		t.Fatalf("DescribeVolumes: unexpected result (count=%d)", len(desc.Volumes))
	}

	// Create an instance so we can attach
	run, err := cli.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      aws.String("ami-test"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
	})
	if err != nil {
		t.Fatalf("RunInstances for attach: %v", err)
	}
	instID := aws.ToString(run.Instances[0].InstanceId)
	t.Cleanup(func() {
		cli.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{instID}}) //nolint:errcheck
	})

	// AttachVolume
	att, err := cli.AttachVolume(ctx, &ec2.AttachVolumeInput{
		VolumeId:   aws.String(volID),
		InstanceId: aws.String(instID),
		Device:     aws.String("/dev/sdf"),
	})
	if err != nil {
		t.Fatalf("AttachVolume: %v", err)
	}
	if att.State != ec2types.VolumeAttachmentStateAttached {
		t.Errorf("AttachVolume State = %v, want attached", att.State)
	}

	// DescribeVolumes shows in-use
	desc2, _ := cli.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volID}})
	if len(desc2.Volumes) > 0 && desc2.Volumes[0].State != ec2types.VolumeStateInUse {
		t.Errorf("post-attach state = %v, want in-use", desc2.Volumes[0].State)
	}

	// DetachVolume
	det, err := cli.DetachVolume(ctx, &ec2.DetachVolumeInput{
		VolumeId:   aws.String(volID),
		InstanceId: aws.String(instID),
	})
	if err != nil {
		t.Fatalf("DetachVolume: %v", err)
	}
	if det.State != ec2types.VolumeAttachmentStateDetached {
		t.Errorf("DetachVolume State = %v, want detached", det.State)
	}

	// DeleteVolume
	if _, err := cli.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volID)}); err != nil {
		t.Fatalf("DeleteVolume: %v", err)
	}
	desc3, _ := cli.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volID}})
	if len(desc3.Volumes) != 0 {
		t.Errorf("expected 0 volumes after delete, got %d", len(desc3.Volumes))
	}
}

func TestAWSSDK_EBS_SnapshotLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVolume as source
	vol, err := cli.CreateVolume(ctx, &ec2.CreateVolumeInput{
		AvailabilityZone: aws.String("us-east-1a"),
		Size:             aws.Int32(10),
		VolumeType:       ec2types.VolumeTypeGp3,
	})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volID := aws.ToString(vol.VolumeId)
	t.Cleanup(func() {
		cli.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volID)}) //nolint:errcheck
	})

	// CreateSnapshot
	snap, err := cli.CreateSnapshot(ctx, &ec2.CreateSnapshotInput{
		VolumeId:    aws.String(volID),
		Description: aws.String("phase-17-test-snap"),
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	snapID := aws.ToString(snap.SnapshotId)
	if snapID == "" {
		t.Fatal("CreateSnapshot: empty SnapshotId")
	}
	if snap.State != ec2types.SnapshotStateCompleted {
		t.Errorf("CreateSnapshot State = %v, want completed", snap.State)
	}
	t.Cleanup(func() {
		cli.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{SnapshotId: aws.String(snapID)}) //nolint:errcheck
	})

	// DescribeSnapshots by ID
	desc, err := cli.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{snapID},
	})
	if err != nil {
		t.Fatalf("DescribeSnapshots: %v", err)
	}
	if len(desc.Snapshots) != 1 || aws.ToString(desc.Snapshots[0].SnapshotId) != snapID {
		t.Fatalf("DescribeSnapshots: unexpected result (count=%d)", len(desc.Snapshots))
	}
	if aws.ToString(desc.Snapshots[0].VolumeId) != volID {
		t.Errorf("DescribeSnapshots VolumeId = %q, want %q", aws.ToString(desc.Snapshots[0].VolumeId), volID)
	}

	// DeleteSnapshot
	if _, err := cli.DeleteSnapshot(ctx, &ec2.DeleteSnapshotInput{SnapshotId: aws.String(snapID)}); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	desc2, _ := cli.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{SnapshotIds: []string{snapID}})
	if len(desc2.Snapshots) != 0 {
		t.Errorf("expected 0 snapshots after delete, got %d", len(desc2.Snapshots))
	}
}

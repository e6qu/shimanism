// Conformance: K8s block-storage peer exercised via the AWS EC2 frontend.
// Volumes map to PersistentVolumeClaims; attach/detach and snapshots are
// out-of-intersection on K8s and must return the source cloud's
// "UnsupportedOperation" error rather than a silent success (N28).
package conformance_test

import (
	"context"
	"strings"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/k8scompute"
)

func TestK8sPeer_AWSShaped_VolumeLifecycle(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVolume → PersistentVolumeClaim
	create, err := cli.CreateVolume(ctx, &awsec2sdk.CreateVolumeInput{
		AvailabilityZone: awsapi.String("us-east-1a"),
		Size:             awsapi.Int32(10),
	})
	if err != nil {
		t.Fatalf("CreateVolume (K8s peer): %v", err)
	}
	volID := awsapi.ToString(create.VolumeId)
	if volID == "" {
		t.Fatal("empty VolumeId from K8s peer")
	}

	// DescribeVolumes → list PVCs
	desc, err := cli.DescribeVolumes(ctx, &awsec2sdk.DescribeVolumesInput{
		VolumeIds: []string{volID},
	})
	if err != nil {
		t.Fatalf("DescribeVolumes (K8s peer): %v", err)
	}
	if len(desc.Volumes) != 1 || awsapi.ToString(desc.Volumes[0].VolumeId) != volID {
		t.Fatalf("DescribeVolumes: expected 1 volume %q, got %d", volID, len(desc.Volumes))
	}

	// DeleteVolume → delete PVC
	if _, err := cli.DeleteVolume(ctx, &awsec2sdk.DeleteVolumeInput{VolumeId: awsapi.String(volID)}); err != nil {
		t.Fatalf("DeleteVolume (K8s peer): %v", err)
	}
	desc2, _ := cli.DescribeVolumes(ctx, &awsec2sdk.DescribeVolumesInput{VolumeIds: []string{volID}})
	if len(desc2.Volumes) != 0 {
		t.Errorf("expected 0 volumes after delete, got %d", len(desc2.Volumes))
	}
}

func TestK8sPeer_AWSShaped_VolumeAttachUnsupported(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// AttachVolume is out-of-intersection on K8s (no imperative attach API).
	_, err := cli.AttachVolume(ctx, &awsec2sdk.AttachVolumeInput{
		VolumeId:   awsapi.String("vol-x"),
		InstanceId: awsapi.String("i-x"),
		Device:     awsapi.String("/dev/sdf"),
	})
	if err == nil {
		t.Fatal("AttachVolume on K8s peer should fail, got nil")
	}
	if !strings.Contains(err.Error(), "UnsupportedOperation") {
		t.Errorf("AttachVolume error = %v, want UnsupportedOperation", err)
	}
}

func TestK8sPeer_AWSShaped_SnapshotUnsupported(t *testing.T) {
	k8s := k8scompute.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartComputeServerAWS(t, k8s)
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateSnapshot is out-of-intersection on K8s (VolumeSnapshot is a CSI
	// CRD, not a core built-in).
	_, err := cli.CreateSnapshot(ctx, &awsec2sdk.CreateSnapshotInput{
		VolumeId: awsapi.String("vol-x"),
	})
	if err == nil {
		t.Fatal("CreateSnapshot on K8s peer should fail, got nil")
	}
	if !strings.Contains(err.Error(), "UnsupportedOperation") {
		t.Errorf("CreateSnapshot error = %v, want UnsupportedOperation", err)
	}
}

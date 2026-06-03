// Package domain — volumes.go covers Phase 17: block storage lifecycle
// (volumes + snapshots) neutral across AWS EBS, GCP Persistent Disk,
// Azure Managed Disk, and K8s PersistentVolume.
//
// Normalization rules:
//   - N28: volume size in GiB; volume type is opaque per-cloud.
//   - N28: attach/detach are synchronous in the domain; the frontend
//     returns the result immediately (same as GCP operations DONE pattern).
package domain

import "context"

// ──────────────────────────────────────────────
// Volume
// ──────────────────────────────────────────────

// VolumeState is the normalized volume lifecycle state (N28).
type VolumeState string

const (
	VolumeStateCreating  VolumeState = "creating"
	VolumeStateAvailable VolumeState = "available"
	VolumeStateInUse     VolumeState = "in-use"
	VolumeStateDeleting  VolumeState = "deleting"
	VolumeStateDeleted   VolumeState = "deleted"
	VolumeStateError     VolumeState = "error"
)

// Volume is the neutral representation of a block storage volume.
type Volume struct {
	ID         string
	Name       string
	SizeGiB    int    // size in gibibytes (N28)
	VolumeType string // gp3 / pd-ssd / Premium_LRS — opaque (N28)
	State      VolumeState
	Zone       string
	InstanceID string // non-empty when in-use
	DeviceName string // e.g. /dev/sdf; empty when not attached
	SnapshotID string // source snapshot ID if created from one
	Tags       map[string]string
}

// CreateVolumeOptions carries inputs for CreateVolume.
type CreateVolumeOptions struct {
	SizeGiB    int    // required
	VolumeType string // optional; opaque per-cloud default if empty
	Zone       string // optional
	SnapshotID string // optional; create from snapshot
	Tags       map[string]string
}

// DescribeVolumesOptions carries optional filters for DescribeVolumes.
type DescribeVolumesOptions struct {
	IDs        []string
	InstanceID string // filter to volumes attached to this instance
}

// DescribeVolumesResult is the result of DescribeVolumes.
type DescribeVolumesResult struct {
	Volumes []Volume
}

// AttachVolumeOptions carries inputs for AttachVolume.
type AttachVolumeOptions struct {
	DeviceName string // e.g. /dev/sdf; optional
}

// VolumeAttachment is the result of AttachVolume / DetachVolume.
type VolumeAttachment struct {
	VolumeID   string
	InstanceID string
	DeviceName string
	State      VolumeAttachmentState
}

// VolumeAttachmentState is the normalized attachment state.
type VolumeAttachmentState string

const (
	VolumeAttachmentStateAttaching VolumeAttachmentState = "attaching"
	VolumeAttachmentStateAttached  VolumeAttachmentState = "attached"
	VolumeAttachmentStateDetaching VolumeAttachmentState = "detaching"
	VolumeAttachmentStateDetached  VolumeAttachmentState = "detached"
)

// ──────────────────────────────────────────────
// Snapshot
// ──────────────────────────────────────────────

// SnapshotState is the normalized snapshot lifecycle state.
type SnapshotState string

const (
	SnapshotStatePending   SnapshotState = "pending"
	SnapshotStateCompleted SnapshotState = "completed"
	SnapshotStateError     SnapshotState = "error"
)

// Snapshot is the neutral representation of a volume snapshot.
type Snapshot struct {
	ID          string
	VolumeID    string
	VolumeSize  int // GiB of the source volume
	State       SnapshotState
	Description string
	Tags        map[string]string
}

// CreateSnapshotOptions carries inputs for CreateSnapshot.
type CreateSnapshotOptions struct {
	Description string
	Tags        map[string]string
}

// DescribeSnapshotsOptions carries optional filters for DescribeSnapshots.
type DescribeSnapshotsOptions struct {
	IDs      []string
	VolumeID string
}

// DescribeSnapshotsResult is the result of DescribeSnapshots.
type DescribeSnapshotsResult struct {
	Snapshots []Snapshot
}

// ──────────────────────────────────────────────
// Interface
// ──────────────────────────────────────────────

// BlockStorage is the neutral interface for volume + snapshot lifecycle.
// Implemented by the inmem backend, and real cloud backends for AWS EBS,
// GCP Persistent Disk, Azure Managed Disk, and K8s PV/PVC.
type BlockStorage interface {
	// Volume lifecycle.
	CreateVolume(ctx context.Context, opts CreateVolumeOptions) (Volume, error)
	DescribeVolumes(ctx context.Context, opts DescribeVolumesOptions) (DescribeVolumesResult, error)
	DeleteVolume(ctx context.Context, id string) error

	// Volume attachment.
	AttachVolume(ctx context.Context, volumeID, instanceID string, opts AttachVolumeOptions) (VolumeAttachment, error)
	DetachVolume(ctx context.Context, volumeID, instanceID string) (VolumeAttachment, error)

	// Snapshot lifecycle.
	CreateSnapshot(ctx context.Context, volumeID string, opts CreateSnapshotOptions) (Snapshot, error)
	DescribeSnapshots(ctx context.Context, opts DescribeSnapshotsOptions) (DescribeSnapshotsResult, error)
	DeleteSnapshot(ctx context.Context, id string) error
}

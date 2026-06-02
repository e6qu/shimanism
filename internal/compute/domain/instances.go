// Package domain — instances.go covers Phase 16.C: compute instance
// lifecycle interface and types (neutral across AWS EC2, GCP Compute
// Engine, Azure Compute, and K8s Nodes).
//
// Normalization rules applied here:
//   - N20: instance state machine — {pending, running, stopped, terminated}.
//   - N23: machine type naming is opaque per-cloud; passes through untranslated.
//   - N24: image reference is opaque per-cloud; passes through untranslated.
//
// **Stateless.** All state lives in the destination backend.
package domain

import "context"

// ──────────────────────────────────────────────
// Instance
// ──────────────────────────────────────────────

// InstanceState is the normalized instance lifecycle state (N20).
type InstanceState string

const (
	InstanceStatePending    InstanceState = "pending"
	InstanceStateRunning    InstanceState = "running"
	InstanceStateStopped    InstanceState = "stopped"
	InstanceStateTerminated InstanceState = "terminated"
)

// Instance is the neutral representation of a compute instance.
type Instance struct {
	// ID is the destination-cloud-native resource identifier.
	ID           string
	Name         string
	ImageID      string        // AMI / GCE image / Azure image URN — opaque (N24)
	InstanceType string        // m5.large / n1-standard-1 / Standard_D2s_v3 — opaque (N23)
	State        InstanceState // N20 normalized state
	// NetworkID is the parent network (VPC / VNet / GCP network).
	NetworkID string
	// SubnetID is the parent subnet. May be empty when not applicable.
	SubnetID string
	// PrivateIP is the primary private IPv4 address. Assigned by backend.
	PrivateIP string
	// PublicIP is the primary public IPv4 address. May be empty.
	PublicIP string
	// SecurityGroupIDs are the attached security group IDs.
	SecurityGroupIDs []string
	// KeyName is the SSH key pair name. Opaque; backend-assigned.
	KeyName string
	Tags    map[string]string
}

// RunInstancesOptions carries inputs for RunInstances.
type RunInstancesOptions struct {
	ImageID          string            // required
	InstanceType     string            // required; opaque per-cloud (N23)
	MinCount         int               // minimum instances to launch (default 1)
	MaxCount         int               // maximum instances to launch (default 1)
	NetworkID        string            // optional VPC/VNet/network ID
	SubnetID         string            // optional subnet ID
	SecurityGroupIDs []string          // optional
	KeyName          string            // optional
	UserData         string            // optional; base64-encoded on AWS
	Tags             map[string]string // optional
}

// DescribeInstancesOptions carries optional filters for DescribeInstances.
type DescribeInstancesOptions struct {
	// IDs filters to specific instance IDs. Empty = all.
	IDs []string
	// States filters by instance state. Empty = all states.
	States []InstanceState
}

// DescribeInstancesResult is the result of DescribeInstances.
type DescribeInstancesResult struct {
	Instances []Instance
	NextToken string
}

// ──────────────────────────────────────────────
// InstanceType
// ──────────────────────────────────────────────

// InstanceTypeInfo describes the capabilities of an instance type.
type InstanceTypeInfo struct {
	// InstanceType is the type identifier (opaque per-cloud, N23).
	InstanceType string
	// VCPUs is the number of virtual CPUs.
	VCPUs int
	// MemoryMiB is the memory in mebibytes.
	MemoryMiB int
}

// DescribeInstanceTypesOptions carries optional filters.
type DescribeInstanceTypesOptions struct {
	// InstanceTypes filters to specific type strings. Empty = all.
	InstanceTypes []string
}

// DescribeInstanceTypesResult is the result of DescribeInstanceTypes.
type DescribeInstanceTypesResult struct {
	InstanceTypes []InstanceTypeInfo
	NextToken     string
}

// ──────────────────────────────────────────────
// Instances interface
// ──────────────────────────────────────────────

// Instances is the neutral compute-instance interface. All backends
// implement this; frontends dispatch through it.
type Instances interface {
	// RunInstances launches one or more instances. Returns all launched
	// instances (may be fewer than MaxCount if the backend can't
	// satisfy the full request).
	RunInstances(ctx context.Context, opt RunInstancesOptions) ([]Instance, error)

	// DescribeInstances returns instances matching the filter options.
	DescribeInstances(ctx context.Context, opt DescribeInstancesOptions) (DescribeInstancesResult, error)

	// StartInstances starts stopped instances. Returns the new state
	// for each instance ID provided.
	StartInstances(ctx context.Context, ids []string) ([]Instance, error)

	// StopInstances stops running instances.
	StopInstances(ctx context.Context, ids []string) ([]Instance, error)

	// TerminateInstances terminates instances (irreversible).
	TerminateInstances(ctx context.Context, ids []string) ([]Instance, error)

	// RebootInstances reboots running instances.
	RebootInstances(ctx context.Context, ids []string) error

	// DescribeInstanceTypes returns capabilities for known instance types.
	DescribeInstanceTypes(ctx context.Context, opt DescribeInstanceTypesOptions) (DescribeInstanceTypesResult, error)
}

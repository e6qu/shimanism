// Package domain holds shimanism's neutral RDBMS control-plane
// interface and types. The interface is the lingua franca between
// three frontend protocols (AWS RDS awsQuery, GCP Cloud SQL Admin
// REST, Azure DB Admin ARM REST) and four backends (the three
// clouds + CloudNativePG / MySQL Operator as the K8s peer); each
// frontend translates its wire types into this domain, each backend
// translates this domain into its cloud's native API.
//
// **Phase 5 is control plane only.** The shim provisions instances
// and returns connection metadata; clients open direct
// PostgreSQL/MySQL connections to the returned host. The shim is
// not on the wire-protocol data path. See
// `services/rdbms/OPERATIONS.md` for design rationale.
//
// **Async semantics are explicit.** All four backends provision
// asynchronously. The domain models this with a `Status` field
// clients poll after `CreateInstance`. The shim doesn't fake
// synchronous-on-top-of-async — the wire protocols + SDKs all
// surface async-ness anyway.
//
// **Stateless credentials.** Master password returned exactly once
// at `CreateInstance` time. The CloudNativePG backend stores
// credentials in a Kubernetes Secret; the shim re-reads that Secret
// on each `DescribeInstance`. No shim-side credential cache.
package domain

import (
	"context"
	"time"
)

// Engine names the database engine family. Two engines are in the
// intersection; backends translate to their cloud's native engine
// identifier (`postgres` / `aurora-postgresql` on AWS, `POSTGRES_15`
// on GCP, `Microsoft.DBforPostgreSQL/flexibleServers` on Azure,
// CloudNativePG `Cluster` for Postgres; MySQL Operator
// `InnoDBCluster` for MySQL).
type Engine int

const (
	EngineUnknown Engine = iota
	EnginePostgres
	EngineMySQL
)

func (e Engine) String() string {
	switch e {
	case EnginePostgres:
		return "postgres"
	case EngineMySQL:
		return "mysql"
	default:
		return "unknown"
	}
}

// ParseEngine accepts the canonical strings "postgres" / "mysql"
// (lowercase). Returns EngineUnknown on anything else.
func ParseEngine(s string) Engine {
	switch s {
	case "postgres", "postgresql":
		return EnginePostgres
	case "mysql":
		return EngineMySQL
	default:
		return EngineUnknown
	}
}

// Status is the explicit async state. Clients poll DescribeInstance
// until Status == StatusAvailable.
type Status int

const (
	StatusUnknown Status = iota
	StatusCreating
	StatusAvailable
	StatusModifying
	StatusRebooting
	StatusDeleting
)

func (s Status) String() string {
	switch s {
	case StatusCreating:
		return "creating"
	case StatusAvailable:
		return "available"
	case StatusModifying:
		return "modifying"
	case StatusRebooting:
		return "rebooting"
	case StatusDeleting:
		return "deleting"
	default:
		return "unknown"
	}
}

// Instance describes a managed-DB instance's control-plane metadata.
type Instance struct {
	Name              string
	Engine            Engine
	EngineVersion     string
	Status            Status
	AllocatedStorageGB int
	InstanceClass     string
	Connection        Connection
	CreatedAt         time.Time
}

// Connection is what clients need to open a direct DB connection
// after Status == Available. The master password is **not** here —
// it's returned exactly once by CreateInstance and not re-surfaced.
type Connection struct {
	Host           string
	Port           int
	MasterUsername string
	DatabaseName   string
	EngineVersion  string
}

// Snapshot is a point-in-time copy of an instance.
type Snapshot struct {
	ID         string
	Instance   string
	Engine     Engine
	EngineVersion string
	CreatedAt  time.Time
	Status     Status
}

// CreateInstanceOptions controls CreateInstance.
type CreateInstanceOptions struct {
	Engine             Engine
	EngineVersion      string
	MasterUsername     string
	// MasterPassword may be empty — backends that support generating
	// a password (CNPG, GCP) will do so and surface it once in the
	// result. AWS RDS accepts either generated or user-provided.
	MasterPassword     string
	AllocatedStorageGB int
	// InstanceClass is the per-cloud sizing tier ("db.t3.micro" on
	// AWS, "db-custom-1-3840" on GCP, "Standard_B1ms" on Azure).
	// Backends translate; cnpg ignores (sizing is per-pod resource
	// requests, not a tier name).
	InstanceClass string
}

// CreateInstanceResult is the CreateInstance response.
type CreateInstanceResult struct {
	Instance Instance
	// MasterPassword is the password — present iff the caller
	// didn't supply one. Surfaced exactly once at create time.
	MasterPassword string
}

// ModifyInstanceOptions controls ModifyInstance. All fields are
// optional; only the ones set are changed.
type ModifyInstanceOptions struct {
	AllocatedStorageGB int
	InstanceClass      string
	MasterPassword     string
}

// ListInstancesOptions controls ListInstances.
type ListInstancesOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListInstancesResult is the ListInstances response.
type ListInstancesResult struct {
	Instances []Instance
	NextToken string
}

// ListSnapshotsOptions controls ListSnapshots.
type ListSnapshotsOptions struct {
	Instance   string // optional filter
	MaxResults int
	NextToken  string
}

// ListSnapshotsResult is the ListSnapshots response.
type ListSnapshotsResult struct {
	Snapshots []Snapshot
	NextToken string
}

// RDBMS is the interface every rdbms backend implements.
// Implementations must be safe for concurrent use across goroutines.
type RDBMS interface {
	// CreateInstance provisions a new DB instance. Returns
	// immediately with Status=Creating; clients poll DescribeInstance
	// until Available.
	CreateInstance(ctx context.Context, name string, opt CreateInstanceOptions) (CreateInstanceResult, error)

	// DeleteInstance removes an instance. Async on every backend —
	// the instance enters Status=Deleting and disappears once the
	// backend has reclaimed resources.
	DeleteInstance(ctx context.Context, name string) error

	// DescribeInstance returns the current control-plane state for
	// an instance, including the Connection block (populated once
	// Status == Available).
	DescribeInstance(ctx context.Context, name string) (Instance, error)

	// ListInstances returns instances, optionally filtered by name
	// prefix.
	ListInstances(ctx context.Context, opt ListInstancesOptions) (ListInstancesResult, error)

	// ModifyInstance changes mutable parameters. Returns immediately
	// with Status=Modifying; the change applies asynchronously.
	ModifyInstance(ctx context.Context, name string, opt ModifyInstanceOptions) error

	// RebootInstance kicks the instance. Status=Rebooting until the
	// backend reports the restart complete.
	RebootInstance(ctx context.Context, name string) error

	// CreateSnapshot takes a point-in-time backup of an instance.
	// snapshotID is caller-supplied (must be unique). Returns
	// immediately with Status=Creating on the snapshot.
	CreateSnapshot(ctx context.Context, instance, snapshotID string) (Snapshot, error)

	// DeleteSnapshot removes a snapshot.
	DeleteSnapshot(ctx context.Context, snapshotID string) error

	// DescribeSnapshot returns a snapshot's metadata.
	DescribeSnapshot(ctx context.Context, snapshotID string) (Snapshot, error)

	// ListSnapshots returns snapshots, optionally filtered by source
	// instance.
	ListSnapshots(ctx context.Context, opt ListSnapshotsOptions) (ListSnapshotsResult, error)

	// RestoreFromSnapshot provisions a new instance from a snapshot.
	// Returns immediately with the new instance in Status=Creating.
	RestoreFromSnapshot(ctx context.Context, snapshotID, newInstanceName string) (CreateInstanceResult, error)
}

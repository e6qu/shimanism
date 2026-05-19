// Package domain holds shimanism's neutral cache (Redis)
// control-plane interface and types. Same shape as Phase 5's rdbms
// domain — the shim provisions Redis instances and returns
// connection metadata; clients open direct RESP connections.
//
// See services/cache/OPERATIONS.md for design rationale.
package domain

import (
	"context"
	"time"
)

// Status is the explicit async state. Clients poll DescribeInstance
// until Status == StatusAvailable. Mirrors Phase 5's rdbms.Status
// shape since the lifecycle is identical (provisioning, modifying,
// rebooting, deleting).
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

// Instance describes a managed Redis instance's control-plane state.
type Instance struct {
	Name          string
	EngineVersion string // "7.0", "6.2", etc.
	NodeType      string // per-cloud sizing tier
	Status        Status
	Connection    Connection
	CreatedAt     time.Time
}

// Connection is what clients need to open a direct RESP connection
// once Status == Available. AuthToken is returned only at create
// time (or set by caller); subsequent DescribeInstance calls
// preserve it only on backends that store it themselves (Redis
// Operator via Kubernetes Secret). AWS / GCP / Azure surface
// "<redacted>" matching their published API.
type Connection struct {
	Host          string
	Port          int
	AuthToken     string
	EngineVersion string
}

// CreateInstanceOptions controls CreateInstance.
type CreateInstanceOptions struct {
	EngineVersion string
	NodeType      string
	// AuthToken is optional. If empty, backends that can generate
	// one will do so and return it once in the result. AWS / GCP /
	// Azure accept either caller-supplied or generated.
	AuthToken string
}

// CreateInstanceResult is the CreateInstance response.
type CreateInstanceResult struct {
	Instance Instance
	// AuthToken is the auth secret — present iff the caller didn't
	// supply one. Surfaced exactly once at create time.
	AuthToken string
}

// ModifyInstanceOptions controls ModifyInstance. All fields are
// optional; only set fields are changed.
type ModifyInstanceOptions struct {
	NodeType  string
	AuthToken string
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

// Cache is the interface every cache backend implements.
type Cache interface {
	CreateInstance(ctx context.Context, name string, opt CreateInstanceOptions) (CreateInstanceResult, error)
	DeleteInstance(ctx context.Context, name string) error
	DescribeInstance(ctx context.Context, name string) (Instance, error)
	ListInstances(ctx context.Context, opt ListInstancesOptions) (ListInstancesResult, error)
	ModifyInstance(ctx context.Context, name string, opt ModifyInstanceOptions) error
	RebootInstance(ctx context.Context, name string) error
}

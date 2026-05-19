// Package domain holds shimanism's neutral functions control-plane
// interface. Same control-plane shape as Phases 5+6; HTTP as the
// data plane (clients invoke the deployed function via the returned
// URL — shim plays no role on the invocation path).
//
// **Container image only.** All four backends natively support
// container images; ZIP-package Lambda is out of intersection.
// **Events deferred.** HTTP-trigger functions only at this phase.
//
// See services/functions/OPERATIONS.md for design rationale.
package domain

import (
	"context"
	"time"
)

// Status is the explicit async lifecycle. Reused shape from Phases
// 5+6 — Creating → Available with Updating / Deleting for changes.
type Status int

const (
	StatusUnknown Status = iota
	StatusCreating
	StatusAvailable
	StatusUpdating
	StatusDeleting
)

func (s Status) String() string {
	switch s {
	case StatusCreating:
		return "creating"
	case StatusAvailable:
		return "available"
	case StatusUpdating:
		return "updating"
	case StatusDeleting:
		return "deleting"
	default:
		return "unknown"
	}
}

// Function describes a deployed function's control-plane state.
type Function struct {
	Name           string
	Image          string // container image reference
	Status         Status
	Endpoint       Endpoint
	Environment    map[string]string
	MemoryBytes    int64 // domain uses bytes; backends translate
	CPUMilliCores  int   // 0 = backend default
	TimeoutSeconds int   // 1-900; 0 = backend default
	CreatedAt      time.Time
}

// Endpoint is the HTTP URL clients invoke the function on. Populated
// once Status == Available.
type Endpoint struct {
	URL string
}

// CreateFunctionOptions controls CreateFunction.
type CreateFunctionOptions struct {
	Image          string
	Environment    map[string]string
	MemoryBytes    int64
	CPUMilliCores  int
	TimeoutSeconds int
}

// UpdateFunctionOptions controls UpdateFunction. All fields optional.
type UpdateFunctionOptions struct {
	Image          string
	Environment    map[string]string
	MemoryBytes    int64
	CPUMilliCores  int
	TimeoutSeconds int
}

// ListFunctionsOptions controls ListFunctions.
type ListFunctionsOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListFunctionsResult is the ListFunctions response.
type ListFunctionsResult struct {
	Functions []Function
	NextToken string
}

// Functions is the interface every functions backend implements.
type Functions interface {
	CreateFunction(ctx context.Context, name string, opt CreateFunctionOptions) (Function, error)
	DeleteFunction(ctx context.Context, name string) error
	DescribeFunction(ctx context.Context, name string) (Function, error)
	ListFunctions(ctx context.Context, opt ListFunctionsOptions) (ListFunctionsResult, error)
	UpdateFunction(ctx context.Context, name string, opt UpdateFunctionOptions) error
}

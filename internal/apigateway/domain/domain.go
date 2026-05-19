// Package domain holds shimanism's neutral API Gateway
// control-plane interface and types.
//
// **Declarative-replace.** `DeployGateway(spec)` atomically swaps
// the routing table. Each backend implements "atomically"
// differently but the visible behaviour is consistent — all-or-
// nothing route swap.
//
// See services/apigateway/OPERATIONS.md for design rationale.
package domain

import (
	"context"
	"time"
)

// Status is the explicit async lifecycle. Reused shape from
// Phases 5-7 (Creating / Available / Updating / Deleting).
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

// Gateway describes a deployed API Gateway's control-plane state.
type Gateway struct {
	Name      string
	Status    Status
	Endpoint  Endpoint
	Routes    []Route
	CreatedAt time.Time
}

// Endpoint is the base URL clients prefix HTTP requests with.
// Populated once Status == Available.
type Endpoint struct {
	URL string
}

// Route is a single dispatch rule. First-match-wins.
type Route struct {
	// Method is "GET" / "POST" / "PUT" / "DELETE" / "PATCH" / "ANY".
	Method string
	// Path is the route path template — "/users/{id}" with {var}
	// segments allowed. Per-cloud syntax may vary; backends
	// translate (e.g. AWS uses {proxy+}, Envoy uses regex).
	Path string
	// Backend is the upstream HTTPS URL the gateway proxies to.
	Backend string
	// ID is an optional stable identifier the frontend assigns so a
	// caller can update / delete a single route without rewriting
	// the whole table. Backends ignore the ID; it round-trips
	// through DescribeGateway so the frontend can correlate.
	ID string
}

// CreateGatewayOptions controls CreateGateway. Routes can be empty
// at create time; DeployGateway is what publishes the routing
// table.
type CreateGatewayOptions struct {
	Routes []Route
}

// DeployGatewayOptions controls DeployGateway. The Routes slice
// replaces the gateway's current routing table atomically.
type DeployGatewayOptions struct {
	Routes []Route
}

// ListGatewaysOptions controls ListGateways.
type ListGatewaysOptions struct {
	Prefix     string
	MaxResults int
	NextToken  string
}

// ListGatewaysResult is the ListGateways response.
type ListGatewaysResult struct {
	Gateways  []Gateway
	NextToken string
}

// APIGateway is the interface every API Gateway backend implements.
type APIGateway interface {
	// CreateGateway creates a new gateway. May include an initial
	// route set; DeployGateway is the canonical way to publish
	// routes after creation.
	CreateGateway(ctx context.Context, name string, opt CreateGatewayOptions) (Gateway, error)

	// DeleteGateway removes the gateway and its routes.
	DeleteGateway(ctx context.Context, name string) error

	// DescribeGateway returns the current state including the
	// Endpoint URL (once Status == Available) and the active
	// routing table.
	DescribeGateway(ctx context.Context, name string) (Gateway, error)

	// ListGateways returns gateways, optionally filtered by name
	// prefix.
	ListGateways(ctx context.Context, opt ListGatewaysOptions) (ListGatewaysResult, error)

	// DeployGateway atomically swaps the routing table to the
	// supplied Routes slice. Returns once the swap is accepted by
	// the backend; the actual data-plane propagation may be async
	// (status flips through Updating).
	DeployGateway(ctx context.Context, name string, opt DeployGatewayOptions) error
}

// Package domain holds shimanism's neutral load-balancer interface and
// types. It is the lingua franca between the three frontends (AWS ELBv2,
// GCP Compute LB, Azure Network LB) and the four backends (AWS / GCP /
// Azure / K8s) plus the inmem testing backend.
//
// Phase 16.D covers layer-4 TCP load balancers only (N27: L7/HTTPS is
// out of intersection). Operations covered:
//
//   - LoadBalancer lifecycle (create / delete / describe)
//   - TargetGroup lifecycle (create / delete / describe)
//   - Target registration (register / deregister)
//   - Listener lifecycle (create / delete / describe)
//
// All backends implement the LoadBalancers interface.
package domain

import (
	"context"
	"errors"
)

// ──────────────────────────────────────────────
// LoadBalancer
// ──────────────────────────────────────────────

// Protocol is the listener / target group protocol.
type Protocol string

const (
	ProtocolTCP Protocol = "TCP"
	ProtocolUDP Protocol = "UDP"
)

// LoadBalancerType distinguishes network (layer-4) from application
// (layer-7). Only Network is in the intersection per N27.
type LoadBalancerType string

const (
	LoadBalancerTypeNetwork     LoadBalancerType = "network"
	LoadBalancerTypeApplication LoadBalancerType = "application" // out-of-intersection
)

// LoadBalancerState mirrors ELBv2 LB states.
type LoadBalancerState string

const (
	LoadBalancerStateActive       LoadBalancerState = "active"
	LoadBalancerStateProvisioning LoadBalancerState = "provisioning"
)

// LoadBalancer is the neutral representation of a load balancer.
type LoadBalancer struct {
	ID      string // opaque backend ID
	Name    string
	Type    LoadBalancerType
	DNSName string // backend-assigned DNS name
	State   LoadBalancerState
	VpcID   string // optional parent network ID
	Tags    map[string]string
}

// CreateLoadBalancerOptions carries inputs for CreateLoadBalancer.
type CreateLoadBalancerOptions struct {
	Type  LoadBalancerType // defaults to Network
	VpcID string
	Tags  map[string]string
}

// ListLoadBalancersOptions carries optional filters.
type ListLoadBalancersOptions struct {
	IDs   []string
	Names []string
}

// ListLoadBalancersResult is the paginated result.
type ListLoadBalancersResult struct {
	LoadBalancers []LoadBalancer
	NextToken     string
}

// ──────────────────────────────────────────────
// TargetGroup
// ──────────────────────────────────────────────

// Target is a registered backend target (instance ID or IP + port).
type Target struct {
	ID   string // instance ID or IP address
	Port int
}

// TargetGroup is the neutral representation of a target group.
type TargetGroup struct {
	ID       string
	Name     string
	Protocol Protocol
	Port     int
	VpcID    string
	Targets  []Target
	Tags     map[string]string
}

// CreateTargetGroupOptions carries inputs for CreateTargetGroup.
type CreateTargetGroupOptions struct {
	Protocol Protocol // defaults to TCP
	Port     int
	VpcID    string
	Tags     map[string]string
}

// ListTargetGroupsOptions carries optional filters.
type ListTargetGroupsOptions struct {
	IDs []string
}

// ListTargetGroupsResult is the paginated result.
type ListTargetGroupsResult struct {
	TargetGroups []TargetGroup
	NextToken    string
}

// ──────────────────────────────────────────────
// Listener
// ──────────────────────────────────────────────

// Listener is the neutral representation of a load balancer listener.
type Listener struct {
	ID             string
	LoadBalancerID string
	Protocol       Protocol
	Port           int
	TargetGroupID  string
	Tags           map[string]string
}

// CreateListenerOptions carries inputs for CreateListener.
type CreateListenerOptions struct {
	LoadBalancerID string // required
	Protocol       Protocol
	Port           int
	TargetGroupID  string
	Tags           map[string]string
}

// ListListenersOptions carries optional filters.
type ListListenersOptions struct {
	LoadBalancerID string
	IDs            []string
}

// ListListenersResult is the paginated result.
type ListListenersResult struct {
	Listeners []Listener
	NextToken string
}

// ──────────────────────────────────────────────
// LoadBalancers interface
// ──────────────────────────────────────────────

// LoadBalancers is the backend contract for layer-4 TCP load balancers.
type LoadBalancers interface {
	// LoadBalancer lifecycle
	CreateLoadBalancer(ctx context.Context, name string, opt CreateLoadBalancerOptions) (LoadBalancer, error)
	GetLoadBalancer(ctx context.Context, id string) (LoadBalancer, error)
	ListLoadBalancers(ctx context.Context, opt ListLoadBalancersOptions) (ListLoadBalancersResult, error)
	DeleteLoadBalancer(ctx context.Context, id string) error

	// TargetGroup lifecycle
	CreateTargetGroup(ctx context.Context, name string, opt CreateTargetGroupOptions) (TargetGroup, error)
	GetTargetGroup(ctx context.Context, id string) (TargetGroup, error)
	ListTargetGroups(ctx context.Context, opt ListTargetGroupsOptions) (ListTargetGroupsResult, error)
	DeleteTargetGroup(ctx context.Context, id string) error

	// Target registration
	RegisterTargets(ctx context.Context, targetGroupID string, targets []Target) error
	DeregisterTargets(ctx context.Context, targetGroupID string, targets []Target) error

	// Listener lifecycle
	CreateListener(ctx context.Context, opt CreateListenerOptions) (Listener, error)
	GetListener(ctx context.Context, id string) (Listener, error)
	ListListeners(ctx context.Context, opt ListListenersOptions) (ListListenersResult, error)
	DeleteListener(ctx context.Context, id string) error
}

// ──────────────────────────────────────────────
// Domain errors
// ──────────────────────────────────────────────

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrNotSupported  = errors.New("operation not supported on this backend")
)

func IsNotFound(err error) bool      { return errors.Is(err, ErrNotFound) }
func IsAlreadyExists(err error) bool { return errors.Is(err, ErrAlreadyExists) }
func IsNotSupported(err error) bool  { return errors.Is(err, ErrNotSupported) }

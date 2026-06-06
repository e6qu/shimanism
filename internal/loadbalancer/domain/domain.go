// Package domain holds shimanism's neutral load-balancer interface and
// types. It is the lingua franca between the three frontends (AWS ELBv2,
// GCP Compute LB, Azure Network LB / Application Gateway) and the four
// backends (AWS / GCP / Azure / K8s) plus the inmem testing backend.
//
// Phase 16.D covers layer-4 TCP load balancers (N27).
// Phase 21 extends to layer-7 HTTP/HTTPS routing (N35):
//
//   - LoadBalancer lifecycle (create / delete / describe) — type=application
//   - TargetGroup lifecycle — protocol=HTTP, HTTP health check
//   - Listener lifecycle — protocol=HTTPS, CertificateIDs
//   - Rule lifecycle (create / delete / list / get) — host/path conditions
//   - Target registration (register / deregister)
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
	ProtocolTCP   Protocol = "TCP"
	ProtocolUDP   Protocol = "UDP"
	ProtocolHTTP  Protocol = "HTTP"
	ProtocolHTTPS Protocol = "HTTPS"
)

// LoadBalancerType distinguishes network (layer-4) from application
// (layer-7). Only Network is in the intersection per N27.
type LoadBalancerType string

const (
	LoadBalancerTypeNetwork     LoadBalancerType = "network"
	LoadBalancerTypeApplication LoadBalancerType = "application" // Phase 21 in-intersection
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
// HealthCheck (L7)
// ──────────────────────────────────────────────

// HealthCheck is an HTTP/HTTPS health-check configuration (N35).
// Used by TargetGroups with Protocol=HTTP or Protocol=HTTPS.
type HealthCheck struct {
	Protocol  Protocol // HTTP or HTTPS; empty means inherit from TG protocol
	Path      string   // default "/"
	Port      string   // "traffic-port" or a numeric port; empty = traffic-port
	HTTPCodes string   // e.g. "200" or "200-299"; empty = "200"
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
	ID          string
	Name        string
	Protocol    Protocol
	Port        int
	VpcID       string
	Targets     []Target
	HealthCheck HealthCheck // populated for HTTP/HTTPS protocol groups
	Tags        map[string]string
}

// CreateTargetGroupOptions carries inputs for CreateTargetGroup.
type CreateTargetGroupOptions struct {
	Protocol    Protocol // defaults to TCP
	Port        int
	VpcID       string
	HealthCheck HealthCheck
	Tags        map[string]string
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
	TargetGroupID  string   // default action target group
	CertificateIDs []string // for HTTPS listeners (opaque cert resource IDs)
	Tags           map[string]string
}

// CreateListenerOptions carries inputs for CreateListener.
type CreateListenerOptions struct {
	LoadBalancerID string // required
	Protocol       Protocol
	Port           int
	TargetGroupID  string
	CertificateIDs []string // for HTTPS listeners
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
// Rule (L7)
// ──────────────────────────────────────────────

// RuleConditionType classifies a routing condition.
type RuleConditionType string

const (
	RuleConditionHostHeader  RuleConditionType = "host-header"
	RuleConditionPathPattern RuleConditionType = "path-pattern"
)

// RuleCondition is a single matching condition in a routing rule.
type RuleCondition struct {
	Type   RuleConditionType
	Values []string
}

// RuleAction is the forwarding action of a routing rule.
// Only forward (to a TargetGroup) is in-intersection per N35.
type RuleAction struct {
	TargetGroupID string
}

// Rule is an L7 routing rule scoped to a Listener (N35).
type Rule struct {
	ID         string
	ListenerID string
	Priority   int // lower number = higher precedence
	Conditions []RuleCondition
	Action     RuleAction
	Tags       map[string]string
}

// CreateRuleOptions carries inputs for CreateRule.
type CreateRuleOptions struct {
	ListenerID string // required
	Priority   int
	Conditions []RuleCondition
	Action     RuleAction
	Tags       map[string]string
}

// ListRulesOptions carries optional filters for ListRules.
type ListRulesOptions struct {
	ListenerID string // filter by listener
	IDs        []string
}

// ListRulesResult is the paginated result of ListRules.
type ListRulesResult struct {
	Rules     []Rule
	NextToken string
}

// UpdateTargetGroupOptions carries mutable TargetGroup fields (Phase 21).
type UpdateTargetGroupOptions struct {
	HealthCheck HealthCheck
}

// UpdateListenerOptions carries mutable Listener fields (Phase 21).
type UpdateListenerOptions struct {
	Protocol       Protocol
	Port           int
	TargetGroupID  string
	CertificateIDs []string
}

// UpdateRuleOptions carries mutable Rule fields (Phase 21).
type UpdateRuleOptions struct {
	Conditions []RuleCondition
	Action     RuleAction
}

// RulePriorityPair associates a Rule ID with a new priority (Phase 21).
type RulePriorityPair struct {
	ID       string
	Priority int
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

	// Rule lifecycle (L7 only; N35)
	CreateRule(ctx context.Context, opt CreateRuleOptions) (Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	ListRules(ctx context.Context, opt ListRulesOptions) (ListRulesResult, error)
	DeleteRule(ctx context.Context, id string) error

	// Modify operations (L7 only; N35)
	UpdateTargetGroup(ctx context.Context, id string, opt UpdateTargetGroupOptions) (TargetGroup, error)
	UpdateListener(ctx context.Context, id string, opt UpdateListenerOptions) (Listener, error)
	UpdateRule(ctx context.Context, id string, opt UpdateRuleOptions) (Rule, error)
	SetRulePriorities(ctx context.Context, pairs []RulePriorityPair) ([]Rule, error)
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

// Package domain holds shimanism's neutral compute + networking
// interface and types. It is the lingua franca between the three
// frontends (AWS EC2, GCP Compute Engine, Azure Compute/Network) and
// the four backends (AWS / GCP / Azure / K8s) plus the inmem testing
// backend.
//
// This file covers Phase 16.B networking primitives: networks (VPCs /
// VNets), subnets, security groups (SGs / Firewalls / NSGs), and
// public IPs (Elastic IPs / External Addresses / Azure Public IPs).
//
// Phase 16.C (instances.go) covers compute instance lifecycle.
//
// Normalization rules applied here:
//   - N21: security group semantics — allow-only stateful intersection.
//   - N22: public IP two-step model (allocate + associate).
//   - N25: VPC CIDR is optional at the network level (GCP ignores it).
//   - N26: subnet zone is optional (GCP ignores it; K8s = NotImplemented).
//
// **Stateless.** No per-process maps. All state lives in the
// destination backend.
package domain

import (
	"context"
	"errors"
)

// ──────────────────────────────────────────────
// Network (VPC / VNet / GCP Network)
// ──────────────────────────────────────────────

// Network is the neutral representation of a VPC/VNet/GCP-network.
type Network struct {
	// ID is the destination-cloud-native resource identifier. Opaque
	// to frontends; round-tripped for create → describe paths.
	ID   string
	Name string
	// CIDR is the primary IPv4 CIDR block. Required by AWS + Azure;
	// not meaningful at the GCP network level (CIDRs live on
	// subnetworks). May be empty when the backend doesn't use it.
	CIDR string
	Tags map[string]string
}

// CreateNetworkOptions carries inputs for CreateNetwork.
type CreateNetworkOptions struct {
	CIDR string            // optional for GCP (N25); required for AWS / Azure
	Tags map[string]string // optional
}

// ListNetworksOptions carries optional filters for ListNetworks.
type ListNetworksOptions struct {
	// IDs filters the list to the given network IDs. Empty = all.
	IDs []string
}

// ListNetworksResult is the paginated result of ListNetworks.
type ListNetworksResult struct {
	Networks  []Network
	NextToken string // empty when there are no more pages
}

// ──────────────────────────────────────────────
// Subnet
// ──────────────────────────────────────────────

// Subnet is the neutral representation of a subnet / subnetwork.
type Subnet struct {
	ID        string
	Name      string
	NetworkID string // parent network
	CIDR      string
	// Zone is the availability zone (AWS) or region (GCP) or empty
	// (Azure). Per N26: optional at domain level.
	Zone string
	Tags map[string]string
}

// CreateSubnetOptions carries inputs for CreateSubnet.
type CreateSubnetOptions struct {
	NetworkID string // required: parent network
	CIDR      string // required
	// Zone is the AZ for AWS subnets; ignored by GCP (regional) and
	// Azure (VNet-scoped). Per N26.
	Zone string
	Tags map[string]string
}

// ListSubnetsOptions carries optional filters for ListSubnets.
type ListSubnetsOptions struct {
	NetworkID string // filter by parent network (empty = all)
	IDs       []string
}

// ListSubnetsResult is the paginated result of ListSubnets.
type ListSubnetsResult struct {
	Subnets   []Subnet
	NextToken string
}

// ──────────────────────────────────────────────
// Security Group / Firewall / NSG (N21)
// ──────────────────────────────────────────────

// Protocol names for SecurityGroupRule. Use the string form that the
// source cloud's SDK accepts; backends translate.
const (
	ProtocolTCP  = "tcp"
	ProtocolUDP  = "udp"
	ProtocolICMP = "icmp"
	ProtocolAll  = "-1" // matches all protocols (AWS convention)
)

// SecurityGroupRule is an allow rule (N21: deny rules and priority
// are out of intersection). A zero PortTo means the rule covers a
// single port equal to PortFrom. Both zero means "all ports".
type SecurityGroupRule struct {
	Protocol  string // "tcp", "udp", "icmp", "-1" (all)
	PortFrom  int    // inclusive lower bound; 0 = all
	PortTo    int    // inclusive upper bound; 0 = same as PortFrom
	CIDRs     []string
	Direction RuleDirection
}

// RuleDirection indicates whether the rule governs inbound or
// outbound traffic.
type RuleDirection int

const (
	Inbound  RuleDirection = iota
	Outbound               // egress
)

// SecurityGroup is the neutral representation of an SG / Firewall /
// NSG.
type SecurityGroup struct {
	ID          string
	Name        string
	NetworkID   string // parent network; may be empty for Azure default NSGs
	Description string
	Rules       []SecurityGroupRule
	Tags        map[string]string
}

// CreateSecurityGroupOptions carries inputs for CreateSecurityGroup.
type CreateSecurityGroupOptions struct {
	NetworkID   string
	Description string
	Tags        map[string]string
}

// ListSecurityGroupsOptions carries optional filters.
type ListSecurityGroupsOptions struct {
	NetworkID string
	IDs       []string
}

// ListSecurityGroupsResult is the paginated result.
type ListSecurityGroupsResult struct {
	SecurityGroups []SecurityGroup
	NextToken      string
}

// ──────────────────────────────────────────────
// Public IP (N22)
// ──────────────────────────────────────────────

// PublicIP is the neutral representation of an Elastic IP /
// GCP External Address / Azure Public IP Address.
type PublicIP struct {
	ID         string
	Name       string
	Address    string // the allocated IP address (v4)
	Region     string
	InstanceID string // non-empty when associated with an instance
	Tags       map[string]string
}

// AllocatePublicIPOptions carries inputs for AllocatePublicIP.
type AllocatePublicIPOptions struct {
	// Name is the caller-specified resource name. Azure uses this directly;
	// AWS and GCP ignore it (IDs are system-assigned).
	Name   string
	Region string // required for GCP / Azure; optional for AWS
	Tags   map[string]string
}

// ListPublicIPsOptions carries optional filters.
type ListPublicIPsOptions struct {
	IDs []string
}

// ListPublicIPsResult is the paginated result.
type ListPublicIPsResult struct {
	PublicIPs []PublicIP
	NextToken string
}

// ──────────────────────────────────────────────
// Networking interface
// ──────────────────────────────────────────────

// Networking is the backend contract for VPC networking primitives.
// Every cloud backend and the inmem / K8s backends implement this.
type Networking interface {
	// Network lifecycle
	CreateNetwork(ctx context.Context, name string, opt CreateNetworkOptions) (Network, error)
	GetNetwork(ctx context.Context, id string) (Network, error)
	ListNetworks(ctx context.Context, opt ListNetworksOptions) (ListNetworksResult, error)
	DeleteNetwork(ctx context.Context, id string) error

	// Subnet lifecycle
	CreateSubnet(ctx context.Context, name string, opt CreateSubnetOptions) (Subnet, error)
	GetSubnet(ctx context.Context, id string) (Subnet, error)
	ListSubnets(ctx context.Context, opt ListSubnetsOptions) (ListSubnetsResult, error)
	DeleteSubnet(ctx context.Context, id string) error

	// Security group lifecycle
	CreateSecurityGroup(ctx context.Context, name string, opt CreateSecurityGroupOptions) (SecurityGroup, error)
	GetSecurityGroup(ctx context.Context, id string) (SecurityGroup, error)
	ListSecurityGroups(ctx context.Context, opt ListSecurityGroupsOptions) (ListSecurityGroupsResult, error)
	DeleteSecurityGroup(ctx context.Context, id string) error
	AddRule(ctx context.Context, sgID string, rule SecurityGroupRule) error
	RemoveRule(ctx context.Context, sgID string, rule SecurityGroupRule) error

	// Public IP lifecycle (N22)
	AllocatePublicIP(ctx context.Context, opt AllocatePublicIPOptions) (PublicIP, error)
	AssociatePublicIP(ctx context.Context, ipID, instanceID string) error
	DisassociatePublicIP(ctx context.Context, ipID string) error
	ReleasePublicIP(ctx context.Context, ipID string) error
	ListPublicIPs(ctx context.Context, opt ListPublicIPsOptions) (ListPublicIPsResult, error)
}

// ──────────────────────────────────────────────
// Domain errors
// ──────────────────────────────────────────────

// ErrNotFound is returned when a resource doesn't exist.
var ErrNotFound = errors.New("not found")

// ErrAlreadyExists is returned when a resource with that name/ID
// already exists.
var ErrAlreadyExists = errors.New("already exists")

// ErrInvalidInput is returned when request parameters are invalid.
var ErrInvalidInput = errors.New("invalid input")

// ErrNotSupported is returned for operations that are out of
// intersection on a particular backend (e.g. AllocatePublicIP on
// K8s).
var ErrNotSupported = errors.New("operation not supported on this backend")

// IsNotFound reports whether err wraps ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsAlreadyExists reports whether err wraps ErrAlreadyExists.
func IsAlreadyExists(err error) bool { return errors.Is(err, ErrAlreadyExists) }

// IsNotSupported reports whether err wraps ErrNotSupported.
func IsNotSupported(err error) bool { return errors.Is(err, ErrNotSupported) }

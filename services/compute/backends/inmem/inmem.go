// Package inmem provides an in-memory networking backend for tests.
// All state lives in maps guarded by a single RWMutex; no external
// dependencies. Mirrors the destination cloud contract: networks +
// subnets + security groups + public IPs stored in maps, deterministic
// list ordering (by name) so test assertions stay stable.
package inmem

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Backend implements domain.Networking entirely in memory.
type Backend struct {
	mu sync.RWMutex

	networks map[string]*domain.Network
	subnets  map[string]*domain.Subnet
	sgs      map[string]*domain.SecurityGroup
	ips      map[string]*domain.PublicIP

	// ID sequence counters — monotonic, collision-free within a
	// single Backend instance.
	netSeq int
	subSeq int
	sgSeq  int
	ipSeq  int
}

// New returns an empty in-memory networking backend.
func New() *Backend {
	return &Backend{
		networks: map[string]*domain.Network{},
		subnets:  map[string]*domain.Subnet{},
		sgs:      map[string]*domain.SecurityGroup{},
		ips:      map[string]*domain.PublicIP{},
	}
}

var _ domain.Networking = (*Backend)(nil)

func (b *Backend) nextID(prefix string, seq *int) string {
	*seq++
	return fmt.Sprintf("%s-%08d", prefix, *seq)
}

func copyTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ─── Networks ───────────────────────────────────────────────────────

func (b *Backend) CreateNetwork(_ context.Context, name string, opt domain.CreateNetworkOptions) (domain.Network, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, n := range b.networks {
		if n.Name == name {
			return domain.Network{}, fmt.Errorf("network %q: %w", name, domain.ErrAlreadyExists)
		}
	}
	n := &domain.Network{
		ID:   b.nextID("net", &b.netSeq),
		Name: name,
		CIDR: opt.CIDR,
		Tags: copyTags(opt.Tags),
	}
	b.networks[n.ID] = n
	return *n, nil
}

func (b *Backend) GetNetwork(_ context.Context, id string) (domain.Network, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	n, ok := b.networks[id]
	if !ok {
		return domain.Network{}, fmt.Errorf("network %q: %w", id, domain.ErrNotFound)
	}
	return *n, nil
}

func (b *Backend) ListNetworks(_ context.Context, opt domain.ListNetworksOptions) (domain.ListNetworksResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	want := map[string]bool{}
	for _, id := range opt.IDs {
		want[id] = true
	}
	var out []domain.Network
	for _, n := range b.networks {
		if len(want) > 0 && !want[n.ID] {
			continue
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return domain.ListNetworksResult{Networks: out}, nil
}

func (b *Backend) DeleteNetwork(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.networks[id]; !ok {
		return fmt.Errorf("network %q: %w", id, domain.ErrNotFound)
	}
	delete(b.networks, id)
	return nil
}

// ─── Subnets ────────────────────────────────────────────────────────

func (b *Backend) CreateSubnet(_ context.Context, name string, opt domain.CreateSubnetOptions) (domain.Subnet, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if opt.NetworkID == "" {
		return domain.Subnet{}, fmt.Errorf("NetworkID is required: %w", domain.ErrInvalidInput)
	}
	if _, ok := b.networks[opt.NetworkID]; !ok {
		return domain.Subnet{}, fmt.Errorf("parent network %q: %w", opt.NetworkID, domain.ErrNotFound)
	}
	for _, s := range b.subnets {
		if s.NetworkID == opt.NetworkID && s.Name == name {
			return domain.Subnet{}, fmt.Errorf("subnet %q: %w", name, domain.ErrAlreadyExists)
		}
	}
	s := &domain.Subnet{
		ID:        b.nextID("subnet", &b.subSeq),
		Name:      name,
		NetworkID: opt.NetworkID,
		CIDR:      opt.CIDR,
		Zone:      opt.Zone,
		Tags:      copyTags(opt.Tags),
	}
	b.subnets[s.ID] = s
	return *s, nil
}

func (b *Backend) GetSubnet(_ context.Context, id string) (domain.Subnet, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s, ok := b.subnets[id]
	if !ok {
		return domain.Subnet{}, fmt.Errorf("subnet %q: %w", id, domain.ErrNotFound)
	}
	return *s, nil
}

func (b *Backend) ListSubnets(_ context.Context, opt domain.ListSubnetsOptions) (domain.ListSubnetsResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	want := map[string]bool{}
	for _, id := range opt.IDs {
		want[id] = true
	}
	var out []domain.Subnet
	for _, s := range b.subnets {
		if opt.NetworkID != "" && s.NetworkID != opt.NetworkID {
			continue
		}
		if len(want) > 0 && !want[s.ID] {
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return domain.ListSubnetsResult{Subnets: out}, nil
}

func (b *Backend) DeleteSubnet(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subnets[id]; !ok {
		return fmt.Errorf("subnet %q: %w", id, domain.ErrNotFound)
	}
	delete(b.subnets, id)
	return nil
}

// ─── Security Groups ────────────────────────────────────────────────

func (b *Backend) CreateSecurityGroup(_ context.Context, name string, opt domain.CreateSecurityGroupOptions) (domain.SecurityGroup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sg := range b.sgs {
		if sg.NetworkID == opt.NetworkID && sg.Name == name {
			return domain.SecurityGroup{}, fmt.Errorf("security group %q: %w", name, domain.ErrAlreadyExists)
		}
	}
	sg := &domain.SecurityGroup{
		ID:          b.nextID("sg", &b.sgSeq),
		Name:        name,
		NetworkID:   opt.NetworkID,
		Description: opt.Description,
		Tags:        copyTags(opt.Tags),
	}
	b.sgs[sg.ID] = sg
	return *sg, nil
}

func (b *Backend) GetSecurityGroup(_ context.Context, id string) (domain.SecurityGroup, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sg, ok := b.sgs[id]
	if !ok {
		return domain.SecurityGroup{}, fmt.Errorf("security group %q: %w", id, domain.ErrNotFound)
	}
	return *sg, nil
}

func (b *Backend) ListSecurityGroups(_ context.Context, opt domain.ListSecurityGroupsOptions) (domain.ListSecurityGroupsResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	want := map[string]bool{}
	for _, id := range opt.IDs {
		want[id] = true
	}
	var out []domain.SecurityGroup
	for _, sg := range b.sgs {
		if opt.NetworkID != "" && sg.NetworkID != opt.NetworkID {
			continue
		}
		if len(want) > 0 && !want[sg.ID] {
			continue
		}
		out = append(out, *sg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return domain.ListSecurityGroupsResult{SecurityGroups: out}, nil
}

func (b *Backend) DeleteSecurityGroup(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.sgs[id]; !ok {
		return fmt.Errorf("security group %q: %w", id, domain.ErrNotFound)
	}
	delete(b.sgs, id)
	return nil
}

func (b *Backend) AddRule(_ context.Context, sgID string, rule domain.SecurityGroupRule) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sg, ok := b.sgs[sgID]
	if !ok {
		return fmt.Errorf("security group %q: %w", sgID, domain.ErrNotFound)
	}
	sg.Rules = append(sg.Rules, rule)
	return nil
}

func (b *Backend) RemoveRule(_ context.Context, sgID string, rule domain.SecurityGroupRule) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sg, ok := b.sgs[sgID]
	if !ok {
		return fmt.Errorf("security group %q: %w", sgID, domain.ErrNotFound)
	}
	var kept []domain.SecurityGroupRule
	for _, r := range sg.Rules {
		if !rulesEqual(r, rule) {
			kept = append(kept, r)
		}
	}
	sg.Rules = kept
	return nil
}

func rulesEqual(a, b domain.SecurityGroupRule) bool {
	if a.Protocol != b.Protocol || a.PortFrom != b.PortFrom || a.PortTo != b.PortTo || a.Direction != b.Direction {
		return false
	}
	if len(a.CIDRs) != len(b.CIDRs) {
		return false
	}
	aSet := map[string]bool{}
	for _, c := range a.CIDRs {
		aSet[c] = true
	}
	for _, c := range b.CIDRs {
		if !aSet[c] {
			return false
		}
	}
	return true
}

// ─── Public IPs ─────────────────────────────────────────────────────

func (b *Backend) AllocatePublicIP(_ context.Context, opt domain.AllocatePublicIPOptions) (domain.PublicIP, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ipSeq++
	// Allocate a deterministic in-test address from the 203.0.113.0/24
	// documentation range (TEST-NET-3, RFC 5737).
	addr := fmt.Sprintf("203.0.113.%d", b.ipSeq%254+1)
	id := b.nextID("eip", &b.ipSeq)
	name := opt.Name
	if name == "" {
		name = fmt.Sprintf("eip-%08d", b.ipSeq)
	}
	ip := &domain.PublicIP{
		ID:      id,
		Name:    name,
		Address: addr,
		Region:  opt.Region,
		Tags:    copyTags(opt.Tags),
	}
	b.ips[ip.ID] = ip
	return *ip, nil
}

func (b *Backend) AssociatePublicIP(_ context.Context, ipID, instanceID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	ip, ok := b.ips[ipID]
	if !ok {
		return fmt.Errorf("public IP %q: %w", ipID, domain.ErrNotFound)
	}
	ip.InstanceID = instanceID
	return nil
}

func (b *Backend) DisassociatePublicIP(_ context.Context, ipID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	ip, ok := b.ips[ipID]
	if !ok {
		return fmt.Errorf("public IP %q: %w", ipID, domain.ErrNotFound)
	}
	ip.InstanceID = ""
	return nil
}

func (b *Backend) ReleasePublicIP(_ context.Context, ipID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.ips[ipID]; !ok {
		return fmt.Errorf("public IP %q: %w", ipID, domain.ErrNotFound)
	}
	delete(b.ips, ipID)
	return nil
}

func (b *Backend) ListPublicIPs(_ context.Context, opt domain.ListPublicIPsOptions) (domain.ListPublicIPsResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	want := map[string]bool{}
	for _, id := range opt.IDs {
		want[id] = true
	}
	var out []domain.PublicIP
	for _, ip := range b.ips {
		if len(want) > 0 && !want[ip.ID] {
			continue
		}
		out = append(out, *ip)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return domain.ListPublicIPsResult{PublicIPs: out}, nil
}

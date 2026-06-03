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

// Backend implements domain.Networking and domain.Instances entirely
// in memory. Both interfaces are satisfied by the same struct so that
// a single AWS EC2 / GCP Compute frontend can dispatch both networking
// and instance operations to the same backend.
type Backend struct {
	mu sync.RWMutex

	networks  map[string]*domain.Network
	subnets   map[string]*domain.Subnet
	sgs       map[string]*domain.SecurityGroup
	ips       map[string]*domain.PublicIP
	instances map[string]*domain.Instance

	// ID sequence counters — monotonic, collision-free within a
	// single Backend instance.
	netSeq  int
	subSeq  int
	sgSeq   int
	ipSeq   int
	instSeq int
}

// New returns an empty in-memory compute + networking backend.
func New() *Backend {
	return &Backend{
		networks:  map[string]*domain.Network{},
		subnets:   map[string]*domain.Subnet{},
		sgs:       map[string]*domain.SecurityGroup{},
		ips:       map[string]*domain.PublicIP{},
		instances: map[string]*domain.Instance{},
	}
}

var _ domain.Networking = (*Backend)(nil)
var _ domain.Instances = (*Backend)(nil)

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

// ──────────────────────────────────────────────
// domain.Instances implementation
// ──────────────────────────────────────────────

func (b *Backend) RunInstances(_ context.Context, opt domain.RunInstancesOptions) ([]domain.Instance, error) {
	if opt.ImageID == "" {
		return nil, &domain.ValidationError{Field: "ImageID", Msg: "required"}
	}
	if opt.InstanceType == "" {
		return nil, &domain.ValidationError{Field: "InstanceType", Msg: "required"}
	}
	count := opt.MaxCount
	if count < 1 {
		count = 1
	}
	min := opt.MinCount
	if min < 1 {
		min = 1
	}
	if count < min {
		count = min
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	var launched []domain.Instance
	for i := 0; i < count; i++ {
		id := b.nextID("i", &b.instSeq)
		inst := &domain.Instance{
			ID:               id,
			Name:             opt.Tags["Name"],
			ImageID:          opt.ImageID,
			InstanceType:     opt.InstanceType,
			State:            domain.InstanceStateRunning,
			NetworkID:        opt.NetworkID,
			SubnetID:         opt.SubnetID,
			SecurityGroupIDs: append([]string(nil), opt.SecurityGroupIDs...),
			KeyName:          opt.KeyName,
			PrivateIP:        fmt.Sprintf("10.0.0.%d", b.instSeq),
			Tags:             copyTags(opt.Tags),
		}
		b.instances[id] = inst
		launched = append(launched, *inst)
	}
	return launched, nil
}

func (b *Backend) DescribeInstances(_ context.Context, opt domain.DescribeInstancesOptions) (domain.DescribeInstancesResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	wantID := map[string]bool{}
	for _, id := range opt.IDs {
		wantID[id] = true
	}
	wantState := map[domain.InstanceState]bool{}
	for _, s := range opt.States {
		wantState[s] = true
	}
	var out []domain.Instance
	for _, inst := range b.instances {
		if len(wantID) > 0 && !wantID[inst.ID] {
			continue
		}
		if len(wantState) > 0 {
			if !wantState[inst.State] {
				continue
			}
		} else if len(wantID) == 0 && inst.State == domain.InstanceStateTerminated {
			// Default list-all (no ID or state filter): exclude terminated,
			// mirroring AWS DescribeInstances default behavior. When a
			// specific ID is given, terminated instances are returned so
			// that callers (e.g. the Terraform destroy waiter) can observe
			// the terminal "terminated" state.
			continue
		}
		out = append(out, *inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return domain.DescribeInstancesResult{Instances: out}, nil
}

func (b *Backend) StartInstances(_ context.Context, ids []string) ([]domain.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.Instance
	for _, id := range ids {
		inst, ok := b.instances[id]
		if !ok {
			return nil, domain.ErrNotFound
		}
		inst.State = domain.InstanceStateRunning
		out = append(out, *inst)
	}
	return out, nil
}

func (b *Backend) StopInstances(_ context.Context, ids []string) ([]domain.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.Instance
	for _, id := range ids {
		inst, ok := b.instances[id]
		if !ok {
			return nil, domain.ErrNotFound
		}
		inst.State = domain.InstanceStateStopped
		out = append(out, *inst)
	}
	return out, nil
}

func (b *Backend) TerminateInstances(_ context.Context, ids []string) ([]domain.Instance, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []domain.Instance
	for _, id := range ids {
		inst, ok := b.instances[id]
		if !ok {
			return nil, domain.ErrNotFound
		}
		inst.State = domain.InstanceStateTerminated
		// Keep instance in map so subsequent DescribeInstances calls return
		// state="terminated" (AWS provider waiter polls for this exact state
		// before considering destroy complete).
		out = append(out, *inst)
	}
	return out, nil
}

func (b *Backend) RebootInstances(_ context.Context, ids []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, id := range ids {
		if _, ok := b.instances[id]; !ok {
			return domain.ErrNotFound
		}
		// Reboot leaves state as Running (no-op in inmem).
	}
	return nil
}

// wellKnownInstanceTypes is the inmem catalogue of known instance types.
// Real backends return the backend cloud's catalogue; this is for tests.
var wellKnownInstanceTypes = []domain.InstanceTypeInfo{
	{InstanceType: "t3.micro", VCPUs: 2, MemoryMiB: 1024},
	{InstanceType: "t3.small", VCPUs: 2, MemoryMiB: 2048},
	{InstanceType: "t3.medium", VCPUs: 2, MemoryMiB: 4096},
	{InstanceType: "m5.large", VCPUs: 2, MemoryMiB: 8192},
	{InstanceType: "m5.xlarge", VCPUs: 4, MemoryMiB: 16384},
}

func (b *Backend) DescribeInstanceTypes(_ context.Context, opt domain.DescribeInstanceTypesOptions) (domain.DescribeInstanceTypesResult, error) {
	want := map[string]bool{}
	for _, t := range opt.InstanceTypes {
		want[t] = true
	}
	var out []domain.InstanceTypeInfo
	for _, t := range wellKnownInstanceTypes {
		if len(want) > 0 && !want[t.InstanceType] {
			continue
		}
		out = append(out, t)
	}
	return domain.DescribeInstanceTypesResult{InstanceTypes: out}, nil
}

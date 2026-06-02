// Package azure is the Azure Network passthrough backend for
// shimanism's compute service (Phase 16.B networking primitives). It
// uses armnetwork/v6 to drive real Azure Network (VNets, Subnets, NSGs,
// Public IPs) or a sockerless-pointed client for tests.
//
// Domain IDs map to Azure resource names. No shim-side ID tables —
// lookups re-read Azure on every request (stateless).
package azure

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Options carries ARM client configuration.
type Options struct {
	SubscriptionID string
	ResourceGroup  string
	ClientOptions  *arm.ClientOptions
}

// Backend implements domain.Networking via Azure Network ARM.
type Backend struct {
	sub  string
	rg   string
	vnc  *armnetwork.VirtualNetworksClient
	snc  *armnetwork.SubnetsClient
	nsgc *armnetwork.SecurityGroupsClient
	src  *armnetwork.SecurityRulesClient
	pipc *armnetwork.PublicIPAddressesClient
}

// New creates a Backend from a credential + options.
func New(cred azcore.TokenCredential, opt Options) (*Backend, error) {
	co := opt.ClientOptions
	vnc, err := armnetwork.NewVirtualNetworksClient(opt.SubscriptionID, cred, co)
	if err != nil {
		return nil, fmt.Errorf("VirtualNetworks client: %w", err)
	}
	snc, err := armnetwork.NewSubnetsClient(opt.SubscriptionID, cred, co)
	if err != nil {
		return nil, fmt.Errorf("subnets client: %w", err)
	}
	nsgc, err := armnetwork.NewSecurityGroupsClient(opt.SubscriptionID, cred, co)
	if err != nil {
		return nil, fmt.Errorf("SecurityGroups client: %w", err)
	}
	src, err := armnetwork.NewSecurityRulesClient(opt.SubscriptionID, cred, co)
	if err != nil {
		return nil, fmt.Errorf("SecurityRules client: %w", err)
	}
	pipc, err := armnetwork.NewPublicIPAddressesClient(opt.SubscriptionID, cred, co)
	if err != nil {
		return nil, fmt.Errorf("PublicIPAddresses client: %w", err)
	}
	return &Backend{sub: opt.SubscriptionID, rg: opt.ResourceGroup,
		vnc: vnc, snc: snc, nsgc: nsgc, src: src, pipc: pipc}, nil
}

var _ domain.Networking = (*Backend)(nil)

// ─── Networks (VNets) ─────────────────────────────────────────────────

func (b *Backend) CreateNetwork(ctx context.Context, name string, opt domain.CreateNetworkOptions) (domain.Network, error) {
	var addressPrefixes []*string
	if opt.CIDR != "" {
		addressPrefixes = []*string{to.Ptr(opt.CIDR)}
	}
	poller, err := b.vnc.BeginCreateOrUpdate(ctx, b.rg, name, armnetwork.VirtualNetwork{
		Location: to.Ptr("eastus"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			AddressSpace: &armnetwork.AddressSpace{AddressPrefixes: addressPrefixes},
		},
		Tags: domainTagsToAzure(opt.Tags),
	}, nil)
	if err != nil {
		return domain.Network{}, mapAzureErr(err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return domain.Network{}, mapAzureErr(err)
	}
	return azureVNetToDomain(&res.VirtualNetwork), nil
}

func (b *Backend) GetNetwork(ctx context.Context, id string) (domain.Network, error) {
	res, err := b.vnc.Get(ctx, b.rg, id, nil)
	if err != nil {
		return domain.Network{}, mapAzureErr(err)
	}
	return azureVNetToDomain(&res.VirtualNetwork), nil
}

func (b *Backend) ListNetworks(ctx context.Context, opt domain.ListNetworksOptions) (domain.ListNetworksResult, error) {
	pager := b.vnc.NewListPager(b.rg, nil)
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var nets []domain.Network
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListNetworksResult{}, mapAzureErr(err)
		}
		for _, v := range page.Value {
			n := azureVNetToDomain(v)
			if len(idSet) > 0 && !idSet[n.ID] {
				continue
			}
			nets = append(nets, n)
		}
	}
	return domain.ListNetworksResult{Networks: nets}, nil
}

func (b *Backend) DeleteNetwork(ctx context.Context, id string) error {
	poller, err := b.vnc.BeginDelete(ctx, b.rg, id, nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

// ─── Subnets ─────────────────────────────────────────────────────────

func (b *Backend) CreateSubnet(ctx context.Context, name string, opt domain.CreateSubnetOptions) (domain.Subnet, error) {
	poller, err := b.snc.BeginCreateOrUpdate(ctx, b.rg, opt.NetworkID, name, armnetwork.Subnet{
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix: to.Ptr(opt.CIDR),
		},
	}, nil)
	if err != nil {
		return domain.Subnet{}, mapAzureErr(err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return domain.Subnet{}, mapAzureErr(err)
	}
	return azureSubnetToDomain(&res.Subnet, opt.NetworkID), nil
}

func (b *Backend) GetSubnet(ctx context.Context, id string) (domain.Subnet, error) {
	// id encodes "vnetName/subnetName" — stateless lookup.
	parts := splitSubnetID(id)
	if len(parts) != 2 {
		return domain.Subnet{}, fmt.Errorf("invalid subnet ID %q: %w", id, domain.ErrInvalidInput)
	}
	res, err := b.snc.Get(ctx, b.rg, parts[0], parts[1], nil)
	if err != nil {
		return domain.Subnet{}, mapAzureErr(err)
	}
	return azureSubnetToDomain(&res.Subnet, parts[0]), nil
}

func (b *Backend) ListSubnets(ctx context.Context, opt domain.ListSubnetsOptions) (domain.ListSubnetsResult, error) {
	if opt.NetworkID == "" {
		return domain.ListSubnetsResult{}, nil
	}
	pager := b.snc.NewListPager(b.rg, opt.NetworkID, nil)
	var subs []domain.Subnet
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListSubnetsResult{}, mapAzureErr(err)
		}
		for _, s := range page.Value {
			subs = append(subs, azureSubnetToDomain(s, opt.NetworkID))
		}
	}
	return domain.ListSubnetsResult{Subnets: subs}, nil
}

func (b *Backend) DeleteSubnet(ctx context.Context, id string) error {
	parts := splitSubnetID(id)
	if len(parts) != 2 {
		return fmt.Errorf("invalid subnet ID %q: %w", id, domain.ErrInvalidInput)
	}
	poller, err := b.snc.BeginDelete(ctx, b.rg, parts[0], parts[1], nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

// ─── Security Groups (NSGs) ───────────────────────────────────────────

func (b *Backend) CreateSecurityGroup(ctx context.Context, name string, opt domain.CreateSecurityGroupOptions) (domain.SecurityGroup, error) {
	poller, err := b.nsgc.BeginCreateOrUpdate(ctx, b.rg, name, armnetwork.SecurityGroup{
		Location: to.Ptr("eastus"),
		Tags:     domainTagsToAzure(opt.Tags),
	}, nil)
	if err != nil {
		return domain.SecurityGroup{}, mapAzureErr(err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return domain.SecurityGroup{}, mapAzureErr(err)
	}
	return azureNSGToDomain(&res.SecurityGroup), nil
}

func (b *Backend) GetSecurityGroup(ctx context.Context, id string) (domain.SecurityGroup, error) {
	res, err := b.nsgc.Get(ctx, b.rg, id, nil)
	if err != nil {
		return domain.SecurityGroup{}, mapAzureErr(err)
	}
	return azureNSGToDomain(&res.SecurityGroup), nil
}

func (b *Backend) ListSecurityGroups(ctx context.Context, opt domain.ListSecurityGroupsOptions) (domain.ListSecurityGroupsResult, error) {
	pager := b.nsgc.NewListPager(b.rg, nil)
	var sgs []domain.SecurityGroup
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListSecurityGroupsResult{}, mapAzureErr(err)
		}
		for _, sg := range page.Value {
			d := azureNSGToDomain(sg)
			if len(idSet) > 0 && !idSet[d.ID] {
				continue
			}
			sgs = append(sgs, d)
		}
	}
	return domain.ListSecurityGroupsResult{SecurityGroups: sgs}, nil
}

func (b *Backend) DeleteSecurityGroup(ctx context.Context, id string) error {
	poller, err := b.nsgc.BeginDelete(ctx, b.rg, id, nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

func (b *Backend) AddRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	ruleName := fmt.Sprintf("shim-%s-%d", rule.Protocol, ruleHash(rule))
	priority := int32(200 + ruleHash(rule)%3800)
	azRule := domainRuleToAzureRule(rule, ruleName, priority)
	poller, err := b.src.BeginCreateOrUpdate(ctx, b.rg, sgID, ruleName, azRule, nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

func (b *Backend) RemoveRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	ruleName := fmt.Sprintf("shim-%s-%d", rule.Protocol, ruleHash(rule))
	poller, err := b.src.BeginDelete(ctx, b.rg, sgID, ruleName, nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

// ─── Public IPs ───────────────────────────────────────────────────────

func (b *Backend) AllocatePublicIP(ctx context.Context, opt domain.AllocatePublicIPOptions) (domain.PublicIP, error) {
	name := fmt.Sprintf("pip-%d", pipCounter())
	location := opt.Region
	if location == "" {
		location = "eastus"
	}
	poller, err := b.pipc.BeginCreateOrUpdate(ctx, b.rg, name, armnetwork.PublicIPAddress{
		Location: to.Ptr(location),
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(armnetwork.IPAllocationMethodStatic),
		},
		Tags: domainTagsToAzure(opt.Tags),
	}, nil)
	if err != nil {
		return domain.PublicIP{}, mapAzureErr(err)
	}
	res, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		return domain.PublicIP{}, mapAzureErr(err)
	}
	return azureIPToDomain(&res.PublicIPAddress), nil
}

func (b *Backend) AssociatePublicIP(ctx context.Context, ipID, instanceID string) error {
	// Azure IP association with a VM NIC is managed via the NIC resource,
	// not the IP resource itself. Per N22, acknowledge the call without
	// the NIC-level plumbing — the real IP stays reserved.
	return nil
}

func (b *Backend) DisassociatePublicIP(ctx context.Context, ipID string) error {
	return nil
}

func (b *Backend) ReleasePublicIP(ctx context.Context, ipID string) error {
	poller, err := b.pipc.BeginDelete(ctx, b.rg, ipID, nil)
	if err != nil {
		return mapAzureErr(err)
	}
	_, err = poller.PollUntilDone(ctx, nil)
	return mapAzureErr(err)
}

func (b *Backend) ListPublicIPs(ctx context.Context, opt domain.ListPublicIPsOptions) (domain.ListPublicIPsResult, error) {
	pager := b.pipc.NewListPager(b.rg, nil)
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var ips []domain.PublicIP
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListPublicIPsResult{}, mapAzureErr(err)
		}
		for _, p := range page.Value {
			ip := azureIPToDomain(p)
			if len(idSet) > 0 && !idSet[ip.ID] {
				continue
			}
			ips = append(ips, ip)
		}
	}
	return domain.ListPublicIPsResult{PublicIPs: ips}, nil
}

// ─── Converters ───────────────────────────────────────────────────────

func azureVNetToDomain(v *armnetwork.VirtualNetwork) domain.Network {
	n := domain.Network{
		ID:   ptrStr(v.Name),
		Name: ptrStr(v.Name),
	}
	if v.Properties != nil && v.Properties.AddressSpace != nil &&
		len(v.Properties.AddressSpace.AddressPrefixes) > 0 {
		n.CIDR = ptrStr(v.Properties.AddressSpace.AddressPrefixes[0])
	}
	return n
}

func azureSubnetToDomain(s *armnetwork.Subnet, networkName string) domain.Subnet {
	sub := domain.Subnet{
		ID:        networkName + "/" + ptrStr(s.Name),
		Name:      ptrStr(s.Name),
		NetworkID: networkName,
	}
	if s.Properties != nil {
		sub.CIDR = ptrStr(s.Properties.AddressPrefix)
	}
	return sub
}

func azureNSGToDomain(sg *armnetwork.SecurityGroup) domain.SecurityGroup {
	d := domain.SecurityGroup{
		ID:   ptrStr(sg.Name),
		Name: ptrStr(sg.Name),
	}
	if sg.Properties != nil {
		for _, rule := range sg.Properties.SecurityRules {
			if rule.Properties == nil {
				continue
			}
			d.Rules = append(d.Rules, azureRuleToDomainRule(rule))
		}
	}
	return d
}

func azureRuleToDomainRule(rule *armnetwork.SecurityRule) domain.SecurityGroupRule {
	p := rule.Properties
	r := domain.SecurityGroupRule{Direction: domain.Inbound}
	if p.Direction != nil && *p.Direction == armnetwork.SecurityRuleDirectionOutbound {
		r.Direction = domain.Outbound
	}
	if p.Protocol != nil {
		switch *p.Protocol {
		case armnetwork.SecurityRuleProtocolTCP:
			r.Protocol = "tcp"
		case armnetwork.SecurityRuleProtocolUDP:
			r.Protocol = "udp"
		default:
			r.Protocol = "-1"
		}
	}
	if p.SourceAddressPrefix != nil && *p.SourceAddressPrefix != "*" {
		r.CIDRs = []string{*p.SourceAddressPrefix}
	}
	return r
}

func azureIPToDomain(p *armnetwork.PublicIPAddress) domain.PublicIP {
	ip := domain.PublicIP{
		ID:   ptrStr(p.Name),
		Name: ptrStr(p.Name),
	}
	if p.Properties != nil {
		ip.Address = ptrStr(p.Properties.IPAddress)
	}
	return ip
}

func domainRuleToAzureRule(r domain.SecurityGroupRule, name string, priority int32) armnetwork.SecurityRule {
	dir := to.Ptr(armnetwork.SecurityRuleDirectionInbound)
	if r.Direction == domain.Outbound {
		dir = to.Ptr(armnetwork.SecurityRuleDirectionOutbound)
	}
	proto := to.Ptr(armnetwork.SecurityRuleProtocolAsterisk)
	switch r.Protocol {
	case "tcp":
		proto = to.Ptr(armnetwork.SecurityRuleProtocolTCP)
	case "udp":
		proto = to.Ptr(armnetwork.SecurityRuleProtocolUDP)
	}
	portRange := "*"
	if r.PortFrom != 0 {
		if r.PortTo != 0 && r.PortTo != r.PortFrom {
			portRange = fmt.Sprintf("%d-%d", r.PortFrom, r.PortTo)
		} else {
			portRange = fmt.Sprintf("%d", r.PortFrom)
		}
	}
	srcPrefix := "*"
	if len(r.CIDRs) > 0 {
		srcPrefix = r.CIDRs[0]
	}
	return armnetwork.SecurityRule{
		Name: &name,
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
			Direction:                dir,
			Priority:                 &priority,
			Protocol:                 proto,
			SourcePortRange:          to.Ptr("*"),
			DestinationPortRange:     to.Ptr(portRange),
			SourceAddressPrefix:      to.Ptr(srcPrefix),
			DestinationAddressPrefix: to.Ptr("*"),
		},
	}
}

func domainTagsToAzure(tags map[string]string) map[string]*string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]*string, len(tags))
	for k, v := range tags {
		v := v
		m[k] = &v
	}
	return m
}

func ptrStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func splitSubnetID(id string) []string {
	return splitN(id, "/", 2)
}

func splitN(s, sep string, n int) []string {
	idx := indexOf(s, sep[0])
	if idx < 0 || n < 2 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+1:]}
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ruleHash returns a stable 12-bit hash of a SecurityGroupRule for
// naming Azure SecurityRule resources. Not cryptographic; collision-
// resistant enough for per-NSG rule naming.
func ruleHash(r domain.SecurityGroupRule) int32 {
	h := int32(len(r.Protocol)*1000 + r.PortFrom*31 + r.PortTo + int(r.Direction)*7)
	for _, c := range r.CIDRs {
		for _, ch := range c {
			h = h*31 + int32(ch)
		}
	}
	if h < 0 {
		h = -h
	}
	return h % 3800
}

var _pipSeq int32

func pipCounter() int32 {
	_pipSeq++
	return _pipSeq
}

// ─── Error mapping ────────────────────────────────────────────────────

func mapAzureErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if contains(msg, "404", "NotFound", "ResourceNotFound") {
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	}
	if contains(msg, "409", "Conflict", "AlreadyExists") {
		return fmt.Errorf("%w: %v", domain.ErrAlreadyExists, err)
	}
	return err
}

func contains(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(sub) > 0 {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

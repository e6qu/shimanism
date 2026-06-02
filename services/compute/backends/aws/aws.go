// Package aws is the AWS EC2 passthrough backend for shimanism's compute
// service (Phase 16.B networking primitives). It uses the
// aws-sdk-go-v2/service/ec2 client to drive real AWS EC2 (or a
// sockerless-pointed client for tests).
//
// The shim's domain uses opaque IDs for all resources (VPCs, subnets,
// security groups, EIPs). The AWS backend maps domain IDs directly to
// AWS resource IDs (VpcId, SubnetId, GroupId, AllocationId) — the IDs
// assigned by AWS at creation time are stored as domain IDs without
// translation.
//
// Stateless: no name→ID tables. List operations re-read AWS on every
// request.
package aws

import (
	"context"
	"fmt"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Backend implements domain.Networking via real AWS EC2.
type Backend struct {
	c *ec2.Client
}

// New wraps an already-configured EC2 client.
func New(client *ec2.Client) *Backend { return &Backend{c: client} }

var _ domain.Networking = (*Backend)(nil)

// ─── Networks (VPCs) ─────────────────────────────────────────────────

func (b *Backend) CreateNetwork(ctx context.Context, name string, opt domain.CreateNetworkOptions) (domain.Network, error) {
	out, err := b.c.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: awsapi.String(opt.CIDR),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeVpc,
			Tags:         tagsToEC2(mergeTags(opt.Tags, "Name", name)),
		}},
	})
	if err != nil {
		return domain.Network{}, mapEC2Err(err)
	}
	return awsVPCToDomain(out.Vpc), nil
}

func (b *Backend) GetNetwork(ctx context.Context, id string) (domain.Network, error) {
	out, err := b.c.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []string{id},
	})
	if err != nil {
		return domain.Network{}, mapEC2Err(err)
	}
	if len(out.Vpcs) == 0 {
		return domain.Network{}, fmt.Errorf("VPC %q: %w", id, domain.ErrNotFound)
	}
	return awsVPCToDomain(&out.Vpcs[0]), nil
}

func (b *Backend) ListNetworks(ctx context.Context, opt domain.ListNetworksOptions) (domain.ListNetworksResult, error) {
	in := &ec2.DescribeVpcsInput{}
	if len(opt.IDs) > 0 {
		in.VpcIds = opt.IDs
	}
	out, err := b.c.DescribeVpcs(ctx, in)
	if err != nil {
		return domain.ListNetworksResult{}, mapEC2Err(err)
	}
	var nets []domain.Network
	for _, v := range out.Vpcs {
		v := v
		nets = append(nets, awsVPCToDomain(&v))
	}
	return domain.ListNetworksResult{Networks: nets}, nil
}

func (b *Backend) DeleteNetwork(ctx context.Context, id string) error {
	_, err := b.c.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: awsapi.String(id)})
	return mapEC2Err(err)
}

// ─── Subnets ─────────────────────────────────────────────────────────

func (b *Backend) CreateSubnet(ctx context.Context, name string, opt domain.CreateSubnetOptions) (domain.Subnet, error) {
	in := &ec2.CreateSubnetInput{
		VpcId:     awsapi.String(opt.NetworkID),
		CidrBlock: awsapi.String(opt.CIDR),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSubnet,
			Tags:         tagsToEC2(mergeTags(opt.Tags, "Name", name)),
		}},
	}
	if opt.Zone != "" {
		in.AvailabilityZone = awsapi.String(opt.Zone)
	}
	out, err := b.c.CreateSubnet(ctx, in)
	if err != nil {
		return domain.Subnet{}, mapEC2Err(err)
	}
	return awsSubnetToDomain(out.Subnet), nil
}

func (b *Backend) GetSubnet(ctx context.Context, id string) (domain.Subnet, error) {
	out, err := b.c.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{id}})
	if err != nil {
		return domain.Subnet{}, mapEC2Err(err)
	}
	if len(out.Subnets) == 0 {
		return domain.Subnet{}, fmt.Errorf("subnet %q: %w", id, domain.ErrNotFound)
	}
	return awsSubnetToDomain(&out.Subnets[0]), nil
}

func (b *Backend) ListSubnets(ctx context.Context, opt domain.ListSubnetsOptions) (domain.ListSubnetsResult, error) {
	in := &ec2.DescribeSubnetsInput{}
	if len(opt.IDs) > 0 {
		in.SubnetIds = opt.IDs
	}
	if opt.NetworkID != "" {
		in.Filters = []ec2types.Filter{{Name: awsapi.String("vpc-id"), Values: []string{opt.NetworkID}}}
	}
	out, err := b.c.DescribeSubnets(ctx, in)
	if err != nil {
		return domain.ListSubnetsResult{}, mapEC2Err(err)
	}
	var subs []domain.Subnet
	for _, s := range out.Subnets {
		s := s
		subs = append(subs, awsSubnetToDomain(&s))
	}
	return domain.ListSubnetsResult{Subnets: subs}, nil
}

func (b *Backend) DeleteSubnet(ctx context.Context, id string) error {
	_, err := b.c.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: awsapi.String(id)})
	return mapEC2Err(err)
}

// ─── Security Groups ─────────────────────────────────────────────────

func (b *Backend) CreateSecurityGroup(ctx context.Context, name string, opt domain.CreateSecurityGroupOptions) (domain.SecurityGroup, error) {
	in := &ec2.CreateSecurityGroupInput{
		GroupName:   awsapi.String(name),
		Description: awsapi.String(opt.Description),
	}
	if opt.NetworkID != "" {
		in.VpcId = awsapi.String(opt.NetworkID)
	}
	out, err := b.c.CreateSecurityGroup(ctx, in)
	if err != nil {
		return domain.SecurityGroup{}, mapEC2Err(err)
	}
	return domain.SecurityGroup{
		ID:          awsapi.ToString(out.GroupId),
		Name:        name,
		NetworkID:   opt.NetworkID,
		Description: opt.Description,
		Tags:        opt.Tags,
	}, nil
}

func (b *Backend) GetSecurityGroup(ctx context.Context, id string) (domain.SecurityGroup, error) {
	out, err := b.c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{id}})
	if err != nil {
		return domain.SecurityGroup{}, mapEC2Err(err)
	}
	if len(out.SecurityGroups) == 0 {
		return domain.SecurityGroup{}, fmt.Errorf("security group %q: %w", id, domain.ErrNotFound)
	}
	return awsSGToDomain(&out.SecurityGroups[0]), nil
}

func (b *Backend) ListSecurityGroups(ctx context.Context, opt domain.ListSecurityGroupsOptions) (domain.ListSecurityGroupsResult, error) {
	in := &ec2.DescribeSecurityGroupsInput{}
	if len(opt.IDs) > 0 {
		in.GroupIds = opt.IDs
	}
	if opt.NetworkID != "" {
		in.Filters = []ec2types.Filter{{Name: awsapi.String("vpc-id"), Values: []string{opt.NetworkID}}}
	}
	out, err := b.c.DescribeSecurityGroups(ctx, in)
	if err != nil {
		return domain.ListSecurityGroupsResult{}, mapEC2Err(err)
	}
	var sgs []domain.SecurityGroup
	for _, sg := range out.SecurityGroups {
		sg := sg
		sgs = append(sgs, awsSGToDomain(&sg))
	}
	return domain.ListSecurityGroupsResult{SecurityGroups: sgs}, nil
}

func (b *Backend) DeleteSecurityGroup(ctx context.Context, id string) error {
	_, err := b.c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: awsapi.String(id)})
	return mapEC2Err(err)
}

func (b *Backend) AddRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	perms := []ec2types.IpPermission{domainRuleToAWS(rule)}
	if rule.Direction == domain.Inbound {
		_, err := b.c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
			GroupId:       awsapi.String(sgID),
			IpPermissions: perms,
		})
		return mapEC2Err(err)
	}
	_, err := b.c.AuthorizeSecurityGroupEgress(ctx, &ec2.AuthorizeSecurityGroupEgressInput{
		GroupId:       awsapi.String(sgID),
		IpPermissions: perms,
	})
	return mapEC2Err(err)
}

func (b *Backend) RemoveRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	perms := []ec2types.IpPermission{domainRuleToAWS(rule)}
	if rule.Direction == domain.Inbound {
		_, err := b.c.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       awsapi.String(sgID),
			IpPermissions: perms,
		})
		return mapEC2Err(err)
	}
	_, err := b.c.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
		GroupId:       awsapi.String(sgID),
		IpPermissions: perms,
	})
	return mapEC2Err(err)
}

// ─── Public IPs (Elastic IPs) ────────────────────────────────────────

func (b *Backend) AllocatePublicIP(ctx context.Context, opt domain.AllocatePublicIPOptions) (domain.PublicIP, error) {
	out, err := b.c.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
	})
	if err != nil {
		return domain.PublicIP{}, mapEC2Err(err)
	}
	return domain.PublicIP{
		ID:      awsapi.ToString(out.AllocationId),
		Address: awsapi.ToString(out.PublicIp),
		Region:  opt.Region,
		Tags:    opt.Tags,
	}, nil
}

func (b *Backend) AssociatePublicIP(ctx context.Context, ipID, instanceID string) error {
	_, err := b.c.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: awsapi.String(ipID),
		InstanceId:   awsapi.String(instanceID),
	})
	return mapEC2Err(err)
}

func (b *Backend) DisassociatePublicIP(ctx context.Context, ipID string) error {
	// Look up the AssociationId from the allocation.
	out, err := b.c.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{ipID},
	})
	if err != nil {
		return mapEC2Err(err)
	}
	if len(out.Addresses) == 0 {
		return fmt.Errorf("EIP %q: %w", ipID, domain.ErrNotFound)
	}
	assocID := awsapi.ToString(out.Addresses[0].AssociationId)
	if assocID == "" {
		return nil // already disassociated
	}
	_, err = b.c.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
		AssociationId: awsapi.String(assocID),
	})
	return mapEC2Err(err)
}

func (b *Backend) ReleasePublicIP(ctx context.Context, ipID string) error {
	_, err := b.c.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
		AllocationId: awsapi.String(ipID),
	})
	return mapEC2Err(err)
}

func (b *Backend) ListPublicIPs(ctx context.Context, opt domain.ListPublicIPsOptions) (domain.ListPublicIPsResult, error) {
	in := &ec2.DescribeAddressesInput{}
	if len(opt.IDs) > 0 {
		in.AllocationIds = opt.IDs
	}
	out, err := b.c.DescribeAddresses(ctx, in)
	if err != nil {
		return domain.ListPublicIPsResult{}, mapEC2Err(err)
	}
	var ips []domain.PublicIP
	for _, a := range out.Addresses {
		a := a
		ips = append(ips, awsAddressToDomain(&a))
	}
	return domain.ListPublicIPsResult{PublicIPs: ips}, nil
}

// ─── Converters ───────────────────────────────────────────────────────

func awsVPCToDomain(v *ec2types.Vpc) domain.Network {
	n := domain.Network{
		ID:   awsapi.ToString(v.VpcId),
		CIDR: awsapi.ToString(v.CidrBlock),
		Tags: awsTagsToDomain(v.Tags),
	}
	n.Name = n.Tags["Name"]
	if n.Name == "" {
		n.Name = n.ID
	}
	return n
}

func awsSubnetToDomain(s *ec2types.Subnet) domain.Subnet {
	sub := domain.Subnet{
		ID:        awsapi.ToString(s.SubnetId),
		CIDR:      awsapi.ToString(s.CidrBlock),
		NetworkID: awsapi.ToString(s.VpcId),
		Zone:      awsapi.ToString(s.AvailabilityZone),
		Tags:      awsTagsToDomain(s.Tags),
	}
	sub.Name = sub.Tags["Name"]
	if sub.Name == "" {
		sub.Name = sub.ID
	}
	return sub
}

func awsSGToDomain(sg *ec2types.SecurityGroup) domain.SecurityGroup {
	d := domain.SecurityGroup{
		ID:          awsapi.ToString(sg.GroupId),
		Name:        awsapi.ToString(sg.GroupName),
		NetworkID:   awsapi.ToString(sg.VpcId),
		Description: awsapi.ToString(sg.Description),
		Tags:        awsTagsToDomain(sg.Tags),
	}
	for _, p := range sg.IpPermissions {
		d.Rules = append(d.Rules, awsPermToDomainRule(p, domain.Inbound))
	}
	for _, p := range sg.IpPermissionsEgress {
		d.Rules = append(d.Rules, awsPermToDomainRule(p, domain.Outbound))
	}
	return d
}

func awsPermToDomainRule(p ec2types.IpPermission, dir domain.RuleDirection) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{
		Protocol:  awsapi.ToString(p.IpProtocol),
		Direction: dir,
	}
	if p.FromPort != nil {
		r.PortFrom = int(*p.FromPort)
	}
	if p.ToPort != nil {
		r.PortTo = int(*p.ToPort)
	}
	for _, ipRange := range p.IpRanges {
		r.CIDRs = append(r.CIDRs, awsapi.ToString(ipRange.CidrIp))
	}
	return r
}

func awsAddressToDomain(a *ec2types.Address) domain.PublicIP {
	ip := domain.PublicIP{
		ID:         awsapi.ToString(a.AllocationId),
		Address:    awsapi.ToString(a.PublicIp),
		InstanceID: awsapi.ToString(a.InstanceId),
		Tags:       awsTagsToDomain(a.Tags),
	}
	ip.Name = ip.Tags["Name"]
	if ip.Name == "" {
		ip.Name = ip.ID
	}
	return ip
}

func domainRuleToAWS(r domain.SecurityGroupRule) ec2types.IpPermission {
	p := ec2types.IpPermission{IpProtocol: awsapi.String(r.Protocol)}
	if r.PortFrom != 0 {
		from := int32(r.PortFrom)
		p.FromPort = &from
	}
	if r.PortTo != 0 {
		to := int32(r.PortTo)
		p.ToPort = &to
	}
	for _, cidr := range r.CIDRs {
		cidr := cidr
		p.IpRanges = append(p.IpRanges, ec2types.IpRange{CidrIp: &cidr})
	}
	return p
}

func tagsToEC2(tags map[string]string) []ec2types.Tag {
	if len(tags) == 0 {
		return nil
	}
	out := make([]ec2types.Tag, 0, len(tags))
	for k, v := range tags {
		k, v := k, v
		out = append(out, ec2types.Tag{Key: &k, Value: &v})
	}
	return out
}

func awsTagsToDomain(tags []ec2types.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			m[*t.Key] = *t.Value
		}
	}
	return m
}

func mergeTags(base map[string]string, key, val string) map[string]string {
	m := make(map[string]string, len(base)+1)
	for k, v := range base {
		m[k] = v
	}
	if _, ok := m[key]; !ok {
		m[key] = val
	}
	return m
}

// ─── Error mapping ────────────────────────────────────────────────────

func mapEC2Err(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case contains(msg, "NotFound", "InvalidVpcID.NotFound", "InvalidSubnetID.NotFound",
		"InvalidGroup.NotFound", "InvalidAllocationID.NotFound"):
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	case contains(msg, "InvalidGroup.Duplicate", "InvalidVpc.Conflict"):
		return fmt.Errorf("%w: %v", domain.ErrAlreadyExists, err)
	default:
		return err
	}
}

func contains(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}

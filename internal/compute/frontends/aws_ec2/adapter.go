// Package aws_ec2 is the AWS EC2 frontend for shimanism's compute
// service (Phase 16.B networking + Phase 16.C instance lifecycle +
// Phase 17 block storage). It bridges the spec-driven ec2Query
// generated stubs (services/compute/gen) onto the neutral
// domain.Networking, domain.Instances, and domain.BlockStorage
// interfaces.
//
// Protocol: ec2Query (form-encoded POST, Action dispatch, flattened
// lists, EC2 XML error envelope). Auth: SigV4 with service="ec2".
package aws_ec2

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/internal/ec2query"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/compute/gen"
)

// ComputeBackend is satisfied by any type that implements both
// domain.Networking and domain.Instances — e.g. the inmem backend
// and every real-cloud backend.
type ComputeBackend interface {
	domain.Networking
	domain.Instances
	domain.BlockStorage
}

// Adapter binds gen.EC2Backend to a ComputeBackend.
type Adapter struct {
	n ComputeBackend
}

// New returns the http.Handler dispatching through the generated
// ec2Query router into the adapter bound to the given backend.
// SigV4 verification is wired in.
func New(n ComputeBackend) http.Handler {
	router := gen.RegisterEC2Routes(&Adapter{n: n})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "ec2", Region: "us-east-1"})
	return sigv4verifier.Middleware(verifier, ec2query.EmitVerifierError)(router)
}

// ─── helpers ────────────────────────────────────────────────────────

func strPtr(s string) *string { return &s }

// mapDomainErr converts domain errors to ec2query.BackendError with the
// correct EC2 error codes and HTTP status codes.
func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case domain.IsNotFound(err):
		return &ec2query.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidParameterValue",
			Message:    err.Error(),
		}
	case domain.IsAlreadyExists(err):
		return &ec2query.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidParameterValue",
			Message:    err.Error(),
		}
	case domain.IsNotSupported(err):
		return &ec2query.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Code:       "UnsupportedOperation",
			Message:    err.Error(),
		}
	default:
		return &ec2query.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Code:       "InternalError",
			Message:    err.Error(),
		}
	}
}

// domainTagsToGen converts domain tags map to a gen.TagList.
func domainTagsToGen(tags map[string]string) gen.TagList {
	if len(tags) == 0 {
		return gen.TagList{}
	}
	tl := gen.TagList{}
	for k, v := range tags {
		k, v := k, v
		tl.Member = append(tl.Member, gen.Tag{Key: &k, Value: &v})
	}
	return tl
}

// domainVpcToGen converts a domain.Network to a gen.Vpc.
func domainVpcToGen(n domain.Network) gen.Vpc {
	available := gen.VpcState("available")
	defaultTenancy := gen.Tenancy("default")
	acc := "000000000000"
	return gen.Vpc{
		VpcId:           &n.ID,
		CidrBlock:       &n.CIDR,
		State:           &available,
		InstanceTenancy: &defaultTenancy,
		OwnerId:         &acc,
		Tags:            domainTagsToGen(n.Tags),
	}
}

// domainSubnetToGen converts a domain.Subnet to a gen.Subnet.
func domainSubnetToGen(s domain.Subnet) gen.Subnet {
	available := gen.SubnetState("available")
	acc := "000000000000"
	sub := gen.Subnet{
		SubnetId:  &s.ID,
		CidrBlock: &s.CIDR,
		VpcId:     &s.NetworkID,
		State:     &available,
		OwnerId:   &acc,
		Tags:      domainTagsToGen(s.Tags),
	}
	if s.Zone != "" {
		sub.AvailabilityZone = &s.Zone
	}
	return sub
}

// domainSGToGen converts a domain.SecurityGroup to a gen.SecurityGroup.
func domainSGToGen(sg domain.SecurityGroup) gen.SecurityGroup {
	acc := "000000000000"
	g := gen.SecurityGroup{
		GroupId:     &sg.ID,
		GroupName:   &sg.Name,
		Description: &sg.Description,
		VpcId:       &sg.NetworkID,
		OwnerId:     &acc,
		Tags:        domainTagsToGen(sg.Tags),
	}
	for _, r := range sg.Rules {
		perm := domainRuleToPermission(r)
		if r.Direction == domain.Inbound {
			g.IpPermissions.Member = append(g.IpPermissions.Member, perm)
		} else {
			g.IpPermissionsEgress.Member = append(g.IpPermissionsEgress.Member, perm)
		}
	}
	return g
}

// domainRuleToPermission converts a domain.SecurityGroupRule to a
// gen.IpPermission.
func domainRuleToPermission(r domain.SecurityGroupRule) gen.IpPermission {
	p := gen.IpPermission{IpProtocol: strPtr(r.Protocol)}
	if r.PortFrom != 0 || r.PortTo != 0 {
		from := int32(r.PortFrom)
		to := int32(r.PortTo)
		if to == 0 {
			to = from
		}
		p.FromPort = &from
		p.ToPort = &to
	}
	for _, cidr := range r.CIDRs {
		cidr := cidr
		p.IpRanges.Member = append(p.IpRanges.Member, gen.IpRange{CidrIp: &cidr})
	}
	return p
}

// decodeFormPermissions decodes a list of IpPermission structs from the
// ec2Query form context. The SDK sends them as:
//
//	IpPermissions.1.IpProtocol=tcp
//	IpPermissions.1.FromPort=80
//	IpPermissions.1.ToPort=80
//	IpPermissions.1.IpRanges.1.CidrIp=0.0.0.0/0
//
// Complex struct lists are NOT decoded by the generated handler code,
// only available via ec2query.FormFromContext(ctx).
func decodeFormPermissions(ctx context.Context, prefix string, dir domain.RuleDirection) []domain.SecurityGroupRule {
	form := ec2query.FormFromContext(ctx)
	if form == nil {
		return nil
	}
	var rules []domain.SecurityGroupRule
	for i := 1; ; i++ {
		proto := form.Get(fmt.Sprintf("%s.%d.IpProtocol", prefix, i))
		if proto == "" {
			break
		}
		r := domain.SecurityGroupRule{Protocol: proto, Direction: dir}
		if v := form.Get(fmt.Sprintf("%s.%d.FromPort", prefix, i)); v != "" {
			n, _ := strconv.Atoi(v)
			r.PortFrom = n
		}
		if v := form.Get(fmt.Sprintf("%s.%d.ToPort", prefix, i)); v != "" {
			n, _ := strconv.Atoi(v)
			r.PortTo = n
		}
		for j := 1; ; j++ {
			cidr := form.Get(fmt.Sprintf("%s.%d.IpRanges.%d.CidrIp", prefix, i, j))
			if cidr == "" {
				break
			}
			r.CIDRs = append(r.CIDRs, cidr)
		}
		rules = append(rules, r)
	}
	return rules
}

// domainAddressToGen converts a domain.PublicIP to a gen.Address.
func domainAddressToGen(ip domain.PublicIP) gen.Address {
	vpc := gen.DomainType("vpc")
	a := gen.Address{
		AllocationId: &ip.ID,
		PublicIp:     &ip.Address,
		Domain:       &vpc,
		Tags:         domainTagsToGen(ip.Tags),
	}
	if ip.InstanceID != "" {
		a.InstanceId = &ip.InstanceID
		assocID := "eipassoc-" + ip.ID
		a.AssociationId = &assocID
	}
	return a
}

// ─── VPC operations ─────────────────────────────────────────────────

func (a *Adapter) CreateVpc(ctx context.Context, in *gen.CreateVpcRequest) (*gen.CreateVpcResult, error) {
	cidr := ""
	if in.CidrBlock != nil {
		cidr = *in.CidrBlock
	}
	// Use CIDR as name fallback; real EC2 doesn't have a name at create
	// time — name comes from a CreateTags call on the VpcId.
	name := cidr
	if name == "" {
		name = "vpc"
	}
	n, err := a.n.CreateNetwork(ctx, name, domain.CreateNetworkOptions{CIDR: cidr})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	vpc := domainVpcToGen(n)
	return &gen.CreateVpcResult{Vpc: &vpc}, nil
}

func (a *Adapter) DeleteVpc(ctx context.Context, in *gen.DeleteVpcRequest) (struct{}, error) {
	if err := a.n.DeleteNetwork(ctx, in.VpcId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeVpcs(ctx context.Context, in *gen.DescribeVpcsRequest) (*gen.DescribeVpcsResult, error) {
	opt := domain.ListNetworksOptions{IDs: in.VpcIds.Member}
	res, err := a.n.ListNetworks(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeVpcsResult{}
	for _, n := range res.Networks {
		vpc := domainVpcToGen(n)
		result.Vpcs.Member = append(result.Vpcs.Member, vpc)
	}
	if res.NextToken != "" {
		result.NextToken = &res.NextToken
	}
	return result, nil
}

func (a *Adapter) DescribeNetworkInterfaces(_ context.Context, _ *gen.DescribeNetworkInterfacesRequest) (*gen.DescribeNetworkInterfacesResult, error) {
	// The stateless shim holds no ENIs. Return an empty list so that
	// the hashicorp/aws provider's SG destroy path (which drains ENIs
	// before deleting the SG) completes without error.
	return &gen.DescribeNetworkInterfacesResult{}, nil
}

func (a *Adapter) DescribeVpcAttribute(_ context.Context, in *gen.DescribeVpcAttributeRequest) (*gen.DescribeVpcAttributeResult, error) {
	// DNS settings are not in the domain intersection (N25). Return
	// plausible defaults (enabled) so Terraform's read-after-create
	// cycle sees a consistent value and doesn't produce a diff.
	tval := true
	fval := false
	vpcID := in.VpcId
	return &gen.DescribeVpcAttributeResult{
		VpcId:                            &vpcID,
		EnableDnsHostnames:               &gen.AttributeBooleanValue{Value: &tval},
		EnableDnsSupport:                 &gen.AttributeBooleanValue{Value: &tval},
		EnableNetworkAddressUsageMetrics: &gen.AttributeBooleanValue{Value: &fval},
	}, nil
}

func (a *Adapter) ModifyVpcAttribute(_ context.Context, in *gen.ModifyVpcAttributeRequest) (struct{}, error) {
	// DNS settings are not part of the domain intersection (N25). Accept
	// the call silently — the attribute write is acknowledged but not
	// persisted (stateless shim). A real deployment targeting real-cloud
	// backends propagates the attribute natively.
	_ = in
	return struct{}{}, nil
}

// ─── Subnet operations ──────────────────────────────────────────────

func (a *Adapter) CreateSubnet(ctx context.Context, in *gen.CreateSubnetRequest) (*gen.CreateSubnetResult, error) {
	cidr := ""
	if in.CidrBlock != nil {
		cidr = *in.CidrBlock
	}
	zone := ""
	if in.AvailabilityZone != nil {
		zone = *in.AvailabilityZone
	}
	// Derive a stable name from the CIDR (EC2 names subnets via tags).
	name := "subnet"
	if cidr != "" {
		name = strings.ReplaceAll(cidr, "/", "-")
	}
	s, err := a.n.CreateSubnet(ctx, name, domain.CreateSubnetOptions{
		NetworkID: in.VpcId,
		CIDR:      cidr,
		Zone:      zone,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	sub := domainSubnetToGen(s)
	return &gen.CreateSubnetResult{Subnet: &sub}, nil
}

func (a *Adapter) DeleteSubnet(ctx context.Context, in *gen.DeleteSubnetRequest) (struct{}, error) {
	if err := a.n.DeleteSubnet(ctx, in.SubnetId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeSubnets(ctx context.Context, in *gen.DescribeSubnetsRequest) (*gen.DescribeSubnetsResult, error) {
	opt := domain.ListSubnetsOptions{IDs: in.SubnetIds.Member}
	// Collect VPC filter from form context if present.
	if form := ec2query.FormFromContext(ctx); form != nil {
		for i := 1; ; i++ {
			fname := form.Get("Filter." + strconv.Itoa(i) + ".Name")
			if fname == "" {
				break
			}
			if fname == "vpc-id" {
				if v := form.Get("Filter." + strconv.Itoa(i) + ".Value.1"); v != "" {
					opt.NetworkID = v
				}
			}
		}
	}
	res, err := a.n.ListSubnets(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeSubnetsResult{}
	for _, s := range res.Subnets {
		sub := domainSubnetToGen(s)
		result.Subnets.Member = append(result.Subnets.Member, sub)
	}
	if res.NextToken != "" {
		result.NextToken = &res.NextToken
	}
	return result, nil
}

func (a *Adapter) ModifySubnetAttribute(_ context.Context, in *gen.ModifySubnetAttributeRequest) (struct{}, error) {
	// MapPublicIpOnLaunch and similar attributes are not in the domain
	// intersection. Accept silently.
	_ = in
	return struct{}{}, nil
}

// ─── Security Group operations ──────────────────────────────────────

func (a *Adapter) CreateSecurityGroup(ctx context.Context, in *gen.CreateSecurityGroupRequest) (*gen.CreateSecurityGroupResult, error) {
	vpcID := ""
	if in.VpcId != nil {
		vpcID = *in.VpcId
	}
	sg, err := a.n.CreateSecurityGroup(ctx, in.GroupName, domain.CreateSecurityGroupOptions{
		NetworkID:   vpcID,
		Description: in.Description,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateSecurityGroupResult{GroupId: &sg.ID}, nil
}

func (a *Adapter) DeleteSecurityGroup(ctx context.Context, in *gen.DeleteSecurityGroupRequest) (*gen.DeleteSecurityGroupResult, error) {
	id := ""
	if in.GroupId != nil {
		id = *in.GroupId
	}
	if err := a.n.DeleteSecurityGroup(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	t := true
	return &gen.DeleteSecurityGroupResult{Return: &t}, nil
}

func (a *Adapter) DescribeSecurityGroups(ctx context.Context, in *gen.DescribeSecurityGroupsRequest) (*gen.DescribeSecurityGroupsResult, error) {
	opt := domain.ListSecurityGroupsOptions{IDs: in.GroupIds.Member}
	res, err := a.n.ListSecurityGroups(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeSecurityGroupsResult{}
	for _, sg := range res.SecurityGroups {
		result.SecurityGroups.Member = append(result.SecurityGroups.Member, domainSGToGen(sg))
	}
	if res.NextToken != "" {
		result.NextToken = &res.NextToken
	}
	return result, nil
}

func (a *Adapter) AuthorizeSecurityGroupIngress(ctx context.Context, in *gen.AuthorizeSecurityGroupIngressRequest) (*gen.AuthorizeSecurityGroupIngressResult, error) {
	sgID := ""
	if in.GroupId != nil {
		sgID = *in.GroupId
	}
	// Decode IpPermissions from raw form (complex list-of-structs not
	// decoded by generated code).
	rules := decodeFormPermissions(ctx, "IpPermissions", domain.Inbound)
	for _, rule := range rules {
		if err := a.n.AddRule(ctx, sgID, rule); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	// Legacy flat CidrIp / IpProtocol / FromPort / ToPort form path.
	if in.CidrIp != nil && len(rules) == 0 {
		rule := flatRuleFromIngress(in)
		if err := a.n.AddRule(ctx, sgID, rule); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	t := true
	return &gen.AuthorizeSecurityGroupIngressResult{Return: &t}, nil
}

func flatRuleFromIngress(in *gen.AuthorizeSecurityGroupIngressRequest) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{Direction: domain.Inbound}
	if in.IpProtocol != nil {
		r.Protocol = *in.IpProtocol
	}
	if in.FromPort != nil {
		r.PortFrom = int(*in.FromPort)
	}
	if in.ToPort != nil {
		r.PortTo = int(*in.ToPort)
	}
	if in.CidrIp != nil {
		r.CIDRs = []string{*in.CidrIp}
	}
	return r
}

func (a *Adapter) RevokeSecurityGroupIngress(ctx context.Context, in *gen.RevokeSecurityGroupIngressRequest) (*gen.RevokeSecurityGroupIngressResult, error) {
	sgID := ""
	if in.GroupId != nil {
		sgID = *in.GroupId
	}
	rules := decodeFormPermissions(ctx, "IpPermissions", domain.Inbound)
	for _, rule := range rules {
		if err := a.n.RemoveRule(ctx, sgID, rule); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	t := true
	return &gen.RevokeSecurityGroupIngressResult{Return: &t}, nil
}

func (a *Adapter) AuthorizeSecurityGroupEgress(ctx context.Context, in *gen.AuthorizeSecurityGroupEgressRequest) (*gen.AuthorizeSecurityGroupEgressResult, error) {
	rules := decodeFormPermissions(ctx, "IpPermissions", domain.Outbound)
	for _, rule := range rules {
		if err := a.n.AddRule(ctx, in.GroupId, rule); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	if in.CidrIp != nil && len(rules) == 0 {
		r := domain.SecurityGroupRule{Direction: domain.Outbound}
		r.CIDRs = []string{*in.CidrIp}
		if in.IpProtocol != nil {
			r.Protocol = *in.IpProtocol
		}
		if in.FromPort != nil {
			r.PortFrom = int(*in.FromPort)
		}
		if in.ToPort != nil {
			r.PortTo = int(*in.ToPort)
		}
		if err := a.n.AddRule(ctx, in.GroupId, r); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	t := true
	return &gen.AuthorizeSecurityGroupEgressResult{Return: &t}, nil
}

func (a *Adapter) RevokeSecurityGroupEgress(ctx context.Context, in *gen.RevokeSecurityGroupEgressRequest) (*gen.RevokeSecurityGroupEgressResult, error) {
	rules := decodeFormPermissions(ctx, "IpPermissions", domain.Outbound)
	for _, rule := range rules {
		if err := a.n.RemoveRule(ctx, in.GroupId, rule); err != nil {
			return nil, mapDomainErr(err)
		}
	}
	t := true
	return &gen.RevokeSecurityGroupEgressResult{Return: &t}, nil
}

func (a *Adapter) DescribeSecurityGroupRules(ctx context.Context, in *gen.DescribeSecurityGroupRulesRequest) (*gen.DescribeSecurityGroupRulesResult, error) {
	// Resolve group from filters in form context.
	sgID := ""
	if form := ec2query.FormFromContext(ctx); form != nil {
		for i := 1; ; i++ {
			fname := form.Get("Filter." + strconv.Itoa(i) + ".Name")
			if fname == "" {
				break
			}
			if fname == "group-id" {
				sgID = form.Get("Filter." + strconv.Itoa(i) + ".Value.1")
			}
		}
	}
	if sgID == "" {
		return &gen.DescribeSecurityGroupRulesResult{}, nil
	}
	sg, err := a.n.GetSecurityGroup(ctx, sgID)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeSecurityGroupRulesResult{}
	for i, r := range sg.Rules {
		isEgress := r.Direction == domain.Outbound
		ruleID := fmt.Sprintf("sgr-%s-%d", sg.ID, i)
		genRule := gen.SecurityGroupRule{
			SecurityGroupRuleId: &ruleID,
			GroupId:             &sg.ID,
			IsEgress:            &isEgress,
			IpProtocol:          strPtr(r.Protocol),
		}
		if r.PortFrom != 0 {
			from := int32(r.PortFrom)
			genRule.FromPort = &from
		}
		if r.PortTo != 0 {
			to := int32(r.PortTo)
			genRule.ToPort = &to
		}
		if len(r.CIDRs) > 0 {
			genRule.CidrIpv4 = &r.CIDRs[0]
		}
		result.SecurityGroupRules.Member = append(result.SecurityGroupRules.Member, genRule)
	}
	return result, nil
}

// ─── EIP / Address operations ────────────────────────────────────────

func (a *Adapter) AllocateAddress(ctx context.Context, in *gen.AllocateAddressRequest) (*gen.AllocateAddressResult, error) {
	ip, err := a.n.AllocatePublicIP(ctx, domain.AllocatePublicIPOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	vpc := gen.DomainType("vpc")
	return &gen.AllocateAddressResult{
		AllocationId: &ip.ID,
		PublicIp:     &ip.Address,
		Domain:       &vpc,
	}, nil
}

func (a *Adapter) ReleaseAddress(ctx context.Context, in *gen.ReleaseAddressRequest) (struct{}, error) {
	id := ""
	if in.AllocationId != nil {
		id = *in.AllocationId
	}
	if err := a.n.ReleasePublicIP(ctx, id); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) AssociateAddress(ctx context.Context, in *gen.AssociateAddressRequest) (*gen.AssociateAddressResult, error) {
	allocationID := ""
	if in.AllocationId != nil {
		allocationID = *in.AllocationId
	}
	instanceID := ""
	if in.InstanceId != nil {
		instanceID = *in.InstanceId
	}
	if err := a.n.AssociatePublicIP(ctx, allocationID, instanceID); err != nil {
		return nil, mapDomainErr(err)
	}
	assocID := "eipassoc-" + allocationID
	return &gen.AssociateAddressResult{AssociationId: &assocID}, nil
}

func (a *Adapter) DisassociateAddress(ctx context.Context, in *gen.DisassociateAddressRequest) (struct{}, error) {
	// AssociationId in our model encodes the EIP allocation ID as
	// "eipassoc-<allocationID>".
	assocID := ""
	if in.AssociationId != nil {
		assocID = *in.AssociationId
	}
	ipID := strings.TrimPrefix(assocID, "eipassoc-")
	if err := a.n.DisassociatePublicIP(ctx, ipID); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeAddresses(ctx context.Context, in *gen.DescribeAddressesRequest) (*gen.DescribeAddressesResult, error) {
	opt := domain.ListPublicIPsOptions{IDs: in.AllocationIds.Member}
	res, err := a.n.ListPublicIPs(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeAddressesResult{}
	for _, ip := range res.PublicIPs {
		addr := domainAddressToGen(ip)
		result.Addresses.Member = append(result.Addresses.Member, addr)
	}
	return result, nil
}

// ─── Tag operations ─────────────────────────────────────────────────
// Tags modify resource metadata. In the stateless shim, tag mutations
// on the domain objects are not persisted across requests unless the
// backend supports it natively. These operations accept the call and
// return success (AWS behavior: CreateTags is idempotent).

func (a *Adapter) CreateTags(_ context.Context, in *gen.CreateTagsRequest) (struct{}, error) {
	_ = in
	return struct{}{}, nil
}

func (a *Adapter) DeleteTags(_ context.Context, in *gen.DeleteTagsRequest) (struct{}, error) {
	_ = in
	return struct{}{}, nil
}

func (a *Adapter) DescribeTags(_ context.Context, in *gen.DescribeTagsRequest) (*gen.DescribeTagsResult, error) {
	_ = in
	return &gen.DescribeTagsResult{}, nil
}

// ─── Image operations ────────────────────────────────────────────────
//
// DescribeImages returns a synthetic entry for each requested image ID
// so that the hashicorp/aws Terraform provider can validate AMI IDs
// before calling RunInstances. The shim is stateless — it doesn't
// manage an AMI catalogue; any ID is accepted as valid.

// ModifyInstanceAttribute accepts instance attribute modifications and
// returns success without applying them. The shim is stateless — it
// doesn't persist per-instance attribute state.
func (a *Adapter) ModifyInstanceAttribute(_ context.Context, _ *gen.ModifyInstanceAttributeRequest) (struct{}, error) {
	return struct{}{}, nil
}

// DescribeInstanceAttribute returns a plausible default for each
// requested attribute. The Terraform provider reads several attributes
// after creating an instance (instanceInitiatedShutdownBehavior,
// disableApiTermination, userData, etc.) to populate state.

// DescribeInstanceStatus returns "ok/ok" (2/2 checks passed) for all
// requested instance IDs. The Terraform provider polls this to wait for
// system + instance status checks to clear after RunInstances.
func (a *Adapter) DescribeInstanceStatus(ctx context.Context, in *gen.DescribeInstanceStatusRequest) (*gen.DescribeInstanceStatusResult, error) {
	// Collect the requested instance IDs from form context (InstanceId.N).
	form := ec2query.FormFromContext(ctx)
	var ids []string
	for i := 1; ; i++ {
		v := form.Get("InstanceId." + strconv.Itoa(i))
		if v == "" {
			break
		}
		ids = append(ids, v)
	}
	// Fall back to all instances if no IDs requested.
	if len(ids) == 0 {
		res, err := a.n.DescribeInstances(ctx, domain.DescribeInstancesOptions{})
		if err != nil {
			return nil, mapDomainErr(err)
		}
		for _, inst := range res.Instances {
			ids = append(ids, inst.ID)
		}
	}
	statusOK := gen.SummaryStatus("ok")
	result := &gen.DescribeInstanceStatusResult{}
	for _, id := range ids {
		id := id
		code := int32(16)
		name := gen.InstanceStateName("running")
		result.InstanceStatuses.Member = append(result.InstanceStatuses.Member, gen.InstanceStatus{
			InstanceId:    &id,
			InstanceState: &gen.InstanceState{Code: &code, Name: &name},
			InstanceStatus: &gen.InstanceStatusSummary{
				Status: &statusOK,
			},
			SystemStatus: &gen.InstanceStatusSummary{
				Status: &statusOK,
			},
		})
	}
	return result, nil
}

func (a *Adapter) DescribeInstanceAttribute(ctx context.Context, in *gen.DescribeInstanceAttributeRequest) (*gen.InstanceAttribute, error) {
	// Attribute is an enum; the codegen template only handles plain
	// strings/bools/ints, so read it from form context directly.
	attr := &gen.InstanceAttribute{
		InstanceId: strPtr(in.InstanceId),
	}
	// Attribute is an enum; the codegen template only handles plain
	// strings/bools/ints. The ec2QueryName for Attribute is "Attribute"
	// (PascalCase), so read that key from form context directly.
	attrName := string(in.Attribute)
	if v := ec2query.FormFromContext(ctx).Get("Attribute"); v != "" {
		attrName = v
	}
	switch attrName {
	case "instanceInitiatedShutdownBehavior":
		attr.InstanceInitiatedShutdownBehavior = &gen.AttributeValue{Value: strPtr("stop")}
	case "disableApiTermination":
		f := false
		attr.DisableApiTermination = &gen.AttributeBooleanValue{Value: &f}
	case "disableApiStop":
		f := false
		attr.DisableApiStop = &gen.AttributeBooleanValue{Value: &f}
	case "ebsOptimized":
		f := false
		attr.EbsOptimized = &gen.AttributeBooleanValue{Value: &f}
	case "enaSupport":
		t := true
		attr.EnaSupport = &gen.AttributeBooleanValue{Value: &t}
	case "userData":
		attr.UserData = &gen.AttributeValue{Value: strPtr("")}
	case "sriovNetSupport":
		attr.SriovNetSupport = &gen.AttributeValue{Value: strPtr("")}
	case "rootDeviceName":
		attr.RootDeviceName = &gen.AttributeValue{Value: strPtr("/dev/xvda")}
	case "instanceType":
		// Return the actual instance type if we can look it up.
		res, err := a.n.DescribeInstances(ctx, domain.DescribeInstancesOptions{IDs: []string{in.InstanceId}})
		if err == nil && len(res.Instances) > 0 {
			attr.InstanceType = &gen.AttributeValue{Value: strPtr(res.Instances[0].InstanceType)}
		}
	}
	return attr, nil
}

func (a *Adapter) DescribeImages(_ context.Context, in *gen.DescribeImagesRequest) (*gen.DescribeImagesResult, error) {
	result := &gen.DescribeImagesResult{}
	// Echo back each requested image ID as a synthetic available image.
	for _, id := range in.ImageIds.Member {
		id := id
		state := gen.ImageState("available")
		rootDevType := gen.DeviceType("ebs")
		arch := gen.ArchitectureValues("x86_64")
		virtType := gen.VirtualizationType("hvm")
		hypervisor := gen.HypervisorType("xen")
		ownerID := strPtr("000000000000")
		result.Images.Member = append(result.Images.Member, gen.Image{
			ImageId:            &id,
			ImageLocation:      strPtr("shimanism/" + id),
			Name:               strPtr(id),
			OwnerId:            ownerID,
			State:              &state,
			RootDeviceType:     &rootDevType,
			Architecture:       &arch,
			VirtualizationType: &virtType,
			Hypervisor:         &hypervisor,
		})
	}
	return result, nil
}

// ─── Instance operations (Phase 16.C) ───────────────────────────────

func (a *Adapter) RunInstances(ctx context.Context, in *gen.RunInstancesRequest) (*gen.Reservation, error) {
	opts := domain.RunInstancesOptions{
		MinCount: int(in.MinCount),
		MaxCount: int(in.MaxCount),
	}
	if in.ImageId != nil {
		opts.ImageID = *in.ImageId
	}
	// InstanceType is an enum; the codegen template only handles plain
	// strings, so we read it from form context directly.
	if v := ec2query.FormFromContext(ctx).Get("InstanceType"); v != "" {
		opts.InstanceType = v
	} else if in.InstanceType != nil {
		opts.InstanceType = string(*in.InstanceType)
	}
	if in.SubnetId != nil {
		opts.SubnetID = *in.SubnetId
	}
	if in.KeyName != nil {
		opts.KeyName = *in.KeyName
	}
	if in.UserData != nil {
		opts.UserData = *in.UserData
	}
	opts.SecurityGroupIDs = in.SecurityGroupIds.Member

	instances, err := a.n.RunInstances(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}

	now := time.Now()
	rid := "r-" + instances[0].ID
	res := &gen.Reservation{
		ReservationId: strPtr(rid),
		OwnerId:       strPtr("000000000000"),
	}
	for _, inst := range instances {
		inst := inst
		res.Instances.Member = append(res.Instances.Member, domainInstanceToGen(inst, now))
	}
	return res, nil
}

func (a *Adapter) DescribeInstances(ctx context.Context, in *gen.DescribeInstancesRequest) (*gen.DescribeInstancesResult, error) {
	opts := domain.DescribeInstancesOptions{
		IDs: in.InstanceIds.Member,
	}
	// Parse instance-state-name filter. When the caller passes explicit
	// states (e.g. the Terraform destroy waiter includes "terminated"),
	// forward them so terminated instances are visible. Without this
	// filter the default domain behavior excludes terminated instances.
	if form := ec2query.FormFromContext(ctx); form != nil {
		for i := 1; ; i++ {
			fname := form.Get("Filter." + strconv.Itoa(i) + ".Name")
			if fname == "" {
				break
			}
			if fname == "instance-state-name" {
				for j := 1; ; j++ {
					sv := form.Get("Filter." + strconv.Itoa(i) + ".Value." + strconv.Itoa(j))
					if sv == "" {
						break
					}
					for _, part := range strings.Split(sv, ",") {
						if ds, ok := ec2StateToDomain(strings.TrimSpace(part)); ok {
							opts.States = append(opts.States, ds)
						}
					}
				}
			}
		}
	}
	res, err := a.n.DescribeInstances(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	now := time.Now()
	result := &gen.DescribeInstancesResult{}
	// Group all instances into a single reservation (shim uses owner=000000000000).
	if len(res.Instances) > 0 {
		rv := gen.Reservation{
			ReservationId: strPtr("r-shim"),
			OwnerId:       strPtr("000000000000"),
		}
		for _, inst := range res.Instances {
			inst := inst
			rv.Instances.Member = append(rv.Instances.Member, domainInstanceToGen(inst, now))
		}
		result.Reservations.Member = append(result.Reservations.Member, rv)
	}
	return result, nil
}

func (a *Adapter) StartInstances(ctx context.Context, in *gen.StartInstancesRequest) (*gen.StartInstancesResult, error) {
	instances, err := a.n.StartInstances(ctx, in.InstanceIds.Member)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.StartInstancesResult{}
	for _, inst := range instances {
		inst := inst
		result.StartingInstances.Member = append(result.StartingInstances.Member, gen.InstanceStateChange{
			InstanceId:    strPtr(inst.ID),
			CurrentState:  domainStateToGen(inst.State),
			PreviousState: domainStateToGen(domain.InstanceStateStopped),
		})
	}
	return result, nil
}

func (a *Adapter) StopInstances(ctx context.Context, in *gen.StopInstancesRequest) (*gen.StopInstancesResult, error) {
	instances, err := a.n.StopInstances(ctx, in.InstanceIds.Member)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.StopInstancesResult{}
	for _, inst := range instances {
		inst := inst
		result.StoppingInstances.Member = append(result.StoppingInstances.Member, gen.InstanceStateChange{
			InstanceId:    strPtr(inst.ID),
			CurrentState:  domainStateToGen(inst.State),
			PreviousState: domainStateToGen(domain.InstanceStateRunning),
		})
	}
	return result, nil
}

func (a *Adapter) TerminateInstances(ctx context.Context, in *gen.TerminateInstancesRequest) (*gen.TerminateInstancesResult, error) {
	instances, err := a.n.TerminateInstances(ctx, in.InstanceIds.Member)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.TerminateInstancesResult{}
	for _, inst := range instances {
		inst := inst
		result.TerminatingInstances.Member = append(result.TerminatingInstances.Member, gen.InstanceStateChange{
			InstanceId:    strPtr(inst.ID),
			CurrentState:  domainStateToGen(inst.State),
			PreviousState: domainStateToGen(domain.InstanceStateRunning),
		})
	}
	return result, nil
}

func (a *Adapter) RebootInstances(ctx context.Context, in *gen.RebootInstancesRequest) (struct{}, error) {
	if err := a.n.RebootInstances(ctx, in.InstanceIds.Member); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeInstanceTypes(ctx context.Context, in *gen.DescribeInstanceTypesRequest) (*gen.DescribeInstanceTypesResult, error) {
	// InstanceTypes is a list<enum>; the codegen template only handles
	// list<string>, so read InstanceType.N directly from form context.
	form := ec2query.FormFromContext(ctx)
	opts := domain.DescribeInstanceTypesOptions{}
	for i := 1; ; i++ {
		v := form.Get("InstanceType." + strconv.Itoa(i))
		if v == "" {
			break
		}
		opts.InstanceTypes = append(opts.InstanceTypes, v)
	}
	// Fall back to in.InstanceTypes.Member if any were decoded by generated code.
	if len(opts.InstanceTypes) == 0 {
		for _, t := range in.InstanceTypes.Member {
			opts.InstanceTypes = append(opts.InstanceTypes, string(t))
		}
	}
	res, err := a.n.DescribeInstanceTypes(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeInstanceTypesResult{}
	for _, t := range res.InstanceTypes {
		t := t
		vcpus := int32(t.VCPUs)
		mem := int64(t.MemoryMiB)
		it := gen.InstanceType(t.InstanceType)
		result.InstanceTypes.Member = append(result.InstanceTypes.Member, gen.InstanceTypeInfo{
			InstanceType: &it,
			VCpuInfo:     &gen.VCpuInfo{DefaultVCpus: &vcpus},
			MemoryInfo:   &gen.MemoryInfo{SizeInMiB: &mem},
		})
	}
	return result, nil
}

// ─── Instance converters ─────────────────────────────────────────────

func domainInstanceToGen(inst domain.Instance, launchTime time.Time) gen.Instance {
	it := gen.InstanceType(inst.InstanceType)
	state := domainStateToGen(inst.State)
	g := gen.Instance{
		InstanceId:       strPtr(inst.ID),
		ImageId:          strPtr(inst.ImageID),
		InstanceType:     &it,
		State:            state,
		PrivateIpAddress: strPtr(inst.PrivateIP),
		LaunchTime:       &launchTime,
	}
	if inst.SubnetID != "" {
		g.SubnetId = strPtr(inst.SubnetID)
	}
	if inst.NetworkID != "" {
		g.VpcId = strPtr(inst.NetworkID)
	}
	if inst.KeyName != "" {
		g.KeyName = strPtr(inst.KeyName)
	}
	if inst.PublicIP != "" {
		g.PublicIpAddress = strPtr(inst.PublicIP)
	}
	return g
}

// EC2 state codes: 0=pending, 16=running, 32=shutting-down, 48=terminated, 64=stopping, 80=stopped
// ec2StateToDomain maps an EC2 wire state name string to a domain state.
func ec2StateToDomain(name string) (domain.InstanceState, bool) {
	m := map[string]domain.InstanceState{
		"pending":       domain.InstanceStatePending,
		"running":       domain.InstanceStateRunning,
		"stopped":       domain.InstanceStateStopped,
		"stopping":      domain.InstanceStateStopped,
		"shutting-down": domain.InstanceStateTerminated,
		"terminated":    domain.InstanceStateTerminated,
	}
	s, ok := m[name]
	return s, ok
}

func domainStateToGen(s domain.InstanceState) *gen.InstanceState {
	type stateCode struct {
		code int32
		name gen.InstanceStateName
	}
	m := map[domain.InstanceState]stateCode{
		domain.InstanceStatePending:    {0, gen.InstanceStateNamePending},
		domain.InstanceStateRunning:    {16, gen.InstanceStateNameRunning},
		domain.InstanceStateStopped:    {80, gen.InstanceStateNameStopped},
		domain.InstanceStateTerminated: {48, gen.InstanceStateNameTerminated},
	}
	sc, ok := m[s]
	if !ok {
		sc = stateCode{16, gen.InstanceStateNameRunning}
	}
	return &gen.InstanceState{Code: &sc.code, Name: &sc.name}
}

// ─── Block Storage ───────────────────────────────────────────────────

func (a *Adapter) CreateVolume(ctx context.Context, in *gen.CreateVolumeRequest) (*gen.Volume, error) {
	opts := domain.CreateVolumeOptions{}
	if in.Size != nil {
		opts.SizeGiB = int(*in.Size)
	}
	if in.VolumeType != nil {
		opts.VolumeType = string(*in.VolumeType)
	}
	if in.AvailabilityZone != nil {
		opts.Zone = *in.AvailabilityZone
	}
	if in.SnapshotId != nil {
		opts.SnapshotID = *in.SnapshotId
	}
	vol, err := a.n.CreateVolume(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return domainVolumeToGen(vol), nil
}

func (a *Adapter) DeleteVolume(ctx context.Context, in *gen.DeleteVolumeRequest) (struct{}, error) {
	if err := a.n.DeleteVolume(ctx, in.VolumeId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeVolumes(ctx context.Context, in *gen.DescribeVolumesRequest) (*gen.DescribeVolumesResult, error) {
	opts := domain.DescribeVolumesOptions{IDs: in.VolumeIds.Member}
	res, err := a.n.DescribeVolumes(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeVolumesResult{}
	for _, vol := range res.Volumes {
		result.Volumes.Member = append(result.Volumes.Member, *domainVolumeToGen(vol))
	}
	return result, nil
}

func (a *Adapter) AttachVolume(ctx context.Context, in *gen.AttachVolumeRequest) (*gen.VolumeAttachment, error) {
	opts := domain.AttachVolumeOptions{DeviceName: in.Device}
	att, err := a.n.AttachVolume(ctx, in.VolumeId, in.InstanceId, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return domainVolumeAttachmentToGen(att), nil
}

func (a *Adapter) DetachVolume(ctx context.Context, in *gen.DetachVolumeRequest) (*gen.VolumeAttachment, error) {
	instanceID := ""
	if in.InstanceId != nil {
		instanceID = *in.InstanceId
	}
	att, err := a.n.DetachVolume(ctx, in.VolumeId, instanceID)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return domainVolumeAttachmentToGen(att), nil
}

func (a *Adapter) CreateSnapshot(ctx context.Context, in *gen.CreateSnapshotRequest) (*gen.Snapshot, error) {
	opts := domain.CreateSnapshotOptions{}
	if in.Description != nil {
		opts.Description = *in.Description
	}
	snap, err := a.n.CreateSnapshot(ctx, in.VolumeId, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return domainSnapshotToGen(snap), nil
}

func (a *Adapter) DeleteSnapshot(ctx context.Context, in *gen.DeleteSnapshotRequest) (struct{}, error) {
	if err := a.n.DeleteSnapshot(ctx, in.SnapshotId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) DescribeSnapshots(ctx context.Context, in *gen.DescribeSnapshotsRequest) (*gen.DescribeSnapshotsResult, error) {
	opts := domain.DescribeSnapshotsOptions{IDs: in.SnapshotIds.Member}
	res, err := a.n.DescribeSnapshots(ctx, opts)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	result := &gen.DescribeSnapshotsResult{}
	for _, snap := range res.Snapshots {
		result.Snapshots.Member = append(result.Snapshots.Member, *domainSnapshotToGen(snap))
	}
	return result, nil
}

// ─── block-storage wire-type converters ─────────────────────────────

func domainVolumeToGen(vol domain.Volume) *gen.Volume {
	state := gen.VolumeState(vol.State)
	volType := gen.VolumeType(vol.VolumeType)
	size := int32(vol.SizeGiB)
	now := time.Now()
	g := &gen.Volume{
		VolumeId:         &vol.ID,
		Size:             &size,
		VolumeType:       &volType,
		State:            &state,
		AvailabilityZone: &vol.Zone,
		CreateTime:       &now, // provider calls .Format() without nil guard
		Tags:             domainTagsToGen(vol.Tags),
	}
	if vol.SnapshotID != "" {
		g.SnapshotId = &vol.SnapshotID
	}
	if vol.InstanceID != "" {
		attState := gen.VolumeAttachmentState(domain.VolumeAttachmentStateAttached)
		g.Attachments.Member = append(g.Attachments.Member, gen.VolumeAttachment{
			VolumeId:   &vol.ID,
			InstanceId: &vol.InstanceID,
			Device:     &vol.DeviceName,
			State:      &attState,
		})
	}
	return g
}

func domainVolumeAttachmentToGen(att domain.VolumeAttachment) *gen.VolumeAttachment {
	state := gen.VolumeAttachmentState(att.State)
	return &gen.VolumeAttachment{
		VolumeId:   &att.VolumeID,
		InstanceId: &att.InstanceID,
		Device:     &att.DeviceName,
		State:      &state,
	}
}

func domainSnapshotToGen(snap domain.Snapshot) *gen.Snapshot {
	state := gen.SnapshotState(snap.State)
	size := int32(snap.VolumeSize)
	progress := "100%"
	if snap.State == domain.SnapshotStatePending {
		progress = "0%"
	}
	return &gen.Snapshot{
		SnapshotId:  &snap.ID,
		VolumeId:    &snap.VolumeID,
		VolumeSize:  &size,
		State:       &state,
		Description: &snap.Description,
		Progress:    &progress,
		Tags:        domainTagsToGen(snap.Tags),
	}
}

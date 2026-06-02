// Package aws_ec2 is the AWS EC2 frontend for shimanism's compute
// service, Phase 16.B (networking primitives). It bridges the
// spec-driven ec2Query generated stubs (services/compute/gen) onto the
// neutral domain.Networking interface.
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

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/internal/ec2query"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/compute/gen"
)

// Adapter binds gen.EC2Backend to a domain.Networking backend.
type Adapter struct {
	n domain.Networking
}

// New returns the http.Handler dispatching through the generated
// ec2Query router into the adapter bound to the given backend.
// SigV4 verification is wired in.
func New(n domain.Networking) http.Handler {
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

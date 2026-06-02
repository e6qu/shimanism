// Conformance: AWS EC2-shaped frontend exercised by the official
// aws-sdk-go-v2/service/ec2 SDK. The SDK is pointed at the shim via
// BaseEndpoint; the shim's SigV4 verifier checks the request signature
// against the trusted test credentials.
//
// This lane covers the Phase 16.B networking operations:
// VPC lifecycle, Subnet lifecycle, SecurityGroup lifecycle, and
// Elastic IP lifecycle.
package conformance_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

// newEC2Client builds an aws-sdk-go-v2 EC2 client pointed at the
// shim. Same SigV4 credentials the verifier trusts.
func newEC2Client(t *testing.T, endpoint string) *ec2.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return ec2.NewFromConfig(cfg, func(o *ec2.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_EC2_VPCLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// CreateVpc
	create, err := cli.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	if create.Vpc == nil || aws.ToString(create.Vpc.VpcId) == "" {
		t.Fatalf("CreateVpc returned nil Vpc or empty ID")
	}
	if aws.ToString(create.Vpc.CidrBlock) != "10.0.0.0/16" {
		t.Errorf("CidrBlock = %q, want 10.0.0.0/16", aws.ToString(create.Vpc.CidrBlock))
	}
	vpcID := aws.ToString(create.Vpc.VpcId)

	// DescribeVpcs — by ID filter
	desc, err := cli.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{
		VpcIds: []string{vpcID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}
	if len(desc.Vpcs) != 1 {
		t.Fatalf("DescribeVpcs count = %d, want 1", len(desc.Vpcs))
	}
	if aws.ToString(desc.Vpcs[0].VpcId) != vpcID {
		t.Errorf("DescribeVpcs VpcId = %q, want %q", aws.ToString(desc.Vpcs[0].VpcId), vpcID)
	}

	// DeleteVpc
	if _, err := cli.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)}); err != nil {
		t.Fatalf("DeleteVpc: %v", err)
	}

	// DescribeVpcs after delete — should be empty
	desc2, err := cli.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		t.Fatalf("DescribeVpcs after delete: %v", err)
	}
	if len(desc2.Vpcs) != 0 {
		t.Errorf("DescribeVpcs after delete count = %d, want 0", len(desc2.Vpcs))
	}
}

func TestAWSSDK_EC2_SubnetLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// Create parent VPC
	vpc, err := cli.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() {
		cli.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
	})

	// CreateSubnet
	sub, err := cli.CreateSubnet(ctx, &ec2.CreateSubnetInput{
		VpcId:            aws.String(vpcID),
		CidrBlock:        aws.String("10.0.1.0/24"),
		AvailabilityZone: aws.String("us-east-1a"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	if sub.Subnet == nil {
		t.Fatalf("CreateSubnet returned nil Subnet")
	}
	subnetID := aws.ToString(sub.Subnet.SubnetId)
	if aws.ToString(sub.Subnet.CidrBlock) != "10.0.1.0/24" {
		t.Errorf("CidrBlock = %q, want 10.0.1.0/24", aws.ToString(sub.Subnet.CidrBlock))
	}
	if aws.ToString(sub.Subnet.AvailabilityZone) != "us-east-1a" {
		t.Errorf("AZ = %q, want us-east-1a", aws.ToString(sub.Subnet.AvailabilityZone))
	}

	// DescribeSubnets
	dsub, err := cli.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{SubnetIds: []string{subnetID}})
	if err != nil {
		t.Fatalf("DescribeSubnets: %v", err)
	}
	if len(dsub.Subnets) != 1 {
		t.Fatalf("DescribeSubnets count = %d, want 1", len(dsub.Subnets))
	}

	// DeleteSubnet
	if _, err := cli.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subnetID)}); err != nil {
		t.Fatalf("DeleteSubnet: %v", err)
	}
}

func TestAWSSDK_EC2_SecurityGroupLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// Create parent VPC
	vpc, err := cli.CreateVpc(ctx, &ec2.CreateVpcInput{CidrBlock: aws.String("10.0.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := aws.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() { cli.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)}) })

	// CreateSecurityGroup
	sg, err := cli.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("web-tier"),
		Description: aws.String("Web tier SG"),
		VpcId:       aws.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	if aws.ToString(sg.GroupId) == "" {
		t.Fatalf("CreateSecurityGroup returned empty GroupId")
	}
	sgID := aws.ToString(sg.GroupId)
	t.Cleanup(func() { cli.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}) })

	// AuthorizeSecurityGroupIngress — add port 80 allow
	if _, err := cli.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(80),
			ToPort:     aws.Int32(80),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	// DescribeSecurityGroups — verify rule present
	dsg, err := cli.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}
	if len(dsg.SecurityGroups) != 1 {
		t.Fatalf("DescribeSecurityGroups count = %d, want 1", len(dsg.SecurityGroups))
	}
	if len(dsg.SecurityGroups[0].IpPermissions) != 1 {
		t.Errorf("IpPermissions count = %d, want 1", len(dsg.SecurityGroups[0].IpPermissions))
	}

	// RevokeSecurityGroupIngress
	if _, err := cli.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
		GroupId: aws.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: aws.String("tcp"),
			FromPort:   aws.Int32(80),
			ToPort:     aws.Int32(80),
			IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
		}},
	}); err != nil {
		t.Fatalf("RevokeSecurityGroupIngress: %v", err)
	}

	// DescribeSecurityGroups — verify rule removed
	dsg2, err := cli.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{GroupIds: []string{sgID}})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups after revoke: %v", err)
	}
	if len(dsg2.SecurityGroups[0].IpPermissions) != 0 {
		t.Errorf("IpPermissions after revoke = %d, want 0", len(dsg2.SecurityGroups[0].IpPermissions))
	}

	// DeleteSecurityGroup
	if _, err := cli.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)}); err != nil {
		t.Fatalf("DeleteSecurityGroup: %v", err)
	}
}

func TestAWSSDK_EC2_ElasticIPLifecycle(t *testing.T) {
	srv := harness.StartComputeServerAWS(t, inmem.New())
	cli := newEC2Client(t, srv.URL)
	ctx := context.Background()

	// AllocateAddress
	alloc, err := cli.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		Domain: ec2types.DomainTypeVpc,
	})
	if err != nil {
		t.Fatalf("AllocateAddress: %v", err)
	}
	if aws.ToString(alloc.AllocationId) == "" || aws.ToString(alloc.PublicIp) == "" {
		t.Fatalf("AllocateAddress: AllocationId=%q PublicIp=%q",
			aws.ToString(alloc.AllocationId), aws.ToString(alloc.PublicIp))
	}
	allocID := aws.ToString(alloc.AllocationId)
	t.Cleanup(func() {
		cli.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(allocID)})
	})

	// DescribeAddresses — verify EIP present
	desc, err := cli.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		AllocationIds: []string{allocID},
	})
	if err != nil {
		t.Fatalf("DescribeAddresses: %v", err)
	}
	if len(desc.Addresses) != 1 {
		t.Fatalf("DescribeAddresses count = %d, want 1", len(desc.Addresses))
	}
	if aws.ToString(desc.Addresses[0].AllocationId) != allocID {
		t.Errorf("AllocationId = %q, want %q", aws.ToString(desc.Addresses[0].AllocationId), allocID)
	}

	// AssociateAddress
	assoc, err := cli.AssociateAddress(ctx, &ec2.AssociateAddressInput{
		AllocationId: aws.String(allocID),
		InstanceId:   aws.String("i-test123"),
	})
	if err != nil {
		t.Fatalf("AssociateAddress: %v", err)
	}
	if aws.ToString(assoc.AssociationId) == "" {
		t.Errorf("AssociateAddress returned empty AssociationId")
	}

	// DisassociateAddress
	assocID := aws.ToString(assoc.AssociationId)
	if _, err := cli.DisassociateAddress(ctx, &ec2.DisassociateAddressInput{
		AssociationId: aws.String(assocID),
	}); err != nil {
		t.Fatalf("DisassociateAddress: %v", err)
	}

	// ReleaseAddress
	if _, err := cli.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(allocID)}); err != nil {
		t.Fatalf("ReleaseAddress: %v", err)
	}
}

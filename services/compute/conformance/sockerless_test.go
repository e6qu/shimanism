// Sockerless lane for the compute service, Phase 16.B.
//
// Through-shim path (AWS frontend example):
//
//	AWS SDK EC2 → shim's EC2 frontend → domain.Networking
//	    → shim's AWS EC2 backend → sockerless's EC2 sim.
//
// VPC / subnet / security-group / Elastic IP API calls are pure
// metadata operations in sockerless — no Firecracker VM boot
// required. The sockerless lane for these is green immediately;
// only the Phase 16.C instance lane (RunInstances, StartInstances,
// etc.) is gated on sockerless #373 / #374 / #375.
//
// Set SOCKERLESS_AWS_ENDPOINT (the sim's HTTP/HTTPS endpoint) to
// opt in. If unset, all subtests in this file skip.
package conformance_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/compute/backends/aws"
)

// ─── helpers ─────────────────────────────────────────────────────────

func newSockerlessAWSEC2Client(t *testing.T, endpoint string) *awsec2sdk.Client {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		t.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		})
	}
	return awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

// ─── Tests ───────────────────────────────────────────────────────────

// TestSockerless_AWSEC2_Through_Shim_VPCLifecycle drives:
//
//	AWS SDK → shim's EC2 frontend → AWS EC2 backend → sockerless EC2 sim.
//
// VPC create/describe/delete — no Firecracker required.
func TestSockerless_AWSEC2_Through_Shim_VPCLifecycle(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	// Backend leg: shim's AWS EC2 backend → sockerless EC2 sim.
	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)

	// Frontend leg: official EC2 SDK → shim.
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// CreateVpc through shim → sockerless.
	create, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.1.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc (through shim → sockerless): %v", err)
	}
	vpcID := awsapi.ToString(create.Vpc.VpcId)
	if vpcID == "" {
		t.Fatalf("CreateVpc returned empty VpcId")
	}
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)})
	})

	// DescribeVpcs — verify presence.
	desc, err := frontendClient.DescribeVpcs(ctx, &awsec2sdk.DescribeVpcsInput{
		VpcIds: []string{vpcID},
	})
	if err != nil {
		t.Fatalf("DescribeVpcs: %v", err)
	}
	if len(desc.Vpcs) != 1 || awsapi.ToString(desc.Vpcs[0].VpcId) != vpcID {
		t.Errorf("DescribeVpcs: got %d VPCs, want 1 with id %q", len(desc.Vpcs), vpcID)
	}

	// DeleteVpc.
	if _, err := frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}); err != nil {
		t.Fatalf("DeleteVpc: %v", err)
	}
}

// TestSockerless_AWSEC2_Through_Shim_SecurityGroup drives:
//
//	AWS SDK → shim → AWS backend → sockerless.
//
// SecurityGroup create/authorize-ingress/describe/delete.
func TestSockerless_AWSEC2_Through_Shim_SecurityGroup(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// CreateVpc as parent.
	vpc, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.2.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)})
	})

	// CreateSecurityGroup.
	sg, err := frontendClient.CreateSecurityGroup(ctx, &awsec2sdk.CreateSecurityGroupInput{
		GroupName:   awsapi.String("sockerless-sg"),
		Description: awsapi.String("sockerless conformance SG"),
		VpcId:       awsapi.String(vpcID),
	})
	if err != nil {
		t.Fatalf("CreateSecurityGroup: %v", err)
	}
	sgID := awsapi.ToString(sg.GroupId)
	t.Cleanup(func() {
		frontendClient.DeleteSecurityGroup(ctx, &awsec2sdk.DeleteSecurityGroupInput{GroupId: awsapi.String(sgID)})
	})

	// AuthorizeSecurityGroupIngress.
	if _, err := frontendClient.AuthorizeSecurityGroupIngress(ctx, &awsec2sdk.AuthorizeSecurityGroupIngressInput{
		GroupId: awsapi.String(sgID),
		IpPermissions: []ec2types.IpPermission{{
			IpProtocol: awsapi.String("tcp"),
			FromPort:   awsapi.Int32(443),
			ToPort:     awsapi.Int32(443),
			IpRanges:   []ec2types.IpRange{{CidrIp: awsapi.String("10.0.0.0/8")}},
		}},
	}); err != nil {
		t.Fatalf("AuthorizeSecurityGroupIngress: %v", err)
	}

	// DescribeSecurityGroups — verify rule.
	dsg, err := frontendClient.DescribeSecurityGroups(ctx, &awsec2sdk.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		t.Fatalf("DescribeSecurityGroups: %v", err)
	}
	if len(dsg.SecurityGroups) != 1 {
		t.Fatalf("DescribeSecurityGroups count = %d, want 1", len(dsg.SecurityGroups))
	}

	// DeleteSecurityGroup.
	if _, err := frontendClient.DeleteSecurityGroup(ctx, &awsec2sdk.DeleteSecurityGroupInput{
		GroupId: awsapi.String(sgID),
	}); err != nil {
		t.Fatalf("DeleteSecurityGroup: %v", err)
	}
}

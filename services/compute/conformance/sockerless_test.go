// Sockerless lane for the compute service, Phase 16.B + 16.C.
//
// Through-shim path (AWS frontend example):
//
//	AWS SDK EC2 → shim's EC2 frontend → domain.Networking
//	    → shim's AWS EC2 backend → sockerless's EC2 sim.
//
// VPC / subnet / security-group / Elastic IP API calls are pure
// metadata — no Firecracker required. The instance lifecycle lane
// (RunInstances → poll running → TerminateInstances) requires
// Firecracker + KVM; sockerless #373/#374/#375 closed by PR #372.
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
	"time"

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

// TestSockerless_EC2_Instances_ThroughShim drives:
//
//	AWS SDK → shim's EC2 frontend → AWS EC2 backend → sockerless EC2 sim.
//
// Instance lifecycle: VPC + subnet prereqs, RunInstances, poll until
// running, TerminateInstances. Requires Firecracker + KVM on the host
// (sockerless #373/#374/#375 now closed; lane unblocked as of PR #372).
func TestSockerless_EC2_Instances_ThroughShim(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	backendClient := newSockerlessAWSEC2Client(t, endpoint)
	backend := awsbackend.New(backendClient)
	shim := harness.StartComputeServerAWS(t, backend)
	frontendClient := newEC2Client(t, shim.URL)
	ctx := context.Background()

	// Create VPC + subnet as prerequisites for RunInstances.
	vpc, err := frontendClient.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{
		CidrBlock: awsapi.String("10.10.0.0/16"),
	})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() {
		frontendClient.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}) //nolint:errcheck
	})

	subnet, err := frontendClient.CreateSubnet(ctx, &awsec2sdk.CreateSubnetInput{
		VpcId:            awsapi.String(vpcID),
		CidrBlock:        awsapi.String("10.10.1.0/24"),
		AvailabilityZone: awsapi.String("us-east-1a"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subnetID := awsapi.ToString(subnet.Subnet.SubnetId)
	t.Cleanup(func() {
		frontendClient.DeleteSubnet(ctx, &awsec2sdk.DeleteSubnetInput{SubnetId: awsapi.String(subnetID)}) //nolint:errcheck
	})

	// RunInstances — triggers a real Firecracker VM boot inside sockerless.
	run, err := frontendClient.RunInstances(ctx, &awsec2sdk.RunInstancesInput{
		ImageId:      awsapi.String("ami-simulated"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     awsapi.Int32(1),
		MaxCount:     awsapi.Int32(1),
		SubnetId:     awsapi.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	if len(run.Instances) != 1 {
		t.Fatalf("RunInstances: expected 1 instance, got %d", len(run.Instances))
	}
	instanceID := awsapi.ToString(run.Instances[0].InstanceId)
	t.Cleanup(func() {
		frontendClient.TerminateInstances(ctx, &awsec2sdk.TerminateInstancesInput{ //nolint:errcheck
			InstanceIds: []string{instanceID},
		})
	})

	// Poll DescribeInstances until running (Firecracker boot takes ~10–30s).
	t.Logf("waiting for instance %s to reach running state", instanceID)
	running := false
	for range 60 { // up to 60 × 2s = 2 minutes
		desc, err := frontendClient.DescribeInstances(ctx, &awsec2sdk.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			t.Fatalf("DescribeInstances: %v", err)
		}
		if len(desc.Reservations) > 0 && len(desc.Reservations[0].Instances) > 0 {
			state := desc.Reservations[0].Instances[0].State.Name
			if state == ec2types.InstanceStateNameRunning {
				running = true
				break
			}
			if state == ec2types.InstanceStateNameTerminated || state == ec2types.InstanceStateNameStopped {
				t.Fatalf("instance reached unexpected terminal state %q before running", state)
			}
		}
		waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		<-waitCtx.Done()
		cancel()
	}
	if !running {
		t.Fatalf("instance %s did not reach running state within 2 minutes", instanceID)
	}

	// TerminateInstances.
	term, err := frontendClient.TerminateInstances(ctx, &awsec2sdk.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		t.Fatalf("TerminateInstances: %v", err)
	}
	if len(term.TerminatingInstances) == 0 {
		t.Fatal("TerminateInstances: empty state list")
	}
	finalState := term.TerminatingInstances[0].CurrentState.Name
	if finalState != ec2types.InstanceStateNameTerminated && finalState != ec2types.InstanceStateNameShuttingDown {
		t.Errorf("TerminateInstances state = %v, want terminated or shutting-down", finalState)
	}
}

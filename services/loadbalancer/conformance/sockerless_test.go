// Sockerless lane for the load balancer service, Phase 16.D.
//
// LB create/describe/delete + RegisterTargets operations are all supported.
// RegisterTargets itself is pure metadata (stores instance IDs in the target
// group); DescribeTargetHealth returns healthy/unhealthy based on actual
// connectivity. The EC2 instance created for RegisterTargets goes through
// RunInstances → pending state (no wait for Firecracker boot required for
// the registration API path itself). Sockerless #373/#374/#375 are closed.
//
// Set SOCKERLESS_AWS_ENDPOINT to opt in.
package conformance_test

import (
	"context"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awsec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2sdk "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/e6qu/shimanism/internal/harness"
	awscomputebackend "github.com/e6qu/shimanism/services/compute/backends/aws"
	awslbbackend "github.com/e6qu/shimanism/services/loadbalancer/backends/aws"
)

// TestSockerless_ELBv2_Through_Shim_LBLifecycle drives:
//
//	AWS SDK ELBv2 → shim's ELBv2 frontend → AWS ELBv2 backend → sockerless.
//
// LB create/describe/delete — no Firecracker required.
func TestSockerless_ELBv2_Through_Shim_LBLifecycle(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	// Backend leg: shim's AWS ELBv2 backend → sockerless.
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	backendELB := elbv2sdk.NewFromConfig(cfg, func(o *elbv2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backendEC2 := awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awslbbackend.New(backendELB, backendEC2)
	shim := harness.StartLoadBalancerServerAWS(t, backend)

	// Frontend leg: official ELBv2 SDK → shim.
	frontendClient := newELBv2Client(t, shim.URL)
	ctx := context.Background()

	// CreateLoadBalancer through shim → sockerless.
	create, err := frontendClient.CreateLoadBalancer(ctx, &elbv2sdk.CreateLoadBalancerInput{
		Name: awsapi.String("shim-nlb"),
		Type: elbv2types.LoadBalancerTypeEnumNetwork,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer (through shim → sockerless): %v", err)
	}
	lbARN := awsapi.ToString(create.LoadBalancers[0].LoadBalancerArn)
	t.Cleanup(func() {
		frontendClient.DeleteLoadBalancer(ctx, &elbv2sdk.DeleteLoadBalancerInput{
			LoadBalancerArn: awsapi.String(lbARN),
		})
	})

	// DescribeLoadBalancers
	desc, err := frontendClient.DescribeLoadBalancers(ctx, &elbv2sdk.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers: %v", err)
	}
	if len(desc.LoadBalancers) != 1 {
		t.Errorf("DescribeLoadBalancers count = %d, want 1", len(desc.LoadBalancers))
	}

	// DeleteLoadBalancer
	if _, err := frontendClient.DeleteLoadBalancer(ctx, &elbv2sdk.DeleteLoadBalancerInput{
		LoadBalancerArn: awsapi.String(lbARN),
	}); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}
}

// TestSockerless_ELBv2_Through_Shim_RegisterTargets drives:
//
//	AWS SDK ELBv2 → shim's ELBv2 frontend → AWS ELBv2 backend → sockerless.
//
// Full RegisterTargets path: VPC + subnet + EC2 instance (pending state,
// no Firecracker wait needed for the registration API), target group,
// RegisterTargets, DescribeTargetHealth (returns data regardless of health
// state), DeregisterTargets. Sockerless #373/#374/#375 closed by PR #372.
func TestSockerless_ELBv2_Through_Shim_RegisterTargets(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}

	// Build shared AWS config pointing at sockerless.
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	ec2Direct := awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	elbDirect := elbv2sdk.NewFromConfig(cfg, func(o *elbv2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})

	// Shim LB server (ELBv2 frontend → AWS LB backend → sockerless).
	lbBackend := awslbbackend.New(elbDirect, ec2Direct)
	lbShim := harness.StartLoadBalancerServerAWS(t, lbBackend)
	lbClient := newELBv2Client(t, lbShim.URL)

	// Shim EC2 server (EC2 frontend → AWS EC2 backend → sockerless).
	// Used to create the VPC, subnet, and EC2 instance for target registration.
	ec2Direct2 := awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	ec2Shim := harness.StartComputeServerAWS(t, awscomputebackend.New(ec2Direct2))
	ec2Client := awsec2sdk.NewFromConfig(cfg, func(o *awsec2sdk.Options) {
		o.BaseEndpoint = awsapi.String(ec2Shim.URL)
	})

	ctx := context.Background()

	// Create VPC + subnet.
	vpc, err := ec2Client.CreateVpc(ctx, &awsec2sdk.CreateVpcInput{CidrBlock: awsapi.String("10.20.0.0/16")})
	if err != nil {
		t.Fatalf("CreateVpc: %v", err)
	}
	vpcID := awsapi.ToString(vpc.Vpc.VpcId)
	t.Cleanup(func() { ec2Client.DeleteVpc(ctx, &awsec2sdk.DeleteVpcInput{VpcId: awsapi.String(vpcID)}) }) //nolint:errcheck

	sn, err := ec2Client.CreateSubnet(ctx, &awsec2sdk.CreateSubnetInput{
		VpcId:     awsapi.String(vpcID),
		CidrBlock: awsapi.String("10.20.1.0/24"),
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %v", err)
	}
	subnetID := awsapi.ToString(sn.Subnet.SubnetId)
	t.Cleanup(func() { ec2Client.DeleteSubnet(ctx, &awsec2sdk.DeleteSubnetInput{SubnetId: awsapi.String(subnetID)}) }) //nolint:errcheck

	// RunInstances — creates instance in pending state (no Firecracker wait).
	run, err := ec2Client.RunInstances(ctx, &awsec2sdk.RunInstancesInput{
		ImageId:      awsapi.String("ami-simulated"),
		InstanceType: ec2types.InstanceTypeT3Micro,
		MinCount:     awsapi.Int32(1),
		MaxCount:     awsapi.Int32(1),
		SubnetId:     awsapi.String(subnetID),
	})
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}
	instanceID := awsapi.ToString(run.Instances[0].InstanceId)
	t.Cleanup(func() {
		ec2Client.TerminateInstances(ctx, &awsec2sdk.TerminateInstancesInput{InstanceIds: []string{instanceID}}) //nolint:errcheck
	})

	// Create LB + target group through the LB shim.
	lb, err := lbClient.CreateLoadBalancer(ctx, &elbv2sdk.CreateLoadBalancerInput{
		Name: awsapi.String("shim-rt-nlb"),
		Type: elbv2types.LoadBalancerTypeEnumNetwork,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}
	lbARN := awsapi.ToString(lb.LoadBalancers[0].LoadBalancerArn)
	t.Cleanup(func() {
		lbClient.DeleteLoadBalancer(ctx, &elbv2sdk.DeleteLoadBalancerInput{LoadBalancerArn: awsapi.String(lbARN)}) //nolint:errcheck
	})

	tg, err := lbClient.CreateTargetGroup(ctx, &elbv2sdk.CreateTargetGroupInput{
		Name:       awsapi.String("shim-rt-tg"),
		Protocol:   elbv2types.ProtocolEnumTcp,
		Port:       awsapi.Int32(80),
		VpcId:      awsapi.String(vpcID),
		TargetType: elbv2types.TargetTypeEnumInstance,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}
	tgARN := awsapi.ToString(tg.TargetGroups[0].TargetGroupArn)
	t.Cleanup(func() {
		lbClient.DeleteTargetGroup(ctx, &elbv2sdk.DeleteTargetGroupInput{TargetGroupArn: awsapi.String(tgARN)}) //nolint:errcheck
	})

	// RegisterTargets.
	if _, err := lbClient.RegisterTargets(ctx, &elbv2sdk.RegisterTargetsInput{
		TargetGroupArn: awsapi.String(tgARN),
		Targets: []elbv2types.TargetDescription{{
			Id:   awsapi.String(instanceID),
			Port: awsapi.Int32(80),
		}},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	// DescribeTargetHealth — verifies the target appears (health state varies).
	health, err := lbClient.DescribeTargetHealth(ctx, &elbv2sdk.DescribeTargetHealthInput{
		TargetGroupArn: awsapi.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}
	found := false
	for _, th := range health.TargetHealthDescriptions {
		if awsapi.ToString(th.Target.Id) == instanceID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DescribeTargetHealth: registered instance %q not found in %d entries", instanceID, len(health.TargetHealthDescriptions))
	}

	// DeregisterTargets.
	if _, err := lbClient.DeregisterTargets(ctx, &elbv2sdk.DeregisterTargetsInput{
		TargetGroupArn: awsapi.String(tgARN),
		Targets: []elbv2types.TargetDescription{{
			Id:   awsapi.String(instanceID),
			Port: awsapi.Int32(80),
		}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}
}

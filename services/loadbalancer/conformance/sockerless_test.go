// Sockerless lane for the load balancer service, Phase 16.D.
//
// LB create/describe/delete operations are pure metadata — no Firecracker
// VM boot required. This lane is green immediately.
//
// RegisterTargets (adding instances to a target group) requires Firecracker
// to verify the instance state, so that sub-lane is gated on:
//   - sockerless #373 (DetectFirecrackerCapabilities /dev/kvm check)
//   - sockerless #374 (3 GB rootfs disk exhaustion)
//   - sockerless #375 (asset caching + ubuntu-latest pin)
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
	elbv2sdk "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/e6qu/shimanism/internal/harness"
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

// TestSockerless_ELBv2_Through_Shim_RegisterTargets exercises
// RegisterTargets against sockerless. Gated on #373/#374/#375 closing —
// skip with a clear message until then.
func TestSockerless_ELBv2_Through_Shim_RegisterTargets(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	t.Skip("RegisterTargets sockerless lane gated on sockerless #373/#374/#375 closing — " +
		"instance target validation requires Firecracker VM boot which is blocked by CI disk " +
		"exhaustion (#374) and missing /dev/kvm capability check (#373)")
}

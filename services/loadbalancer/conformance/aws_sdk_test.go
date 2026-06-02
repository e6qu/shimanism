// Conformance: AWS ELBv2-shaped frontend exercised by the official
// aws-sdk-go-v2/service/elasticloadbalancingv2 SDK. The SDK is pointed
// at the shim via BaseEndpoint; SigV4 verifier checks credentials.
//
// Phase 16.D covers create/describe/delete for LBs, target groups, and
// listeners. RegisterTargets lane is covered here but is an in-memory
// operation (no Firecracker dep).
package conformance_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

// newELBv2Client builds an aws-sdk-go-v2 ELBv2 client pointed at the shim.
func newELBv2Client(t *testing.T, endpoint string) *elbv2.Client {
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
	return elbv2.NewFromConfig(cfg, func(o *elbv2.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_ELBv2_LoadBalancerLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// CreateLoadBalancer
	create, err := cli.CreateLoadBalancer(ctx, &elbv2.CreateLoadBalancerInput{
		Name: aws.String("my-nlb"),
		Type: elbv2types.LoadBalancerTypeEnumNetwork,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}
	if len(create.LoadBalancers) != 1 {
		t.Fatalf("CreateLoadBalancer count = %d, want 1", len(create.LoadBalancers))
	}
	lbARN := aws.ToString(create.LoadBalancers[0].LoadBalancerArn)
	if lbARN == "" {
		t.Fatalf("CreateLoadBalancer returned empty ARN")
	}
	if aws.ToString(create.LoadBalancers[0].LoadBalancerName) != "my-nlb" {
		t.Errorf("LB name = %q, want my-nlb", aws.ToString(create.LoadBalancers[0].LoadBalancerName))
	}

	// DescribeLoadBalancers
	desc, err := cli.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers: %v", err)
	}
	if len(desc.LoadBalancers) != 1 {
		t.Errorf("DescribeLoadBalancers count = %d, want 1", len(desc.LoadBalancers))
	}

	// DeleteLoadBalancer
	if _, err := cli.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: aws.String(lbARN),
	}); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}

	// Verify gone
	desc2, err := cli.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers after delete: %v", err)
	}
	if len(desc2.LoadBalancers) != 0 {
		t.Errorf("LB still present after delete")
	}
}

func TestAWSSDK_ELBv2_TargetGroupLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// CreateTargetGroup
	tg, err := cli.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:     aws.String("my-tg"),
		Protocol: elbv2types.ProtocolEnumTcp,
		Port:     aws.Int32(80),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}
	if len(tg.TargetGroups) != 1 {
		t.Fatalf("CreateTargetGroup count = %d, want 1", len(tg.TargetGroups))
	}
	tgARN := aws.ToString(tg.TargetGroups[0].TargetGroupArn)
	if tgARN == "" {
		t.Fatalf("empty TG ARN")
	}

	// RegisterTargets
	if _, err := cli.RegisterTargets(ctx, &elbv2.RegisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets: []elbv2types.TargetDescription{
			{Id: aws.String("i-001"), Port: aws.Int32(8080)},
			{Id: aws.String("i-002"), Port: aws.Int32(8080)},
		},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	// DescribeTargetHealth
	health, err := cli.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
		TargetGroupArn: aws.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth: %v", err)
	}
	if len(health.TargetHealthDescriptions) != 2 {
		t.Errorf("DescribeTargetHealth count = %d, want 2", len(health.TargetHealthDescriptions))
	}

	// DeregisterTargets
	if _, err := cli.DeregisterTargets(ctx, &elbv2.DeregisterTargetsInput{
		TargetGroupArn: aws.String(tgARN),
		Targets:        []elbv2types.TargetDescription{{Id: aws.String("i-001")}},
	}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}

	// DescribeTargetGroups
	dtg, err := cli.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{tgARN},
	})
	if err != nil {
		t.Fatalf("DescribeTargetGroups: %v", err)
	}
	if len(dtg.TargetGroups) != 1 {
		t.Errorf("DescribeTargetGroups count = %d, want 1", len(dtg.TargetGroups))
	}

	// DeleteTargetGroup
	if _, err := cli.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
		TargetGroupArn: aws.String(tgARN),
	}); err != nil {
		t.Fatalf("DeleteTargetGroup: %v", err)
	}
}

func TestAWSSDK_ELBv2_ListenerLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// Create LB + TG
	lb, err := cli.CreateLoadBalancer(ctx, &elbv2.CreateLoadBalancerInput{
		Name: aws.String("listener-nlb"),
		Type: elbv2types.LoadBalancerTypeEnumNetwork,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}
	lbARN := aws.ToString(lb.LoadBalancers[0].LoadBalancerArn)

	tg, err := cli.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:     aws.String("listener-tg"),
		Protocol: elbv2types.ProtocolEnumTcp,
		Port:     aws.Int32(80),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}
	tgARN := aws.ToString(tg.TargetGroups[0].TargetGroupArn)

	// CreateListener
	listener, err := cli.CreateListener(ctx, &elbv2.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbv2types.ProtocolEnumTcp,
		Port:            aws.Int32(80),
		DefaultActions: []elbv2types.Action{{
			Type:           elbv2types.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}
	if len(listener.Listeners) != 1 {
		t.Fatalf("CreateListener count = %d, want 1", len(listener.Listeners))
	}
	listenerARN := aws.ToString(listener.Listeners[0].ListenerArn)

	// DescribeListeners
	dl, err := cli.DescribeListeners(ctx, &elbv2.DescribeListenersInput{
		LoadBalancerArn: aws.String(lbARN),
	})
	if err != nil {
		t.Fatalf("DescribeListeners: %v", err)
	}
	if len(dl.Listeners) != 1 {
		t.Errorf("DescribeListeners count = %d, want 1", len(dl.Listeners))
	}

	// DeleteListener
	if _, err := cli.DeleteListener(ctx, &elbv2.DeleteListenerInput{
		ListenerArn: aws.String(listenerARN),
	}); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}

	// Cleanup
	cli.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(tgARN)})
	cli.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(lbARN)})
}

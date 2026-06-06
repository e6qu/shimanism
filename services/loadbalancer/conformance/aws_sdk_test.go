// Conformance: AWS ELBv2-shaped frontend exercised by the official
// aws-sdk-go-v2/service/elasticloadbalancingv2 SDK. The SDK is pointed
// at the shim via BaseEndpoint; SigV4 verifier checks credentials.
//
// Phase 16.D covers create/describe/delete for LBs, target groups, and
// listeners. RegisterTargets lane is covered here but is an in-memory
// operation (no Firecracker dep).
//
// Phase 21.A extends to ALB (type=application), HTTPS listeners with
// certificates, HTTP target groups with health checks, and L7 routing rules.
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

// TestAWSSDK_ELBv2_ALB_RuleLifecycle covers Phase 21.A: Application LB
// with HTTPS listener, HTTP target group (with health check), and L7 routing rules.
func TestAWSSDK_ELBv2_ALB_RuleLifecycle(t *testing.T) {
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// ── CreateLoadBalancer (type=application) ─────────────────────────
	lbOut, err := cli.CreateLoadBalancer(ctx, &elbv2.CreateLoadBalancerInput{
		Name: aws.String("my-alb"),
		Type: elbv2types.LoadBalancerTypeEnumApplication,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer(application): %v", err)
	}
	if len(lbOut.LoadBalancers) != 1 {
		t.Fatalf("CreateLoadBalancer count = %d, want 1", len(lbOut.LoadBalancers))
	}
	lbARN := aws.ToString(lbOut.LoadBalancers[0].LoadBalancerArn)
	if lbARN == "" {
		t.Fatal("empty ALB ARN")
	}
	if got := string(lbOut.LoadBalancers[0].Type); got != "application" {
		t.Errorf("LB type = %q, want application", got)
	}

	// ── CreateTargetGroup (HTTP with health check) ────────────────────
	tgOut, err := cli.CreateTargetGroup(ctx, &elbv2.CreateTargetGroupInput{
		Name:                aws.String("my-http-tg"),
		Protocol:            elbv2types.ProtocolEnumHttp,
		Port:                aws.Int32(8080),
		HealthCheckPath:     aws.String("/health"),
		HealthCheckProtocol: elbv2types.ProtocolEnumHttp,
		Matcher:             &elbv2types.Matcher{HttpCode: aws.String("200")},
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup(HTTP): %v", err)
	}
	if len(tgOut.TargetGroups) != 1 {
		t.Fatalf("CreateTargetGroup count = %d, want 1", len(tgOut.TargetGroups))
	}
	tgARN := aws.ToString(tgOut.TargetGroups[0].TargetGroupArn)
	if got := aws.ToString(tgOut.TargetGroups[0].HealthCheckPath); got != "/health" {
		t.Errorf("HealthCheckPath = %q, want /health", got)
	}

	// ── CreateListener (HTTPS with certificate) ───────────────────────
	const certARN = "arn:aws:acm:us-east-1:000000000000:certificate/test-cert-id"
	listenerOut, err := cli.CreateListener(ctx, &elbv2.CreateListenerInput{
		LoadBalancerArn: aws.String(lbARN),
		Protocol:        elbv2types.ProtocolEnumHttps,
		Port:            aws.Int32(443),
		Certificates: []elbv2types.Certificate{
			{CertificateArn: aws.String(certARN)},
		},
		DefaultActions: []elbv2types.Action{{
			Type:           elbv2types.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateListener(HTTPS): %v", err)
	}
	if len(listenerOut.Listeners) != 1 {
		t.Fatalf("CreateListener count = %d, want 1", len(listenerOut.Listeners))
	}
	listenerARN := aws.ToString(listenerOut.Listeners[0].ListenerArn)
	if got := string(listenerOut.Listeners[0].Protocol); got != "HTTPS" {
		t.Errorf("listener protocol = %q, want HTTPS", got)
	}
	if len(listenerOut.Listeners[0].Certificates) == 0 {
		t.Error("listener has no certificates")
	} else if got := aws.ToString(listenerOut.Listeners[0].Certificates[0].CertificateArn); got != certARN {
		t.Errorf("certificate ARN = %q, want %q", got, certARN)
	}

	// ── CreateRule (path-pattern → forward) ──────────────────────────
	ruleOut, err := cli.CreateRule(ctx, &elbv2.CreateRuleInput{
		ListenerArn: aws.String(listenerARN),
		Priority:    aws.Int32(10),
		Conditions: []elbv2types.RuleCondition{{
			Field:  aws.String("path-pattern"),
			Values: []string{"/api/*"},
		}},
		Actions: []elbv2types.Action{{
			Type:           elbv2types.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if len(ruleOut.Rules) != 1 {
		t.Fatalf("CreateRule count = %d, want 1", len(ruleOut.Rules))
	}
	ruleARN := aws.ToString(ruleOut.Rules[0].RuleArn)
	if ruleARN == "" {
		t.Fatal("empty rule ARN")
	}
	if got := aws.ToString(ruleOut.Rules[0].Priority); got != "10" {
		t.Errorf("rule priority = %q, want 10", got)
	}

	// ── DescribeRules by listener ─────────────────────────────────────
	descRules, err := cli.DescribeRules(ctx, &elbv2.DescribeRulesInput{
		ListenerArn: aws.String(listenerARN),
	})
	if err != nil {
		t.Fatalf("DescribeRules: %v", err)
	}
	if len(descRules.Rules) != 1 {
		t.Errorf("DescribeRules count = %d, want 1", len(descRules.Rules))
	}

	// ── DescribeRules by ARN ──────────────────────────────────────────
	descByARN, err := cli.DescribeRules(ctx, &elbv2.DescribeRulesInput{
		RuleArns: []string{ruleARN},
	})
	if err != nil {
		t.Fatalf("DescribeRules by ARN: %v", err)
	}
	if len(descByARN.Rules) != 1 {
		t.Errorf("DescribeRules by ARN count = %d, want 1", len(descByARN.Rules))
	}

	// ── ModifyRule ────────────────────────────────────────────────────
	modRule, err := cli.ModifyRule(ctx, &elbv2.ModifyRuleInput{
		RuleArn: aws.String(ruleARN),
		Conditions: []elbv2types.RuleCondition{{
			Field:  aws.String("host-header"),
			Values: []string{"api.example.com"},
		}},
		Actions: []elbv2types.Action{{
			Type:           elbv2types.ActionTypeEnumForward,
			TargetGroupArn: aws.String(tgARN),
		}},
	})
	if err != nil {
		t.Fatalf("ModifyRule: %v", err)
	}
	if len(modRule.Rules) != 1 {
		t.Errorf("ModifyRule count = %d, want 1", len(modRule.Rules))
	}

	// ── DeleteRule ────────────────────────────────────────────────────
	if _, err := cli.DeleteRule(ctx, &elbv2.DeleteRuleInput{
		RuleArn: aws.String(ruleARN),
	}); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	// Rule gone from DescribeRules
	afterDelete, err := cli.DescribeRules(ctx, &elbv2.DescribeRulesInput{
		ListenerArn: aws.String(listenerARN),
	})
	if err != nil {
		t.Fatalf("DescribeRules after delete: %v", err)
	}
	if len(afterDelete.Rules) != 0 {
		t.Errorf("rule still present after DeleteRule")
	}

	// ── Cleanup ───────────────────────────────────────────────────────
	cli.DeleteListener(ctx, &elbv2.DeleteListenerInput{ListenerArn: aws.String(listenerARN)})
	cli.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{TargetGroupArn: aws.String(tgARN)})
	cli.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(lbARN)})
}

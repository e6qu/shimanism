// Conformance: K8s LB peer exercised via the AWS ELBv2 and GCP Compute
// frontends. The K8s peer backs all cloud frontends — same
// domain.LoadBalancers backend serves requests from any frontend.
package conformance_test

import (
	"context"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	elbv2sdk "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"
	"golang.org/x/oauth2"
	computeraw "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/k8slb"
	"k8s.io/client-go/kubernetes/fake"
)

// TestK8sPeer_AWSShaped_LBLifecycle drives the AWS ELBv2 frontend
// against the K8s LB peer.
func TestK8sPeer_AWSShaped_LBLifecycle(t *testing.T) {
	k8s := k8slb.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartLoadBalancerServerAWS(t, k8s)
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// CreateLoadBalancer → K8s Service
	create, err := cli.CreateLoadBalancer(ctx, &elbv2sdk.CreateLoadBalancerInput{
		Name: awsapi.String("k8s-nlb"),
		Type: elbv2types.LoadBalancerTypeEnumNetwork,
	})
	if err != nil {
		t.Fatalf("CreateLoadBalancer (K8s): %v", err)
	}
	lbARN := awsapi.ToString(create.LoadBalancers[0].LoadBalancerArn)
	if lbARN == "" {
		t.Fatalf("empty LB ARN from K8s peer")
	}

	// DescribeLoadBalancers
	desc, err := cli.DescribeLoadBalancers(ctx, &elbv2sdk.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{lbARN},
	})
	if err != nil {
		t.Fatalf("DescribeLoadBalancers (K8s): %v", err)
	}
	if len(desc.LoadBalancers) != 1 {
		t.Errorf("DescribeLoadBalancers count = %d, want 1", len(desc.LoadBalancers))
	}

	// DeleteLoadBalancer
	if _, err := cli.DeleteLoadBalancer(ctx, &elbv2sdk.DeleteLoadBalancerInput{
		LoadBalancerArn: awsapi.String(lbARN),
	}); err != nil {
		t.Fatalf("DeleteLoadBalancer (K8s): %v", err)
	}
}

// TestK8sPeer_AWSShaped_TargetGroupLifecycle drives TG lifecycle
// against the K8s peer.
func TestK8sPeer_AWSShaped_TargetGroupLifecycle(t *testing.T) {
	k8s := k8slb.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartLoadBalancerServerAWS(t, k8s)
	cli := newELBv2Client(t, srv.URL)
	ctx := context.Background()

	// CreateTargetGroup → K8s Endpoints
	tg, err := cli.CreateTargetGroup(ctx, &elbv2sdk.CreateTargetGroupInput{
		Name:     awsapi.String("k8s-tg"),
		Protocol: elbv2types.ProtocolEnumTcp,
		Port:     awsapi.Int32(80),
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup (K8s): %v", err)
	}
	tgARN := awsapi.ToString(tg.TargetGroups[0].TargetGroupArn)

	// RegisterTargets → K8s Endpoints.Subsets
	if _, err := cli.RegisterTargets(ctx, &elbv2sdk.RegisterTargetsInput{
		TargetGroupArn: awsapi.String(tgARN),
		Targets: []elbv2types.TargetDescription{
			{Id: awsapi.String("10.0.0.1"), Port: awsapi.Int32(8080)},
		},
	}); err != nil {
		t.Fatalf("RegisterTargets (K8s): %v", err)
	}

	// DescribeTargetHealth
	health, err := cli.DescribeTargetHealth(ctx, &elbv2sdk.DescribeTargetHealthInput{
		TargetGroupArn: awsapi.String(tgARN),
	})
	if err != nil {
		t.Fatalf("DescribeTargetHealth (K8s): %v", err)
	}
	if len(health.TargetHealthDescriptions) != 1 {
		t.Errorf("DescribeTargetHealth count = %d, want 1", len(health.TargetHealthDescriptions))
	}

	// DeleteTargetGroup
	if _, err := cli.DeleteTargetGroup(ctx, &elbv2sdk.DeleteTargetGroupInput{
		TargetGroupArn: awsapi.String(tgARN),
	}); err != nil {
		t.Fatalf("DeleteTargetGroup (K8s): %v", err)
	}
}

// TestK8sPeer_GCPShaped_LBLifecycle drives the GCP Compute LB frontend
// against the K8s peer.
func TestK8sPeer_GCPShaped_LBLifecycle(t *testing.T) {
	k8s := k8slb.New(fake.NewSimpleClientset(), "default")
	srv := harness.StartLoadBalancerServerGCP(t, k8s)

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := computeraw.NewService(t.Context(),
		option.WithEndpoint(srv.URL),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build compute client: %v", err)
	}

	// Insert forwarding rule → K8s Service
	if _, err := svc.ForwardingRules.Insert("shim", "us-central1", &computeraw.ForwardingRule{
		Name:       "k8s-fr",
		IPProtocol: "TCP",
	}).Do(); err != nil {
		t.Fatalf("ForwardingRules.Insert (K8s): %v", err)
	}
	t.Cleanup(func() {
		svc.ForwardingRules.Delete("shim", "us-central1", "k8s-fr").Do()
	})

	// List
	list, err := svc.ForwardingRules.List("shim", "us-central1").Do()
	if err != nil {
		t.Fatalf("ForwardingRules.List (K8s): %v", err)
	}
	found := false
	for _, fr := range list.Items {
		if fr.Name == "k8s-fr" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ForwardingRules.List did not find k8s-fr")
	}
}

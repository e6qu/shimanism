package k8slb_test

import (
	"context"
	"testing"

	"k8s.io/client-go/kubernetes/fake"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/k8slb"
)

func TestK8sLB_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := k8slb.New(fake.NewSimpleClientset(), "default")

	// CreateLoadBalancer → Service
	lb, err := b.CreateLoadBalancer(ctx, "my-nlb", domain.CreateLoadBalancerOptions{})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}
	if lb.Name != "my-nlb" {
		t.Fatalf("unexpected LB: %+v", lb)
	}

	// GetLoadBalancer
	got, err := b.GetLoadBalancer(ctx, lb.ID)
	if err != nil || got.Name != "my-nlb" {
		t.Fatalf("GetLoadBalancer: %v %+v", err, got)
	}

	// ListLoadBalancers
	res, err := b.ListLoadBalancers(ctx, domain.ListLoadBalancersOptions{})
	if err != nil || len(res.LoadBalancers) != 1 {
		t.Fatalf("ListLoadBalancers: %v count=%d", err, len(res.LoadBalancers))
	}

	// DeleteLoadBalancer
	if err := b.DeleteLoadBalancer(ctx, lb.ID); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}
	if _, err := b.GetLoadBalancer(ctx, lb.ID); !domain.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got: %v", err)
	}
}

func TestK8sLB_TargetGroup_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := k8slb.New(fake.NewSimpleClientset(), "default")

	// CreateTargetGroup → Endpoints
	tg, err := b.CreateTargetGroup(ctx, "my-tg", domain.CreateTargetGroupOptions{
		Protocol: domain.ProtocolTCP,
		Port:     80,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}
	if tg.Name != "my-tg" {
		t.Fatalf("unexpected TG: %+v", tg)
	}

	// RegisterTargets
	if err := b.RegisterTargets(ctx, tg.ID, []domain.Target{
		{ID: "10.0.0.1", Port: 8080},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	got, err := b.GetTargetGroup(ctx, tg.ID)
	if err != nil || len(got.Targets) != 1 {
		t.Fatalf("GetTargetGroup: %v targets=%d", err, len(got.Targets))
	}

	// DeregisterTargets
	if err := b.DeregisterTargets(ctx, tg.ID, []domain.Target{{ID: "10.0.0.1"}}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}
	got, _ = b.GetTargetGroup(ctx, tg.ID)
	if len(got.Targets) != 0 {
		t.Errorf("targets after deregister: %+v", got.Targets)
	}

	// DeleteTargetGroup
	if err := b.DeleteTargetGroup(ctx, tg.ID); err != nil {
		t.Fatalf("DeleteTargetGroup: %v", err)
	}
}

func TestK8sLB_Listener_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := k8slb.New(fake.NewSimpleClientset(), "default")

	lb, _ := b.CreateLoadBalancer(ctx, "nlb", domain.CreateLoadBalancerOptions{})

	l, err := b.CreateListener(ctx, domain.CreateListenerOptions{
		LoadBalancerID: lb.ID,
		Protocol:       domain.ProtocolTCP,
		Port:           80,
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}
	if l.Port != 80 {
		t.Errorf("Listener port = %d, want 80", l.Port)
	}

	res, err := b.ListListeners(ctx, domain.ListListenersOptions{LoadBalancerID: lb.ID})
	if err != nil || len(res.Listeners) != 1 {
		t.Fatalf("ListListeners: %v count=%d", err, len(res.Listeners))
	}
}

package inmem_test

import (
	"context"
	"testing"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

func TestLB_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	lb, err := b.CreateLoadBalancer(ctx, "my-nlb", domain.CreateLoadBalancerOptions{})
	if err != nil {
		t.Fatalf("CreateLoadBalancer: %v", err)
	}
	if lb.Name != "my-nlb" || lb.ID == "" {
		t.Fatalf("unexpected LB: %+v", lb)
	}

	got, err := b.GetLoadBalancer(ctx, lb.ID)
	if err != nil || got.Name != "my-nlb" {
		t.Fatalf("GetLoadBalancer: %v %+v", err, got)
	}

	res, err := b.ListLoadBalancers(ctx, domain.ListLoadBalancersOptions{})
	if err != nil || len(res.LoadBalancers) != 1 {
		t.Fatalf("ListLoadBalancers: %v count=%d", err, len(res.LoadBalancers))
	}

	_, err = b.CreateLoadBalancer(ctx, "my-nlb", domain.CreateLoadBalancerOptions{})
	if !domain.IsAlreadyExists(err) {
		t.Errorf("expected AlreadyExists on duplicate, got: %v", err)
	}

	if err := b.DeleteLoadBalancer(ctx, lb.ID); err != nil {
		t.Fatalf("DeleteLoadBalancer: %v", err)
	}
	if _, err := b.GetLoadBalancer(ctx, lb.ID); !domain.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got: %v", err)
	}
}

func TestTargetGroup_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	tg, err := b.CreateTargetGroup(ctx, "my-tg", domain.CreateTargetGroupOptions{
		Protocol: domain.ProtocolTCP,
		Port:     80,
	})
	if err != nil {
		t.Fatalf("CreateTargetGroup: %v", err)
	}
	if tg.Name != "my-tg" || tg.Port != 80 {
		t.Fatalf("unexpected TG: %+v", tg)
	}

	if err := b.RegisterTargets(ctx, tg.ID, []domain.Target{
		{ID: "i-001", Port: 8080},
		{ID: "i-002", Port: 8080},
	}); err != nil {
		t.Fatalf("RegisterTargets: %v", err)
	}

	got, err := b.GetTargetGroup(ctx, tg.ID)
	if err != nil || len(got.Targets) != 2 {
		t.Fatalf("GetTargetGroup after register: %v targets=%d", err, len(got.Targets))
	}

	if err := b.DeregisterTargets(ctx, tg.ID, []domain.Target{{ID: "i-001"}}); err != nil {
		t.Fatalf("DeregisterTargets: %v", err)
	}
	got, _ = b.GetTargetGroup(ctx, tg.ID)
	if len(got.Targets) != 1 || got.Targets[0].ID != "i-002" {
		t.Errorf("targets after deregister: %+v", got.Targets)
	}

	if err := b.DeleteTargetGroup(ctx, tg.ID); err != nil {
		t.Fatalf("DeleteTargetGroup: %v", err)
	}
}

func TestListener_Lifecycle(t *testing.T) {
	ctx := context.Background()
	b := inmem.New()

	lb, _ := b.CreateLoadBalancer(ctx, "nlb", domain.CreateLoadBalancerOptions{})
	tg, _ := b.CreateTargetGroup(ctx, "tg", domain.CreateTargetGroupOptions{Port: 80})

	l, err := b.CreateListener(ctx, domain.CreateListenerOptions{
		LoadBalancerID: lb.ID,
		Protocol:       domain.ProtocolTCP,
		Port:           80,
		TargetGroupID:  tg.ID,
	})
	if err != nil {
		t.Fatalf("CreateListener: %v", err)
	}
	if l.Port != 80 || l.LoadBalancerID != lb.ID {
		t.Fatalf("unexpected Listener: %+v", l)
	}

	res, err := b.ListListeners(ctx, domain.ListListenersOptions{LoadBalancerID: lb.ID})
	if err != nil || len(res.Listeners) != 1 {
		t.Fatalf("ListListeners: %v count=%d", err, len(res.Listeners))
	}

	if err := b.DeleteListener(ctx, l.ID); err != nil {
		t.Fatalf("DeleteListener: %v", err)
	}

	// Listener requires existing LB.
	_, err = b.CreateListener(ctx, domain.CreateListenerOptions{LoadBalancerID: "nonexistent"})
	if !domain.IsNotFound(err) {
		t.Errorf("expected NotFound for missing LB, got: %v", err)
	}
}

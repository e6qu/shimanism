// Package inmem provides an in-memory load balancer backend for tests.
package inmem

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

// Backend implements domain.LoadBalancers entirely in memory.
type Backend struct {
	mu sync.RWMutex

	lbs  map[string]*domain.LoadBalancer
	tgs  map[string]*domain.TargetGroup
	lsns map[string]*domain.Listener

	lbSeq  int
	tgSeq  int
	lsnSeq int
}

// New returns an empty in-memory load balancer backend.
func New() *Backend {
	return &Backend{
		lbs:  map[string]*domain.LoadBalancer{},
		tgs:  map[string]*domain.TargetGroup{},
		lsns: map[string]*domain.Listener{},
	}
}

var _ domain.LoadBalancers = (*Backend)(nil)

func (b *Backend) nextID(prefix string, seq *int) string {
	*seq++
	return fmt.Sprintf("%s-%08d", prefix, *seq)
}

func copyTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ─── LoadBalancer lifecycle ──────────────────────────────────────────

func (b *Backend) CreateLoadBalancer(_ context.Context, name string, opt domain.CreateLoadBalancerOptions) (domain.LoadBalancer, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, lb := range b.lbs {
		if lb.Name == name {
			return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", name, domain.ErrAlreadyExists)
		}
	}
	lbType := opt.Type
	if lbType == "" {
		lbType = domain.LoadBalancerTypeNetwork
	}
	lb := &domain.LoadBalancer{
		ID:      b.nextID("lb", &b.lbSeq),
		Name:    name,
		Type:    lbType,
		DNSName: name + ".elb.us-east-1.amazonaws.com",
		State:   domain.LoadBalancerStateActive,
		VpcID:   opt.VpcID,
		Tags:    copyTags(opt.Tags),
	}
	b.lbs[lb.ID] = lb
	return *lb, nil
}

func (b *Backend) GetLoadBalancer(_ context.Context, id string) (domain.LoadBalancer, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	lb, ok := b.lbs[id]
	if !ok {
		return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
	}
	return *lb, nil
}

func (b *Backend) ListLoadBalancers(_ context.Context, opt domain.ListLoadBalancersOptions) (domain.ListLoadBalancersResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	nameSet := make(map[string]bool, len(opt.Names))
	for _, n := range opt.Names {
		nameSet[n] = true
	}
	var out []domain.LoadBalancer
	for _, lb := range b.lbs {
		if len(idSet) > 0 && !idSet[lb.ID] {
			continue
		}
		if len(nameSet) > 0 && !nameSet[lb.Name] {
			continue
		}
		out = append(out, *lb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return domain.ListLoadBalancersResult{LoadBalancers: out}, nil
}

func (b *Backend) DeleteLoadBalancer(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.lbs[id]; !ok {
		return fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
	}
	delete(b.lbs, id)
	return nil
}

// ─── TargetGroup lifecycle ───────────────────────────────────────────

func (b *Backend) CreateTargetGroup(_ context.Context, name string, opt domain.CreateTargetGroupOptions) (domain.TargetGroup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, tg := range b.tgs {
		if tg.Name == name {
			return domain.TargetGroup{}, fmt.Errorf("target group %q: %w", name, domain.ErrAlreadyExists)
		}
	}
	proto := opt.Protocol
	if proto == "" {
		proto = domain.ProtocolTCP
	}
	tg := &domain.TargetGroup{
		ID:       b.nextID("tg", &b.tgSeq),
		Name:     name,
		Protocol: proto,
		Port:     opt.Port,
		VpcID:    opt.VpcID,
		Tags:     copyTags(opt.Tags),
	}
	b.tgs[tg.ID] = tg
	return *tg, nil
}

func (b *Backend) GetTargetGroup(_ context.Context, id string) (domain.TargetGroup, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	tg, ok := b.tgs[id]
	if !ok {
		return domain.TargetGroup{}, fmt.Errorf("target group %q: %w", id, domain.ErrNotFound)
	}
	return *tg, nil
}

func (b *Backend) ListTargetGroups(_ context.Context, opt domain.ListTargetGroupsOptions) (domain.ListTargetGroupsResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var out []domain.TargetGroup
	for _, tg := range b.tgs {
		if len(idSet) > 0 && !idSet[tg.ID] {
			continue
		}
		out = append(out, *tg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return domain.ListTargetGroupsResult{TargetGroups: out}, nil
}

func (b *Backend) DeleteTargetGroup(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.tgs[id]; !ok {
		return fmt.Errorf("target group %q: %w", id, domain.ErrNotFound)
	}
	delete(b.tgs, id)
	return nil
}

// ─── Target registration ─────────────────────────────────────────────

func (b *Backend) RegisterTargets(_ context.Context, targetGroupID string, targets []domain.Target) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	tg, ok := b.tgs[targetGroupID]
	if !ok {
		return fmt.Errorf("target group %q: %w", targetGroupID, domain.ErrNotFound)
	}
	existing := make(map[string]bool, len(tg.Targets))
	for _, t := range tg.Targets {
		existing[t.ID] = true
	}
	for _, t := range targets {
		if !existing[t.ID] {
			tg.Targets = append(tg.Targets, t)
			existing[t.ID] = true
		}
	}
	return nil
}

func (b *Backend) DeregisterTargets(_ context.Context, targetGroupID string, targets []domain.Target) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	tg, ok := b.tgs[targetGroupID]
	if !ok {
		return fmt.Errorf("target group %q: %w", targetGroupID, domain.ErrNotFound)
	}
	remove := make(map[string]bool, len(targets))
	for _, t := range targets {
		remove[t.ID] = true
	}
	var kept []domain.Target
	for _, t := range tg.Targets {
		if !remove[t.ID] {
			kept = append(kept, t)
		}
	}
	tg.Targets = kept
	return nil
}

// ─── Listener lifecycle ──────────────────────────────────────────────

func (b *Backend) CreateListener(_ context.Context, opt domain.CreateListenerOptions) (domain.Listener, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.lbs[opt.LoadBalancerID]; !ok {
		return domain.Listener{}, fmt.Errorf("load balancer %q: %w", opt.LoadBalancerID, domain.ErrNotFound)
	}
	proto := opt.Protocol
	if proto == "" {
		proto = domain.ProtocolTCP
	}
	l := &domain.Listener{
		ID:             b.nextID("lsn", &b.lsnSeq),
		LoadBalancerID: opt.LoadBalancerID,
		Protocol:       proto,
		Port:           opt.Port,
		TargetGroupID:  opt.TargetGroupID,
		Tags:           copyTags(opt.Tags),
	}
	b.lsns[l.ID] = l
	return *l, nil
}

func (b *Backend) GetListener(_ context.Context, id string) (domain.Listener, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	l, ok := b.lsns[id]
	if !ok {
		return domain.Listener{}, fmt.Errorf("listener %q: %w", id, domain.ErrNotFound)
	}
	return *l, nil
}

func (b *Backend) ListListeners(_ context.Context, opt domain.ListListenersOptions) (domain.ListListenersResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var out []domain.Listener
	for _, l := range b.lsns {
		if opt.LoadBalancerID != "" && l.LoadBalancerID != opt.LoadBalancerID {
			continue
		}
		if len(idSet) > 0 && !idSet[l.ID] {
			continue
		}
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return domain.ListListenersResult{Listeners: out}, nil
}

func (b *Backend) DeleteListener(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.lsns[id]; !ok {
		return fmt.Errorf("listener %q: %w", id, domain.ErrNotFound)
	}
	delete(b.lsns, id)
	return nil
}

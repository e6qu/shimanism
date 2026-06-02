// Package k8slb is the Kubernetes peer for shimanism's load balancer
// service (Phase 16.D).
//
// K8s primitives used:
//   - Service (type:LoadBalancer) → domain.LoadBalancer
//   - Endpoints → domain.TargetGroup + target registration
//
// Stateless: every Describe re-reads the K8s API.
package k8slb

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

// Backend implements domain.LoadBalancers via K8s Service + Endpoints.
type Backend struct {
	cs        kubernetes.Interface
	namespace string
}

// New returns a Backend bound to the given Kubernetes client.
func New(cs kubernetes.Interface, namespace string) *Backend {
	if namespace == "" {
		namespace = "default"
	}
	return &Backend{cs: cs, namespace: namespace}
}

var _ domain.LoadBalancers = (*Backend)(nil)

// ─── LoadBalancer (Service type:LoadBalancer) ─────────────────────────

func (b *Backend) CreateLoadBalancer(ctx context.Context, name string, opt domain.CreateLoadBalancerOptions) (domain.LoadBalancer, error) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.namespace,
			Labels:    map[string]string{"shimanism.io/managed": "true"},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{"app": name},
		},
	}
	created, err := b.cs.CoreV1().Services(b.namespace).Create(ctx, svc, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", name, domain.ErrAlreadyExists)
		}
		return domain.LoadBalancer{}, err
	}
	return svcToDomain(created), nil
}

func (b *Backend) GetLoadBalancer(ctx context.Context, id string) (domain.LoadBalancer, error) {
	svc, err := b.cs.CoreV1().Services(b.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
		}
		return domain.LoadBalancer{}, err
	}
	return svcToDomain(svc), nil
}

func (b *Backend) ListLoadBalancers(ctx context.Context, opt domain.ListLoadBalancersOptions) (domain.ListLoadBalancersResult, error) {
	list, err := b.cs.CoreV1().Services(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "shimanism.io/managed=true",
	})
	if err != nil {
		return domain.ListLoadBalancersResult{}, err
	}
	nameSet := make(map[string]bool, len(opt.Names))
	for _, n := range opt.Names {
		nameSet[n] = true
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var lbs []domain.LoadBalancer
	for _, svc := range list.Items {
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		lb := svcToDomain(&svc)
		if len(nameSet) > 0 && !nameSet[lb.Name] {
			continue
		}
		if len(idSet) > 0 && !idSet[lb.ID] {
			continue
		}
		lbs = append(lbs, lb)
	}
	return domain.ListLoadBalancersResult{LoadBalancers: lbs}, nil
}

func (b *Backend) DeleteLoadBalancer(ctx context.Context, id string) error {
	err := b.cs.CoreV1().Services(b.namespace).Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
		}
		return err
	}
	return nil
}

// ─── TargetGroup (Endpoints) ──────────────────────────────────────────

func (b *Backend) CreateTargetGroup(ctx context.Context, name string, opt domain.CreateTargetGroupOptions) (domain.TargetGroup, error) {
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.namespace,
			Labels:    map[string]string{"shimanism.io/managed": "true"},
		},
	}
	created, err := b.cs.CoreV1().Endpoints(b.namespace).Create(ctx, ep, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.TargetGroup{}, fmt.Errorf("target group %q: %w", name, domain.ErrAlreadyExists)
		}
		return domain.TargetGroup{}, err
	}
	return epToDomain(created, opt.Protocol, opt.Port), nil
}

func (b *Backend) GetTargetGroup(ctx context.Context, id string) (domain.TargetGroup, error) {
	ep, err := b.cs.CoreV1().Endpoints(b.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.TargetGroup{}, fmt.Errorf("target group %q: %w", id, domain.ErrNotFound)
		}
		return domain.TargetGroup{}, err
	}
	return epToDomain(ep, domain.ProtocolTCP, 0), nil
}

func (b *Backend) ListTargetGroups(ctx context.Context, opt domain.ListTargetGroupsOptions) (domain.ListTargetGroupsResult, error) {
	list, err := b.cs.CoreV1().Endpoints(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "shimanism.io/managed=true",
	})
	if err != nil {
		return domain.ListTargetGroupsResult{}, err
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var tgs []domain.TargetGroup
	for _, ep := range list.Items {
		tg := epToDomain(&ep, domain.ProtocolTCP, 0)
		if len(idSet) > 0 && !idSet[tg.ID] {
			continue
		}
		tgs = append(tgs, tg)
	}
	return domain.ListTargetGroupsResult{TargetGroups: tgs}, nil
}

func (b *Backend) DeleteTargetGroup(ctx context.Context, id string) error {
	err := b.cs.CoreV1().Endpoints(b.namespace).Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("target group %q: %w", id, domain.ErrNotFound)
		}
		return err
	}
	return nil
}

// ─── Target registration (Endpoint subsets) ───────────────────────────

func (b *Backend) RegisterTargets(ctx context.Context, targetGroupID string, targets []domain.Target) error {
	ep, err := b.cs.CoreV1().Endpoints(b.namespace).Get(ctx, targetGroupID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("target group %q: %w", targetGroupID, domain.ErrNotFound)
		}
		return err
	}
	existing := endpointAddressSet(ep)
	var newAddresses []corev1.EndpointAddress
	for _, t := range targets {
		if !existing[t.ID] {
			newAddresses = append(newAddresses, corev1.EndpointAddress{IP: t.ID})
		}
	}
	if len(newAddresses) == 0 {
		return nil
	}
	port := intstr.FromInt32(80)
	proto := corev1.ProtocolTCP
	if len(ep.Subsets) == 0 {
		ep.Subsets = []corev1.EndpointSubset{{
			Addresses: newAddresses,
			Ports:     []corev1.EndpointPort{{Port: 80, Protocol: proto, Name: "tcp"}},
		}}
	} else {
		ep.Subsets[0].Addresses = append(ep.Subsets[0].Addresses, newAddresses...)
		_ = port
	}
	_, err = b.cs.CoreV1().Endpoints(b.namespace).Update(ctx, ep, metav1.UpdateOptions{})
	return err
}

func (b *Backend) DeregisterTargets(ctx context.Context, targetGroupID string, targets []domain.Target) error {
	ep, err := b.cs.CoreV1().Endpoints(b.namespace).Get(ctx, targetGroupID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("target group %q: %w", targetGroupID, domain.ErrNotFound)
		}
		return err
	}
	remove := make(map[string]bool, len(targets))
	for _, t := range targets {
		remove[t.ID] = true
	}
	for i := range ep.Subsets {
		var kept []corev1.EndpointAddress
		for _, addr := range ep.Subsets[i].Addresses {
			if !remove[addr.IP] {
				kept = append(kept, addr)
			}
		}
		ep.Subsets[i].Addresses = kept
	}
	_, err = b.cs.CoreV1().Endpoints(b.namespace).Update(ctx, ep, metav1.UpdateOptions{})
	return err
}

// ─── Listener (Service port) ──────────────────────────────────────────

func (b *Backend) CreateListener(ctx context.Context, opt domain.CreateListenerOptions) (domain.Listener, error) {
	svc, err := b.cs.CoreV1().Services(b.namespace).Get(ctx, opt.LoadBalancerID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Listener{}, fmt.Errorf("load balancer %q: %w", opt.LoadBalancerID, domain.ErrNotFound)
		}
		return domain.Listener{}, err
	}
	proto := corev1.ProtocolTCP
	if opt.Protocol == domain.ProtocolUDP {
		proto = corev1.ProtocolUDP
	}
	svc.Spec.Ports = append(svc.Spec.Ports, corev1.ServicePort{
		Port:       int32(opt.Port),
		Protocol:   proto,
		TargetPort: intstr.FromInt32(int32(opt.Port)),
	})
	updated, err := b.cs.CoreV1().Services(b.namespace).Update(ctx, svc, metav1.UpdateOptions{})
	if err != nil {
		return domain.Listener{}, err
	}
	lsnID := fmt.Sprintf("%s-port-%d", updated.Name, opt.Port)
	return domain.Listener{
		ID:             lsnID,
		LoadBalancerID: opt.LoadBalancerID,
		Protocol:       opt.Protocol,
		Port:           opt.Port,
		TargetGroupID:  opt.TargetGroupID,
	}, nil
}

func (b *Backend) GetListener(ctx context.Context, id string) (domain.Listener, error) {
	// id = "{svcName}-port-{port}"
	return domain.Listener{}, fmt.Errorf("listener %q: %w", id, domain.ErrNotFound)
}

func (b *Backend) ListListeners(ctx context.Context, opt domain.ListListenersOptions) (domain.ListListenersResult, error) {
	if opt.LoadBalancerID == "" {
		return domain.ListListenersResult{}, nil
	}
	svc, err := b.cs.CoreV1().Services(b.namespace).Get(ctx, opt.LoadBalancerID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.ListListenersResult{}, nil
		}
		return domain.ListListenersResult{}, err
	}
	var listeners []domain.Listener
	for _, p := range svc.Spec.Ports {
		proto := domain.ProtocolTCP
		if p.Protocol == corev1.ProtocolUDP {
			proto = domain.ProtocolUDP
		}
		listeners = append(listeners, domain.Listener{
			ID:             fmt.Sprintf("%s-port-%d", svc.Name, p.Port),
			LoadBalancerID: svc.Name,
			Protocol:       proto,
			Port:           int(p.Port),
		})
	}
	return domain.ListListenersResult{Listeners: listeners}, nil
}

func (b *Backend) DeleteListener(ctx context.Context, id string) error {
	return nil
}

// ─── Converters ───────────────────────────────────────────────────────

func svcToDomain(svc *corev1.Service) domain.LoadBalancer {
	dnsName := svc.Name + ".default.svc.cluster.local"
	for _, ing := range svc.Status.LoadBalancer.Ingress {
		if ing.IP != "" {
			dnsName = ing.IP
			break
		}
		if ing.Hostname != "" {
			dnsName = ing.Hostname
			break
		}
	}
	return domain.LoadBalancer{
		ID:      svc.Name,
		Name:    svc.Name,
		Type:    domain.LoadBalancerTypeNetwork,
		DNSName: dnsName,
		State:   domain.LoadBalancerStateActive,
	}
}

func epToDomain(ep *corev1.Endpoints, proto domain.Protocol, port int) domain.TargetGroup {
	tg := domain.TargetGroup{
		ID:       ep.Name,
		Name:     ep.Name,
		Protocol: proto,
		Port:     port,
	}
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			t := domain.Target{ID: addr.IP}
			if len(subset.Ports) > 0 {
				t.Port = int(subset.Ports[0].Port)
			}
			tg.Targets = append(tg.Targets, t)
		}
	}
	return tg
}

func endpointAddressSet(ep *corev1.Endpoints) map[string]bool {
	m := map[string]bool{}
	for _, subset := range ep.Subsets {
		for _, addr := range subset.Addresses {
			m[addr.IP] = true
		}
	}
	return m
}

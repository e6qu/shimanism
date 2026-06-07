// Package k8slb is the Kubernetes peer for shimanism's load balancer
// service (Phase 16.D + Phase 21.C).
//
// K8s primitives used:
//   - Service (type:LoadBalancer) → domain.LoadBalancer (network type)
//   - Ingress → domain.LoadBalancer (application type)
//   - Endpoints → domain.TargetGroup + target registration
//
// Stateless: every Describe re-reads the K8s API.
package k8slb

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

const (
	labelManaged      = "shimanism.io/managed"
	annLBType         = "shimanism.io/lb-type"
	annListeners      = "shimanism.io/listeners"
	annRules          = "shimanism.io/rules"
	lbTypeApplication = "application"
)

// Backend implements domain.LoadBalancers via K8s Service + Endpoints + Ingress.
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

// ─── LoadBalancer (Service type:LoadBalancer or Ingress) ─────────────

func (b *Backend) CreateLoadBalancer(ctx context.Context, name string, opt domain.CreateLoadBalancerOptions) (domain.LoadBalancer, error) {
	if opt.Type == domain.LoadBalancerTypeApplication {
		return b.createIngressLB(ctx, name)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.namespace,
			Labels:    map[string]string{labelManaged: "true"},
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

func (b *Backend) createIngressLB(ctx context.Context, name string) (domain.LoadBalancer, error) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   b.namespace,
			Labels:      map[string]string{labelManaged: "true"},
			Annotations: map[string]string{annLBType: lbTypeApplication},
		},
		Spec: networkingv1.IngressSpec{},
	}
	created, err := b.cs.NetworkingV1().Ingresses(b.namespace).Create(ctx, ing, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", name, domain.ErrAlreadyExists)
		}
		return domain.LoadBalancer{}, err
	}
	return ingressToDomain(created), nil
}

func (b *Backend) GetLoadBalancer(ctx context.Context, id string) (domain.LoadBalancer, error) {
	svc, err := b.cs.CoreV1().Services(b.namespace).Get(ctx, id, metav1.GetOptions{})
	if err == nil {
		return svcToDomain(svc), nil
	}
	if !apierrors.IsNotFound(err) {
		return domain.LoadBalancer{}, err
	}
	ing, err2 := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, id, metav1.GetOptions{})
	if err2 != nil {
		if apierrors.IsNotFound(err2) {
			return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
		}
		return domain.LoadBalancer{}, err2
	}
	return ingressToDomain(ing), nil
}

func (b *Backend) ListLoadBalancers(ctx context.Context, opt domain.ListLoadBalancersOptions) (domain.ListLoadBalancersResult, error) {
	svcList, err := b.cs.CoreV1().Services(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelManaged + "=true",
	})
	if err != nil {
		return domain.ListLoadBalancersResult{}, err
	}
	ingList, err := b.cs.NetworkingV1().Ingresses(b.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelManaged + "=true",
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
	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}
		lb := svcToDomain(svc)
		if len(nameSet) > 0 && !nameSet[lb.Name] {
			continue
		}
		if len(idSet) > 0 && !idSet[lb.ID] {
			continue
		}
		lbs = append(lbs, lb)
	}
	for i := range ingList.Items {
		lb := ingressToDomain(&ingList.Items[i])
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
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	err2 := b.cs.NetworkingV1().Ingresses(b.namespace).Delete(ctx, id, metav1.DeleteOptions{})
	if err2 != nil {
		if apierrors.IsNotFound(err2) {
			return fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
		}
		return err2
	}
	return nil
}

// ─── TargetGroup (Endpoints) ──────────────────────────────────────────

func (b *Backend) CreateTargetGroup(ctx context.Context, name string, opt domain.CreateTargetGroupOptions) (domain.TargetGroup, error) {
	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.namespace,
			Labels:    map[string]string{labelManaged: "true"},
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
		LabelSelector: labelManaged + "=true",
	})
	if err != nil {
		return domain.ListTargetGroupsResult{}, err
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var tgs []domain.TargetGroup
	for i := range list.Items {
		tg := epToDomain(&list.Items[i], domain.ProtocolTCP, 0)
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
	proto := corev1.ProtocolTCP
	if len(ep.Subsets) == 0 {
		ep.Subsets = []corev1.EndpointSubset{{
			Addresses: newAddresses,
			Ports:     []corev1.EndpointPort{{Port: 80, Protocol: proto, Name: "tcp"}},
		}}
	} else {
		ep.Subsets[0].Addresses = append(ep.Subsets[0].Addresses, newAddresses...)
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

// ─── Listener ────────────────────────────────────────────────────────

func (b *Backend) CreateListener(ctx context.Context, opt domain.CreateListenerOptions) (domain.Listener, error) {
	// Try Ingress first (application type).
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, opt.LoadBalancerID, metav1.GetOptions{})
	if err == nil {
		return b.createIngressListener(ctx, ing, opt)
	}
	if !apierrors.IsNotFound(err) {
		return domain.Listener{}, err
	}
	// Fall back to Service (network type).
	svc, err := b.cs.CoreV1().Services(b.namespace).Get(ctx, opt.LoadBalancerID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Listener{}, fmt.Errorf("load balancer %q: %w", opt.LoadBalancerID, domain.ErrNotFound)
		}
		return domain.Listener{}, err
	}
	return b.createServiceListener(ctx, svc, opt)
}

func (b *Backend) createServiceListener(ctx context.Context, svc *corev1.Service, opt domain.CreateListenerOptions) (domain.Listener, error) {
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
	return domain.Listener{
		ID:             fmt.Sprintf("%s-port-%d", updated.Name, opt.Port),
		LoadBalancerID: opt.LoadBalancerID,
		Protocol:       opt.Protocol,
		Port:           opt.Port,
		TargetGroupID:  opt.TargetGroupID,
	}, nil
}

func (b *Backend) createIngressListener(ctx context.Context, ing *networkingv1.Ingress, opt domain.CreateListenerOptions) (domain.Listener, error) {
	if ing.Annotations == nil {
		ing.Annotations = make(map[string]string)
	}
	lsnID := fmt.Sprintf("%s-port-%d", ing.Name, opt.Port)
	listeners := ingressListeners(ing)
	for _, l := range listeners {
		if l.ID == lsnID {
			return l, nil
		}
	}
	lsn := domain.Listener{
		ID:             lsnID,
		LoadBalancerID: opt.LoadBalancerID,
		Protocol:       opt.Protocol,
		Port:           opt.Port,
		TargetGroupID:  opt.TargetGroupID,
		CertificateIDs: opt.CertificateIDs,
	}
	listeners = append(listeners, lsn)
	data, _ := json.Marshal(listeners)
	ing.Annotations[annListeners] = string(data)
	if opt.Protocol == domain.ProtocolHTTPS {
		for _, certID := range opt.CertificateIDs {
			ing.Spec.TLS = append(ing.Spec.TLS, networkingv1.IngressTLS{
				SecretName: certID,
			})
		}
	}
	_, err := b.cs.NetworkingV1().Ingresses(b.namespace).Update(ctx, ing, metav1.UpdateOptions{})
	if err != nil {
		return domain.Listener{}, err
	}
	return lsn, nil
}

func (b *Backend) GetListener(_ context.Context, id string) (domain.Listener, error) {
	return domain.Listener{}, fmt.Errorf("listener %q: %w", id, domain.ErrNotFound)
}

func (b *Backend) ListListeners(ctx context.Context, opt domain.ListListenersOptions) (domain.ListListenersResult, error) {
	if opt.LoadBalancerID == "" {
		return domain.ListListenersResult{}, nil
	}
	// Try Ingress first.
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, opt.LoadBalancerID, metav1.GetOptions{})
	if err == nil {
		return domain.ListListenersResult{Listeners: ingressListeners(ing)}, nil
	}
	if !apierrors.IsNotFound(err) {
		return domain.ListListenersResult{}, err
	}
	// Fall back to Service.
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

func (b *Backend) DeleteListener(_ context.Context, _ string) error {
	return nil
}

// ─── Rule lifecycle (K8s Ingress path rules) ─────────────────────────
//
// Rule IDs have the form "{ingressName}:{base64url(path)}" so they are
// deterministic and encode no extra state.

func (b *Backend) CreateRule(ctx context.Context, opt domain.CreateRuleOptions) (domain.Rule, error) {
	lbID := lbIDFromListenerID(opt.ListenerID)
	if lbID == "" {
		return domain.Rule{}, fmt.Errorf("cannot derive LB from listener %q: %w", opt.ListenerID, domain.ErrInvalidInput)
	}
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, lbID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Rule{}, fmt.Errorf("load balancer %q: %w", lbID, domain.ErrNotFound)
		}
		return domain.Rule{}, err
	}
	path := ruleConditionPath(opt.Conditions)
	tgName := opt.Action.TargetGroupID
	ruleID := makeRuleID(lbID, path)

	// Check duplicate.
	for _, r := range ingressRules(ing) {
		if r.ID == ruleID {
			return r, nil
		}
	}

	// Append path to Ingress spec.
	pt := networkingv1.PathTypePrefix
	if ing.Spec.Rules == nil {
		ing.Spec.Rules = []networkingv1.IngressRule{{
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{},
			},
		}}
	}
	if ing.Spec.Rules[0].HTTP == nil {
		ing.Spec.Rules[0].HTTP = &networkingv1.HTTPIngressRuleValue{}
	}
	ing.Spec.Rules[0].HTTP.Paths = append(ing.Spec.Rules[0].HTTP.Paths, networkingv1.HTTPIngressPath{
		Path:     path,
		PathType: &pt,
		Backend: networkingv1.IngressBackend{
			Service: &networkingv1.IngressServiceBackend{
				Name: tgName,
				Port: networkingv1.ServiceBackendPort{Number: 80},
			},
		},
	})

	// Persist rule metadata in annotation.
	rule := domain.Rule{
		ID:         ruleID,
		ListenerID: opt.ListenerID,
		Priority:   opt.Priority,
		Conditions: opt.Conditions,
		Action:     opt.Action,
	}
	rules := append(ingressRules(ing), rule)
	data, _ := json.Marshal(rules)
	if ing.Annotations == nil {
		ing.Annotations = make(map[string]string)
	}
	ing.Annotations[annRules] = string(data)
	_, err = b.cs.NetworkingV1().Ingresses(b.namespace).Update(ctx, ing, metav1.UpdateOptions{})
	if err != nil {
		return domain.Rule{}, err
	}
	return rule, nil
}

func (b *Backend) GetRule(ctx context.Context, id string) (domain.Rule, error) {
	lbID := lbIDFromRuleID(id)
	if lbID == "" {
		return domain.Rule{}, fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
	}
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, lbID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Rule{}, fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
		}
		return domain.Rule{}, err
	}
	for _, r := range ingressRules(ing) {
		if r.ID == id {
			return r, nil
		}
	}
	return domain.Rule{}, fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
}

func (b *Backend) ListRules(ctx context.Context, opt domain.ListRulesOptions) (domain.ListRulesResult, error) {
	if opt.ListenerID == "" {
		return domain.ListRulesResult{}, nil
	}
	lbID := lbIDFromListenerID(opt.ListenerID)
	if lbID == "" {
		return domain.ListRulesResult{}, nil
	}
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, lbID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.ListRulesResult{}, nil
		}
		return domain.ListRulesResult{}, err
	}
	var result []domain.Rule
	for _, r := range ingressRules(ing) {
		if r.ListenerID == opt.ListenerID {
			result = append(result, r)
		}
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	if len(idSet) > 0 {
		var filtered []domain.Rule
		for _, r := range result {
			if idSet[r.ID] {
				filtered = append(filtered, r)
			}
		}
		result = filtered
	}
	return domain.ListRulesResult{Rules: result}, nil
}

func (b *Backend) DeleteRule(ctx context.Context, id string) error {
	lbID := lbIDFromRuleID(id)
	if lbID == "" {
		return fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
	}
	ing, err := b.cs.NetworkingV1().Ingresses(b.namespace).Get(ctx, lbID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
		}
		return err
	}
	rules := ingressRules(ing)
	var kept []domain.Rule
	var deletedPath string
	for _, r := range rules {
		if r.ID == id {
			deletedPath = ruleConditionPath(r.Conditions)
		} else {
			kept = append(kept, r)
		}
	}
	if deletedPath == "" {
		return fmt.Errorf("rule %q: %w", id, domain.ErrNotFound)
	}

	// Remove from Ingress spec paths.
	if len(ing.Spec.Rules) > 0 && ing.Spec.Rules[0].HTTP != nil {
		var paths []networkingv1.HTTPIngressPath
		for _, p := range ing.Spec.Rules[0].HTTP.Paths {
			if p.Path != deletedPath {
				paths = append(paths, p)
			}
		}
		ing.Spec.Rules[0].HTTP.Paths = paths
	}

	data, _ := json.Marshal(kept)
	if ing.Annotations == nil {
		ing.Annotations = make(map[string]string)
	}
	ing.Annotations[annRules] = string(data)
	_, err = b.cs.NetworkingV1().Ingresses(b.namespace).Update(ctx, ing, metav1.UpdateOptions{})
	return err
}

func (b *Backend) UpdateTargetGroup(_ context.Context, _ string, _ domain.UpdateTargetGroupOptions) (domain.TargetGroup, error) {
	return domain.TargetGroup{}, fmt.Errorf("K8s target group update: %w", domain.ErrNotSupported)
}

func (b *Backend) UpdateListener(_ context.Context, _ string, _ domain.UpdateListenerOptions) (domain.Listener, error) {
	return domain.Listener{}, fmt.Errorf("K8s listener update: %w", domain.ErrNotSupported)
}

func (b *Backend) UpdateRule(_ context.Context, _ string, _ domain.UpdateRuleOptions) (domain.Rule, error) {
	return domain.Rule{}, fmt.Errorf("K8s rule update: %w", domain.ErrNotSupported)
}

func (b *Backend) SetRulePriorities(_ context.Context, _ []domain.RulePriorityPair) ([]domain.Rule, error) {
	return nil, fmt.Errorf("K8s set rule priorities: %w", domain.ErrNotSupported)
}

// ─── Blob storage (not supported on K8s backend) ─────────────────────

func (b *Backend) PutBlob(_ context.Context, _, _ string, _ []byte) error {
	return fmt.Errorf("blob storage not available on K8s backend: %w", domain.ErrNotSupported)
}

func (b *Backend) GetBlob(_ context.Context, _, _ string) ([]byte, error) {
	return nil, fmt.Errorf("blob storage not available on K8s backend: %w", domain.ErrNotSupported)
}

func (b *Backend) ListBlobs(_ context.Context, _ string) ([]domain.BlobEntry, error) {
	return nil, fmt.Errorf("blob storage not available on K8s backend: %w", domain.ErrNotSupported)
}

func (b *Backend) DeleteBlob(_ context.Context, _, _ string) error {
	return fmt.Errorf("blob storage not available on K8s backend: %w", domain.ErrNotSupported)
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

func ingressToDomain(ing *networkingv1.Ingress) domain.LoadBalancer {
	dnsName := ing.Name + ".default.svc.cluster.local"
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			dnsName = lb.IP
			break
		}
		if lb.Hostname != "" {
			dnsName = lb.Hostname
			break
		}
	}
	return domain.LoadBalancer{
		ID:      ing.Name,
		Name:    ing.Name,
		Type:    domain.LoadBalancerTypeApplication,
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

// ─── Ingress annotation helpers ───────────────────────────────────────

func ingressListeners(ing *networkingv1.Ingress) []domain.Listener {
	raw := ing.Annotations[annListeners]
	if raw == "" {
		return nil
	}
	var result []domain.Listener
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

func ingressRules(ing *networkingv1.Ingress) []domain.Rule {
	raw := ing.Annotations[annRules]
	if raw == "" {
		return nil
	}
	var result []domain.Rule
	_ = json.Unmarshal([]byte(raw), &result)
	return result
}

// ─── Rule ID helpers ──────────────────────────────────────────────────

// makeRuleID creates a stable, path-derived rule ID.
func makeRuleID(ingressName, path string) string {
	return ingressName + ":" + base64.URLEncoding.EncodeToString([]byte(path))
}

// lbIDFromRuleID extracts the Ingress name from a rule ID.
func lbIDFromRuleID(id string) string {
	i := strings.Index(id, ":")
	if i < 0 {
		return ""
	}
	return id[:i]
}

// lbIDFromListenerID extracts the LB name from a listener ID
// (format: "{lbName}-port-{port}").
func lbIDFromListenerID(id string) string {
	const suffix = "-port-"
	i := strings.LastIndex(id, suffix)
	if i < 0 {
		return ""
	}
	return id[:i]
}

// ruleConditionPath extracts the first path-pattern value from conditions.
func ruleConditionPath(conditions []domain.RuleCondition) string {
	for _, c := range conditions {
		if c.Type == domain.RuleConditionPathPattern && len(c.Values) > 0 {
			return c.Values[0]
		}
	}
	return "/"
}

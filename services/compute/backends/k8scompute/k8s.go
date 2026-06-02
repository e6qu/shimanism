// Package k8scompute is the Kubernetes peer for shimanism's compute
// service networking primitives, Phase 16.B.
//
// K8s primitives used:
//   - Namespace  → domain.Network (VPC / VNet)
//   - NetworkPolicy → domain.SecurityGroup (SG / firewall / NSG)
//   - Subnet / PublicIP → ErrNotSupported (no K8s analog in intersection)
//
// Namespace and NetworkPolicy are core K8s built-ins; no CRD import
// needed. Uses the typed k8s.io/client-go APIs directly.
//
// Stateless: every Describe re-reads the K8s API; no shim-side cache.
//
// Normalization (per AGENTS.md):
// N21 (SG allow-only): NetworkPolicy supports egress + ingress policy
// types. The shim emits both PolicyTypes in every NetworkPolicy, and
// populates either or both Ingress/Egress rule slices from the domain
// rules. Stateful return-traffic is not a K8s concept; the mapping is
// correct for the allow-only intersection.
// N26 (subnet AZ): Subnets have no K8s analog → NotImplemented.
// N22 (public IP two-step): PublicIPs have no K8s analog → NotImplemented.
package k8scompute

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"

	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Backend implements domain.Networking via K8s Namespace + NetworkPolicy.
type Backend struct {
	cs kubernetes.Interface
	// parentNS is the K8s namespace where the Backend operates. Networking
	// domain networks map to sub-namespaces scoped under parentNS.
	// For most deployments parentNS = "default".
	parentNS string
}

// New returns a Backend bound to the given Kubernetes client.
// parentNS is the namespace where NetworkPolicies are created;
// domain Networks map to K8s Namespaces at the cluster level.
func New(cs kubernetes.Interface, parentNS string) *Backend {
	if parentNS == "" {
		parentNS = "default"
	}
	return &Backend{cs: cs, parentNS: parentNS}
}

var _ domain.Networking = (*Backend)(nil)

// ─── Networks (Namespaces) ────────────────────────────────────────────

func (b *Backend) CreateNetwork(ctx context.Context, name string, opt domain.CreateNetworkOptions) (domain.Network, error) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labelsFromTags(opt.Tags),
			Annotations: map[string]string{
				"shimanism.io/cidr": opt.CIDR,
			},
		},
	}
	created, err := b.cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.Network{}, fmt.Errorf("network %q: %w", name, domain.ErrAlreadyExists)
		}
		return domain.Network{}, err
	}
	return nsToDomain(created), nil
}

func (b *Backend) GetNetwork(ctx context.Context, id string) (domain.Network, error) {
	ns, err := b.cs.CoreV1().Namespaces().Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.Network{}, fmt.Errorf("network %q: %w", id, domain.ErrNotFound)
		}
		return domain.Network{}, err
	}
	return nsToDomain(ns), nil
}

func (b *Backend) ListNetworks(ctx context.Context, opt domain.ListNetworksOptions) (domain.ListNetworksResult, error) {
	list, err := b.cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: "shimanism.io/managed=true",
	})
	if err != nil {
		return domain.ListNetworksResult{}, err
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var nets []domain.Network
	for _, ns := range list.Items {
		n := nsToDomain(&ns)
		if len(idSet) > 0 && !idSet[n.ID] {
			continue
		}
		nets = append(nets, n)
	}
	return domain.ListNetworksResult{Networks: nets}, nil
}

func (b *Backend) DeleteNetwork(ctx context.Context, id string) error {
	err := b.cs.CoreV1().Namespaces().Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("network %q: %w", id, domain.ErrNotFound)
		}
		return err
	}
	return nil
}

// ─── Subnets (NotImplemented) ─────────────────────────────────────────

func (b *Backend) CreateSubnet(_ context.Context, name string, _ domain.CreateSubnetOptions) (domain.Subnet, error) {
	return domain.Subnet{}, fmt.Errorf("subnets have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) GetSubnet(_ context.Context, id string) (domain.Subnet, error) {
	return domain.Subnet{}, fmt.Errorf("subnets have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) ListSubnets(_ context.Context, _ domain.ListSubnetsOptions) (domain.ListSubnetsResult, error) {
	return domain.ListSubnetsResult{}, fmt.Errorf("subnets have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) DeleteSubnet(_ context.Context, id string) error {
	return fmt.Errorf("subnets have no K8s analog: %w", domain.ErrNotSupported)
}

// ─── Security Groups (NetworkPolicies) ───────────────────────────────

func (b *Backend) CreateSecurityGroup(ctx context.Context, name string, opt domain.CreateSecurityGroupOptions) (domain.SecurityGroup, error) {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: b.parentNS,
			Labels:    labelsFromTags(opt.Tags),
			Annotations: map[string]string{
				"shimanism.io/network-id":  opt.NetworkID,
				"shimanism.io/description": opt.Description,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}
	created, err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Create(ctx, np, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return domain.SecurityGroup{}, fmt.Errorf("security group %q: %w", name, domain.ErrAlreadyExists)
		}
		return domain.SecurityGroup{}, err
	}
	return npToDomain(created), nil
}

func (b *Backend) GetSecurityGroup(ctx context.Context, id string) (domain.SecurityGroup, error) {
	np, err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return domain.SecurityGroup{}, fmt.Errorf("security group %q: %w", id, domain.ErrNotFound)
		}
		return domain.SecurityGroup{}, err
	}
	return npToDomain(np), nil
}

func (b *Backend) ListSecurityGroups(ctx context.Context, opt domain.ListSecurityGroupsOptions) (domain.ListSecurityGroupsResult, error) {
	list, err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).List(ctx, metav1.ListOptions{
		LabelSelector: "shimanism.io/managed=true",
	})
	if err != nil {
		return domain.ListSecurityGroupsResult{}, err
	}
	idSet := make(map[string]bool, len(opt.IDs))
	for _, id := range opt.IDs {
		idSet[id] = true
	}
	var sgs []domain.SecurityGroup
	for _, np := range list.Items {
		sg := npToDomain(&np)
		if len(idSet) > 0 && !idSet[sg.ID] {
			continue
		}
		if opt.NetworkID != "" && sg.NetworkID != opt.NetworkID {
			continue
		}
		sgs = append(sgs, sg)
	}
	return domain.ListSecurityGroupsResult{SecurityGroups: sgs}, nil
}

func (b *Backend) DeleteSecurityGroup(ctx context.Context, id string) error {
	err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Delete(ctx, id, metav1.DeleteOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("security group %q: %w", id, domain.ErrNotFound)
		}
		return err
	}
	return nil
}

func (b *Backend) AddRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	np, err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Get(ctx, sgID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("security group %q: %w", sgID, domain.ErrNotFound)
		}
		return err
	}
	addDomainRule(np, rule)
	_, err = b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Update(ctx, np, metav1.UpdateOptions{})
	return err
}

func (b *Backend) RemoveRule(ctx context.Context, sgID string, rule domain.SecurityGroupRule) error {
	np, err := b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Get(ctx, sgID, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("security group %q: %w", sgID, domain.ErrNotFound)
		}
		return err
	}
	removeDomainRule(np, rule)
	_, err = b.cs.NetworkingV1().NetworkPolicies(b.parentNS).Update(ctx, np, metav1.UpdateOptions{})
	return err
}

// ─── Public IPs (NotImplemented) ─────────────────────────────────────

func (b *Backend) AllocatePublicIP(_ context.Context, _ domain.AllocatePublicIPOptions) (domain.PublicIP, error) {
	return domain.PublicIP{}, fmt.Errorf("public IPs have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) AssociatePublicIP(_ context.Context, _, _ string) error {
	return fmt.Errorf("public IPs have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) DisassociatePublicIP(_ context.Context, _ string) error {
	return fmt.Errorf("public IPs have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) ReleasePublicIP(_ context.Context, _ string) error {
	return fmt.Errorf("public IPs have no K8s analog: %w", domain.ErrNotSupported)
}

func (b *Backend) ListPublicIPs(_ context.Context, _ domain.ListPublicIPsOptions) (domain.ListPublicIPsResult, error) {
	return domain.ListPublicIPsResult{}, fmt.Errorf("public IPs have no K8s analog: %w", domain.ErrNotSupported)
}

// ─── Converters ───────────────────────────────────────────────────────

func nsToDomain(ns *corev1.Namespace) domain.Network {
	cidr := ""
	if ns.Annotations != nil {
		cidr = ns.Annotations["shimanism.io/cidr"]
	}
	return domain.Network{
		ID:   ns.Name,
		Name: ns.Name,
		CIDR: cidr,
		Tags: tagsFromLabels(ns.Labels),
	}
}

func npToDomain(np *networkingv1.NetworkPolicy) domain.SecurityGroup {
	sg := domain.SecurityGroup{
		ID:   np.Name,
		Name: np.Name,
		Tags: tagsFromLabels(np.Labels),
	}
	if np.Annotations != nil {
		sg.NetworkID = np.Annotations["shimanism.io/network-id"]
		sg.Description = np.Annotations["shimanism.io/description"]
	}
	for _, r := range np.Spec.Ingress {
		sg.Rules = append(sg.Rules, npIngressToDomain(r))
	}
	for _, r := range np.Spec.Egress {
		sg.Rules = append(sg.Rules, npEgressToDomain(r))
	}
	return sg
}

func npIngressToDomain(r networkingv1.NetworkPolicyIngressRule) domain.SecurityGroupRule {
	rule := domain.SecurityGroupRule{Direction: domain.Inbound}
	for _, peer := range r.From {
		if peer.IPBlock != nil {
			rule.CIDRs = append(rule.CIDRs, peer.IPBlock.CIDR)
		}
	}
	for _, port := range r.Ports {
		if port.Protocol != nil && *port.Protocol == corev1.ProtocolTCP {
			rule.Protocol = "tcp"
		} else if port.Protocol != nil && *port.Protocol == corev1.ProtocolUDP {
			rule.Protocol = "udp"
		} else {
			rule.Protocol = "-1"
		}
		if port.Port != nil {
			rule.PortFrom = port.Port.IntValue()
			rule.PortTo = rule.PortFrom
		}
		if port.EndPort != nil {
			rule.PortTo = int(*port.EndPort)
		}
	}
	return rule
}

func npEgressToDomain(r networkingv1.NetworkPolicyEgressRule) domain.SecurityGroupRule {
	rule := domain.SecurityGroupRule{Direction: domain.Outbound}
	for _, peer := range r.To {
		if peer.IPBlock != nil {
			rule.CIDRs = append(rule.CIDRs, peer.IPBlock.CIDR)
		}
	}
	for _, port := range r.Ports {
		if port.Protocol != nil && *port.Protocol == corev1.ProtocolTCP {
			rule.Protocol = "tcp"
		} else if port.Protocol != nil && *port.Protocol == corev1.ProtocolUDP {
			rule.Protocol = "udp"
		} else {
			rule.Protocol = "-1"
		}
		if port.Port != nil {
			rule.PortFrom = port.Port.IntValue()
			rule.PortTo = rule.PortFrom
		}
		if port.EndPort != nil {
			rule.PortTo = int(*port.EndPort)
		}
	}
	return rule
}

func addDomainRule(np *networkingv1.NetworkPolicy, r domain.SecurityGroupRule) {
	port := domainRuleToNPPort(r)
	if r.Direction == domain.Inbound {
		rule := networkingv1.NetworkPolicyIngressRule{Ports: port}
		for _, cidr := range r.CIDRs {
			cidr := cidr
			rule.From = append(rule.From, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: cidr},
			})
		}
		np.Spec.Ingress = append(np.Spec.Ingress, rule)
	} else {
		rule := networkingv1.NetworkPolicyEgressRule{Ports: port}
		for _, cidr := range r.CIDRs {
			cidr := cidr
			rule.To = append(rule.To, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: cidr},
			})
		}
		np.Spec.Egress = append(np.Spec.Egress, rule)
	}
}

func removeDomainRule(np *networkingv1.NetworkPolicy, r domain.SecurityGroupRule) {
	// Remove the first ingress or egress rule that matches the domain rule.
	if r.Direction == domain.Inbound {
		var kept []networkingv1.NetworkPolicyIngressRule
		removed := false
		for _, existing := range np.Spec.Ingress {
			if !removed && ingressMatchesDomain(existing, r) {
				removed = true
				continue
			}
			kept = append(kept, existing)
		}
		np.Spec.Ingress = kept
	} else {
		var kept []networkingv1.NetworkPolicyEgressRule
		removed := false
		for _, existing := range np.Spec.Egress {
			if !removed && egressMatchesDomain(existing, r) {
				removed = true
				continue
			}
			kept = append(kept, existing)
		}
		np.Spec.Egress = kept
	}
}

func ingressMatchesDomain(r networkingv1.NetworkPolicyIngressRule, d domain.SecurityGroupRule) bool {
	if len(r.Ports) == 0 && d.PortFrom != 0 {
		return false
	}
	for _, port := range r.Ports {
		if port.Port != nil && port.Port.IntValue() != d.PortFrom {
			return false
		}
	}
	return true
}

func egressMatchesDomain(r networkingv1.NetworkPolicyEgressRule, d domain.SecurityGroupRule) bool {
	if len(r.Ports) == 0 && d.PortFrom != 0 {
		return false
	}
	for _, port := range r.Ports {
		if port.Port != nil && port.Port.IntValue() != d.PortFrom {
			return false
		}
	}
	return true
}

func domainRuleToNPPort(r domain.SecurityGroupRule) []networkingv1.NetworkPolicyPort {
	if r.Protocol == "" || r.Protocol == "-1" {
		return nil
	}
	proto := corev1.ProtocolTCP
	if r.Protocol == "udp" {
		proto = corev1.ProtocolUDP
	}
	port := networkingv1.NetworkPolicyPort{Protocol: &proto}
	if r.PortFrom != 0 {
		portVal := intstr.FromInt32(int32(r.PortFrom))
		port.Port = &portVal
		if r.PortTo != 0 && r.PortTo != r.PortFrom {
			endPort := int32(r.PortTo)
			port.EndPort = &endPort
		}
	}
	return []networkingv1.NetworkPolicyPort{port}
}

// ─── Tag / label helpers ──────────────────────────────────────────────

// shimanism.io/managed label marks all shim-owned resources so
// ListNetworks / ListSecurityGroups can filter by it efficiently.
func labelsFromTags(tags map[string]string) map[string]string {
	m := map[string]string{"shimanism.io/managed": "true"}
	for k, v := range tags {
		m[k] = v
	}
	return m
}

func tagsFromLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	m := make(map[string]string, len(labels))
	for k, v := range labels {
		if k == "shimanism.io/managed" {
			continue
		}
		m[k] = v
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

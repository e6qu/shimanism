// Package aws is the AWS ELBv2 passthrough backend for shimanism's
// load balancer service (Phase 16.D). It uses
// aws-sdk-go-v2/service/elasticloadbalancingv2 to drive real AWS ELBv2
// (or a sockerless-pointed client for tests).
//
// Domain IDs map to AWS ARNs. The backend stores no translation tables;
// list operations re-read AWS on every request (stateless).
//
// EC2 client is needed for RegisterTargets where the target may be an
// instance ID that needs to be resolved to its description.
package aws

import (
	"context"
	"fmt"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	elbv2types "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2/types"

	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

// Backend implements domain.LoadBalancers via real AWS ELBv2.
type Backend struct {
	elb *elbv2.Client
	ec2 *awsec2.Client
}

// New wraps already-configured ELBv2 and EC2 clients.
// ec2 may be nil if RegisterTargets is not used.
func New(elb *elbv2.Client, ec2 *awsec2.Client) *Backend {
	return &Backend{elb: elb, ec2: ec2}
}

var _ domain.LoadBalancers = (*Backend)(nil)

// ─── LoadBalancer lifecycle ──────────────────────────────────────────

func (b *Backend) CreateLoadBalancer(ctx context.Context, name string, opt domain.CreateLoadBalancerOptions) (domain.LoadBalancer, error) {
	lbType := elbv2types.LoadBalancerTypeEnumNetwork
	if opt.Type == domain.LoadBalancerTypeApplication {
		return domain.LoadBalancer{}, fmt.Errorf("application LB: %w", domain.ErrNotSupported)
	}
	out, err := b.elb.CreateLoadBalancer(ctx, &elbv2.CreateLoadBalancerInput{
		Name: awsapi.String(name),
		Type: lbType,
	})
	if err != nil {
		return domain.LoadBalancer{}, mapErr(err)
	}
	if len(out.LoadBalancers) == 0 {
		return domain.LoadBalancer{}, fmt.Errorf("empty response from CreateLoadBalancer")
	}
	return awsLBToDomain(&out.LoadBalancers[0]), nil
}

func (b *Backend) GetLoadBalancer(ctx context.Context, id string) (domain.LoadBalancer, error) {
	out, err := b.elb.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{
		LoadBalancerArns: []string{id},
	})
	if err != nil {
		return domain.LoadBalancer{}, mapErr(err)
	}
	if len(out.LoadBalancers) == 0 {
		return domain.LoadBalancer{}, fmt.Errorf("load balancer %q: %w", id, domain.ErrNotFound)
	}
	return awsLBToDomain(&out.LoadBalancers[0]), nil
}

func (b *Backend) ListLoadBalancers(ctx context.Context, opt domain.ListLoadBalancersOptions) (domain.ListLoadBalancersResult, error) {
	in := &elbv2.DescribeLoadBalancersInput{}
	if len(opt.IDs) > 0 {
		in.LoadBalancerArns = opt.IDs
	}
	if len(opt.Names) > 0 {
		in.Names = opt.Names
	}
	out, err := b.elb.DescribeLoadBalancers(ctx, in)
	if err != nil {
		return domain.ListLoadBalancersResult{}, mapErr(err)
	}
	var lbs []domain.LoadBalancer
	for _, lb := range out.LoadBalancers {
		lb := lb
		lbs = append(lbs, awsLBToDomain(&lb))
	}
	return domain.ListLoadBalancersResult{LoadBalancers: lbs}, nil
}

func (b *Backend) DeleteLoadBalancer(ctx context.Context, id string) error {
	_, err := b.elb.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{
		LoadBalancerArn: awsapi.String(id),
	})
	return mapErr(err)
}

// ─── TargetGroup lifecycle ───────────────────────────────────────────

func (b *Backend) CreateTargetGroup(ctx context.Context, name string, opt domain.CreateTargetGroupOptions) (domain.TargetGroup, error) {
	proto := elbv2types.ProtocolEnumTcp
	if opt.Protocol == domain.ProtocolUDP {
		proto = elbv2types.ProtocolEnumUdp
	}
	in := &elbv2.CreateTargetGroupInput{
		Name:     awsapi.String(name),
		Protocol: proto,
	}
	if opt.Port != 0 {
		in.Port = awsapi.Int32(int32(opt.Port))
	}
	if opt.VpcID != "" {
		in.VpcId = awsapi.String(opt.VpcID)
	}
	out, err := b.elb.CreateTargetGroup(ctx, in)
	if err != nil {
		return domain.TargetGroup{}, mapErr(err)
	}
	if len(out.TargetGroups) == 0 {
		return domain.TargetGroup{}, fmt.Errorf("empty response from CreateTargetGroup")
	}
	return awsTGToDomain(&out.TargetGroups[0]), nil
}

func (b *Backend) GetTargetGroup(ctx context.Context, id string) (domain.TargetGroup, error) {
	out, err := b.elb.DescribeTargetGroups(ctx, &elbv2.DescribeTargetGroupsInput{
		TargetGroupArns: []string{id},
	})
	if err != nil {
		return domain.TargetGroup{}, mapErr(err)
	}
	if len(out.TargetGroups) == 0 {
		return domain.TargetGroup{}, fmt.Errorf("target group %q: %w", id, domain.ErrNotFound)
	}
	tg := awsTGToDomain(&out.TargetGroups[0])
	// Populate targets from DescribeTargetHealth.
	health, err := b.elb.DescribeTargetHealth(ctx, &elbv2.DescribeTargetHealthInput{
		TargetGroupArn: awsapi.String(id),
	})
	if err == nil {
		for _, desc := range health.TargetHealthDescriptions {
			if desc.Target != nil {
				t := domain.Target{ID: awsapi.ToString(desc.Target.Id)}
				if desc.Target.Port != nil {
					t.Port = int(*desc.Target.Port)
				}
				tg.Targets = append(tg.Targets, t)
			}
		}
	}
	return tg, nil
}

func (b *Backend) ListTargetGroups(ctx context.Context, opt domain.ListTargetGroupsOptions) (domain.ListTargetGroupsResult, error) {
	in := &elbv2.DescribeTargetGroupsInput{}
	if len(opt.IDs) > 0 {
		in.TargetGroupArns = opt.IDs
	}
	out, err := b.elb.DescribeTargetGroups(ctx, in)
	if err != nil {
		return domain.ListTargetGroupsResult{}, mapErr(err)
	}
	var tgs []domain.TargetGroup
	for _, tg := range out.TargetGroups {
		tg := tg
		tgs = append(tgs, awsTGToDomain(&tg))
	}
	return domain.ListTargetGroupsResult{TargetGroups: tgs}, nil
}

func (b *Backend) DeleteTargetGroup(ctx context.Context, id string) error {
	_, err := b.elb.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
		TargetGroupArn: awsapi.String(id),
	})
	return mapErr(err)
}

// ─── Target registration ─────────────────────────────────────────────

func (b *Backend) RegisterTargets(ctx context.Context, targetGroupID string, targets []domain.Target) error {
	var awsTargets []elbv2types.TargetDescription
	for _, t := range targets {
		td := elbv2types.TargetDescription{Id: awsapi.String(t.ID)}
		if t.Port != 0 {
			td.Port = awsapi.Int32(int32(t.Port))
		}
		awsTargets = append(awsTargets, td)
	}
	_, err := b.elb.RegisterTargets(ctx, &elbv2.RegisterTargetsInput{
		TargetGroupArn: awsapi.String(targetGroupID),
		Targets:        awsTargets,
	})
	return mapErr(err)
}

func (b *Backend) DeregisterTargets(ctx context.Context, targetGroupID string, targets []domain.Target) error {
	var awsTargets []elbv2types.TargetDescription
	for _, t := range targets {
		awsTargets = append(awsTargets, elbv2types.TargetDescription{Id: awsapi.String(t.ID)})
	}
	_, err := b.elb.DeregisterTargets(ctx, &elbv2.DeregisterTargetsInput{
		TargetGroupArn: awsapi.String(targetGroupID),
		Targets:        awsTargets,
	})
	return mapErr(err)
}

// ─── Listener lifecycle ──────────────────────────────────────────────

func (b *Backend) CreateListener(ctx context.Context, opt domain.CreateListenerOptions) (domain.Listener, error) {
	proto := elbv2types.ProtocolEnumTcp
	if opt.Protocol == domain.ProtocolUDP {
		proto = elbv2types.ProtocolEnumUdp
	}
	in := &elbv2.CreateListenerInput{
		LoadBalancerArn: awsapi.String(opt.LoadBalancerID),
		Protocol:        proto,
	}
	if opt.Port != 0 {
		in.Port = awsapi.Int32(int32(opt.Port))
	}
	if opt.TargetGroupID != "" {
		in.DefaultActions = []elbv2types.Action{{
			Type:           elbv2types.ActionTypeEnumForward,
			TargetGroupArn: awsapi.String(opt.TargetGroupID),
		}}
	}
	out, err := b.elb.CreateListener(ctx, in)
	if err != nil {
		return domain.Listener{}, mapErr(err)
	}
	if len(out.Listeners) == 0 {
		return domain.Listener{}, fmt.Errorf("empty response from CreateListener")
	}
	return awsListenerToDomain(&out.Listeners[0]), nil
}

func (b *Backend) GetListener(ctx context.Context, id string) (domain.Listener, error) {
	out, err := b.elb.DescribeListeners(ctx, &elbv2.DescribeListenersInput{
		ListenerArns: []string{id},
	})
	if err != nil {
		return domain.Listener{}, mapErr(err)
	}
	if len(out.Listeners) == 0 {
		return domain.Listener{}, fmt.Errorf("listener %q: %w", id, domain.ErrNotFound)
	}
	return awsListenerToDomain(&out.Listeners[0]), nil
}

func (b *Backend) ListListeners(ctx context.Context, opt domain.ListListenersOptions) (domain.ListListenersResult, error) {
	in := &elbv2.DescribeListenersInput{}
	if opt.LoadBalancerID != "" {
		in.LoadBalancerArn = awsapi.String(opt.LoadBalancerID)
	}
	if len(opt.IDs) > 0 {
		in.ListenerArns = opt.IDs
	}
	out, err := b.elb.DescribeListeners(ctx, in)
	if err != nil {
		return domain.ListListenersResult{}, mapErr(err)
	}
	var listeners []domain.Listener
	for _, l := range out.Listeners {
		l := l
		listeners = append(listeners, awsListenerToDomain(&l))
	}
	return domain.ListListenersResult{Listeners: listeners}, nil
}

func (b *Backend) DeleteListener(ctx context.Context, id string) error {
	_, err := b.elb.DeleteListener(ctx, &elbv2.DeleteListenerInput{
		ListenerArn: awsapi.String(id),
	})
	return mapErr(err)
}

// ─── Converters ───────────────────────────────────────────────────────

func awsLBToDomain(lb *elbv2types.LoadBalancer) domain.LoadBalancer {
	d := domain.LoadBalancer{
		ID:      awsapi.ToString(lb.LoadBalancerArn),
		Name:    awsapi.ToString(lb.LoadBalancerName),
		DNSName: awsapi.ToString(lb.DNSName),
		State:   domain.LoadBalancerStateActive,
		VpcID:   awsapi.ToString(lb.VpcId),
	}
	if lb.Type == elbv2types.LoadBalancerTypeEnumNetwork {
		d.Type = domain.LoadBalancerTypeNetwork
	} else {
		d.Type = domain.LoadBalancerTypeApplication
	}
	return d
}

func awsTGToDomain(tg *elbv2types.TargetGroup) domain.TargetGroup {
	d := domain.TargetGroup{
		ID:    awsapi.ToString(tg.TargetGroupArn),
		Name:  awsapi.ToString(tg.TargetGroupName),
		VpcID: awsapi.ToString(tg.VpcId),
	}
	if tg.Port != nil {
		d.Port = int(*tg.Port)
	}
	if tg.Protocol == elbv2types.ProtocolEnumUdp {
		d.Protocol = domain.ProtocolUDP
	} else {
		d.Protocol = domain.ProtocolTCP
	}
	return d
}

func awsListenerToDomain(l *elbv2types.Listener) domain.Listener {
	d := domain.Listener{
		ID:             awsapi.ToString(l.ListenerArn),
		LoadBalancerID: awsapi.ToString(l.LoadBalancerArn),
	}
	if l.Port != nil {
		d.Port = int(*l.Port)
	}
	if l.Protocol == elbv2types.ProtocolEnumUdp {
		d.Protocol = domain.ProtocolUDP
	} else {
		d.Protocol = domain.ProtocolTCP
	}
	for _, action := range l.DefaultActions {
		if action.TargetGroupArn != nil {
			d.TargetGroupID = *action.TargetGroupArn
			break
		}
	}
	return d
}

// ─── Error mapping ────────────────────────────────────────────────────

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if contains(msg, "LoadBalancerNotFound", "TargetGroupNotFound", "ListenerNotFound") {
		return fmt.Errorf("%w: %v", domain.ErrNotFound, err)
	}
	if contains(msg, "DuplicateLoadBalancerName", "DuplicateTargetGroupName") {
		return fmt.Errorf("%w: %v", domain.ErrAlreadyExists, err)
	}
	return err
}

func contains(s string, substrs ...string) bool {
	for _, sub := range substrs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}

// Package aws_elbv2 is the AWS ELBv2 frontend for shimanism's
// load balancer service, Phase 16.D. It bridges the spec-driven
// awsQuery generated stubs (services/loadbalancer/gen) onto the
// neutral domain.LoadBalancers interface.
//
// Protocol: awsQuery (form-encoded POST, Action dispatch, awsQuery
// XML envelopes). Auth: SigV4 with service="elasticloadbalancing".
//
// ELBv2 identifies resources by ARN. This adapter fabricates ARN-like
// strings from domain IDs and parses them back; no translation table
// is stored.
package aws_elbv2

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/e6qu/shimanism/internal/awsquery"
	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/loadbalancer/gen"
)

const (
	arnRegion  = "us-east-1"
	arnAccount = "000000000000"
)

// Adapter binds gen.ElasticLoadBalancingv2Backend to domain.LoadBalancers.
type Adapter struct {
	lb domain.LoadBalancers
}

// New returns the http.Handler dispatching through the generated awsQuery
// router into the adapter.
func New(lb domain.LoadBalancers) http.Handler {
	router := gen.RegisterElasticLoadBalancingv2Routes(&Adapter{lb: lb})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "elasticloadbalancing", Region: arnRegion})
	return sigv4verifier.Middleware(verifier, awsquery.EmitVerifierError)(router)
}

// ─── ARN helpers ──────────────────────────────────────────────────────

func lbARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/net/%s/shim", arnRegion, arnAccount, id)
}

func tgARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/shim", arnRegion, arnAccount, id)
}

func listenerARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/net/shim/shim/%s", arnRegion, arnAccount, id)
}

// idFromLBARN extracts the domain ID embedded in a fabricated LB ARN.
// ARN format: .../net/{id}/shim
func idFromLBARN(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			rest := arn[i+1:]
			if rest == "shim" {
				for j := i - 1; j >= 0; j-- {
					if arn[j] == '/' {
						return arn[j+1 : i]
					}
				}
			}
			break
		}
	}
	return arn
}

// idFromTGARN extracts the domain ID from a fabricated TG ARN.
// ARN format: .../targetgroup/{id}/shim
func idFromTGARN(arn string) string { return idFromLBARN(arn) }

// idFromListenerARN extracts the listener domain ID.
// ARN format: .../listener/net/shim/shim/{id}
func idFromListenerARN(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}
	return arn
}

// ─── Error mapping ────────────────────────────────────────────────────

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case domain.IsNotFound(err):
		return &awsquery.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "Sender",
			Code:       "LoadBalancerNotFound",
			Message:    err.Error(),
		}
	case domain.IsAlreadyExists(err):
		return &awsquery.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "Sender",
			Code:       "DuplicateLoadBalancerName",
			Message:    err.Error(),
		}
	case domain.IsNotSupported(err):
		return &awsquery.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "Sender",
			Code:       "UnsupportedProtocol",
			Message:    err.Error(),
		}
	default:
		return &awsquery.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "Receiver",
			Code:       "InternalFailure",
			Message:    err.Error(),
		}
	}
}

// ─── Converters ───────────────────────────────────────────────────────

func domainLBToGen(lb domain.LoadBalancer) gen.LoadBalancer {
	arn := lbARN(lb.ID)
	active := gen.LoadBalancerStateEnum("active")
	lbType := gen.LoadBalancerTypeEnum(lb.Type)
	return gen.LoadBalancer{
		LoadBalancerArn:  &arn,
		LoadBalancerName: &lb.Name,
		DNSName:          &lb.DNSName,
		Type:             &lbType,
		State:            &gen.LoadBalancerState{Code: &active},
		VpcId:            &lb.VpcID,
	}
}

func domainTGToGen(tg domain.TargetGroup) gen.TargetGroup {
	arn := tgARN(tg.ID)
	proto := gen.ProtocolEnum(tg.Protocol)
	port := int32(tg.Port)
	return gen.TargetGroup{
		TargetGroupArn:  &arn,
		TargetGroupName: &tg.Name,
		Protocol:        &proto,
		Port:            &port,
		VpcId:           &tg.VpcID,
	}
}

func domainListenerToGen(l domain.Listener) gen.Listener {
	arn := listenerARN(l.ID)
	lbArn := lbARN(l.LoadBalancerID)
	proto := gen.ProtocolEnum(l.Protocol)
	port := int32(l.Port)
	genL := gen.Listener{
		ListenerArn:     &arn,
		LoadBalancerArn: &lbArn,
		Protocol:        &proto,
		Port:            &port,
	}
	if l.TargetGroupID != "" {
		tgArn := tgARN(l.TargetGroupID)
		genL.DefaultActions.Member = []gen.Action{{
			Type:           gen.ActionTypeEnum("forward"),
			TargetGroupArn: &tgArn,
		}}
	}
	return genL
}

// ─── LoadBalancer operations ──────────────────────────────────────────

func (a *Adapter) CreateLoadBalancer(ctx context.Context, in *gen.CreateLoadBalancerInput) (*gen.CreateLoadBalancerOutput, error) {
	lbType := domain.LoadBalancerTypeNetwork
	if in.Type != nil && string(*in.Type) == "application" {
		return nil, &awsquery.BackendError{
			HTTPStatus: http.StatusBadRequest,
			Type:       "Sender",
			Code:       "InvalidConfigurationRequest",
			Message:    "application load balancers are out of intersection (N27)",
		}
	}
	lb, err := a.lb.CreateLoadBalancer(ctx, in.Name, domain.CreateLoadBalancerOptions{
		Type: lbType,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	genLB := domainLBToGen(lb)
	return &gen.CreateLoadBalancerOutput{
		LoadBalancers: gen.LoadBalancers{Member: []gen.LoadBalancer{genLB}},
	}, nil
}

func (a *Adapter) DeleteLoadBalancer(ctx context.Context, in *gen.DeleteLoadBalancerInput) (*gen.DeleteLoadBalancerOutput, error) {
	id := idFromLBARN(in.LoadBalancerArn)
	if err := a.lb.DeleteLoadBalancer(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteLoadBalancerOutput{}, nil
}

func (a *Adapter) DescribeLoadBalancers(ctx context.Context, in *gen.DescribeLoadBalancersInput) (*gen.DescribeLoadBalancersOutput, error) {
	opt := domain.ListLoadBalancersOptions{}
	for _, arn := range in.LoadBalancerArns.Member {
		opt.IDs = append(opt.IDs, idFromLBARN(arn))
	}
	opt.Names = append(opt.Names, in.Names.Member...)
	res, err := a.lb.ListLoadBalancers(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.DescribeLoadBalancersOutput{}
	for _, lb := range res.LoadBalancers {
		out.LoadBalancers.Member = append(out.LoadBalancers.Member, domainLBToGen(lb))
	}
	if res.NextToken != "" {
		out.NextMarker = &res.NextToken
	}
	return out, nil
}

// ─── TargetGroup operations ───────────────────────────────────────────

func (a *Adapter) CreateTargetGroup(ctx context.Context, in *gen.CreateTargetGroupInput) (*gen.CreateTargetGroupOutput, error) {
	proto := domain.ProtocolTCP
	if in.Protocol != nil && string(*in.Protocol) == "UDP" {
		proto = domain.ProtocolUDP
	}
	port := 0
	if in.Port != nil {
		port = int(*in.Port)
	}
	vpcID := ""
	if in.VpcId != nil {
		vpcID = *in.VpcId
	}
	tg, err := a.lb.CreateTargetGroup(ctx, in.Name, domain.CreateTargetGroupOptions{
		Protocol: proto,
		Port:     port,
		VpcID:    vpcID,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateTargetGroupOutput{
		TargetGroups: gen.TargetGroups{Member: []gen.TargetGroup{domainTGToGen(tg)}},
	}, nil
}

func (a *Adapter) DeleteTargetGroup(ctx context.Context, in *gen.DeleteTargetGroupInput) (*gen.DeleteTargetGroupOutput, error) {
	id := idFromTGARN(in.TargetGroupArn)
	if err := a.lb.DeleteTargetGroup(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteTargetGroupOutput{}, nil
}

func (a *Adapter) DescribeTargetGroups(ctx context.Context, in *gen.DescribeTargetGroupsInput) (*gen.DescribeTargetGroupsOutput, error) {
	opt := domain.ListTargetGroupsOptions{}
	for _, arn := range in.TargetGroupArns.Member {
		opt.IDs = append(opt.IDs, idFromTGARN(arn))
	}
	res, err := a.lb.ListTargetGroups(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.DescribeTargetGroupsOutput{}
	for _, tg := range res.TargetGroups {
		out.TargetGroups.Member = append(out.TargetGroups.Member, domainTGToGen(tg))
	}
	if res.NextToken != "" {
		out.NextMarker = &res.NextToken
	}
	return out, nil
}

// ─── Target registration ──────────────────────────────────────────────

func (a *Adapter) RegisterTargets(ctx context.Context, in *gen.RegisterTargetsInput) (*gen.RegisterTargetsOutput, error) {
	tgID := idFromTGARN(in.TargetGroupArn)
	// Decode Targets from form context — complex struct list not
	// decoded by generated code.
	form := awsquery.FormFromContext(ctx)
	var targets []domain.Target
	if form != nil {
		for i := 1; ; i++ {
			id := form.Get(fmt.Sprintf("Targets.member.%d.Id", i))
			if id == "" {
				break
			}
			t := domain.Target{ID: id}
			if p := form.Get(fmt.Sprintf("Targets.member.%d.Port", i)); p != "" {
				n, _ := strconv.Atoi(p)
				t.Port = n
			}
			targets = append(targets, t)
		}
	}
	if err := a.lb.RegisterTargets(ctx, tgID, targets); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.RegisterTargetsOutput{}, nil
}

func (a *Adapter) DeregisterTargets(ctx context.Context, in *gen.DeregisterTargetsInput) (*gen.DeregisterTargetsOutput, error) {
	tgID := idFromTGARN(in.TargetGroupArn)
	form := awsquery.FormFromContext(ctx)
	var targets []domain.Target
	if form != nil {
		for i := 1; ; i++ {
			id := form.Get(fmt.Sprintf("Targets.member.%d.Id", i))
			if id == "" {
				break
			}
			targets = append(targets, domain.Target{ID: id})
		}
	}
	if err := a.lb.DeregisterTargets(ctx, tgID, targets); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeregisterTargetsOutput{}, nil
}

func (a *Adapter) DescribeTargetHealth(ctx context.Context, in *gen.DescribeTargetHealthInput) (*gen.DescribeTargetHealthOutput, error) {
	tgID := idFromTGARN(in.TargetGroupArn)
	tg, err := a.lb.GetTargetGroup(ctx, tgID)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.DescribeTargetHealthOutput{}
	healthy := gen.TargetHealthStateEnum("healthy")
	for _, t := range tg.Targets {
		t := t
		port := int32(t.Port)
		out.TargetHealthDescriptions.Member = append(out.TargetHealthDescriptions.Member, gen.TargetHealthDescription{
			Target:       &gen.TargetDescription{Id: t.ID, Port: &port},
			TargetHealth: &gen.TargetHealth{State: &healthy},
		})
	}
	return out, nil
}

// ─── Listener operations ──────────────────────────────────────────────

func (a *Adapter) CreateListener(ctx context.Context, in *gen.CreateListenerInput) (*gen.CreateListenerOutput, error) {
	lbID := idFromLBARN(in.LoadBalancerArn)
	proto := domain.ProtocolTCP
	if in.Protocol != nil && string(*in.Protocol) == "UDP" {
		proto = domain.ProtocolUDP
	}
	port := 0
	if in.Port != nil {
		port = int(*in.Port)
	}
	// Extract target group ARN from DefaultActions.
	tgID := ""
	form := awsquery.FormFromContext(ctx)
	if form != nil {
		tgArn := form.Get("DefaultActions.member.1.TargetGroupArn")
		tgID = idFromTGARN(tgArn)
	}
	l, err := a.lb.CreateListener(ctx, domain.CreateListenerOptions{
		LoadBalancerID: lbID,
		Protocol:       proto,
		Port:           port,
		TargetGroupID:  tgID,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	genL := domainListenerToGen(l)
	return &gen.CreateListenerOutput{
		Listeners: gen.Listeners{Member: []gen.Listener{genL}},
	}, nil
}

func (a *Adapter) DeleteListener(ctx context.Context, in *gen.DeleteListenerInput) (*gen.DeleteListenerOutput, error) {
	id := idFromListenerARN(in.ListenerArn)
	if err := a.lb.DeleteListener(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteListenerOutput{}, nil
}

func (a *Adapter) DescribeListeners(ctx context.Context, in *gen.DescribeListenersInput) (*gen.DescribeListenersOutput, error) {
	opt := domain.ListListenersOptions{}
	if in.LoadBalancerArn != nil {
		opt.LoadBalancerID = idFromLBARN(*in.LoadBalancerArn)
	}
	for _, arn := range in.ListenerArns.Member {
		opt.IDs = append(opt.IDs, idFromListenerARN(arn))
	}
	res, err := a.lb.ListListeners(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.DescribeListenersOutput{}
	for _, l := range res.Listeners {
		out.Listeners.Member = append(out.Listeners.Member, domainListenerToGen(l))
	}
	if res.NextToken != "" {
		out.NextMarker = &res.NextToken
	}
	return out, nil
}

// ─── Tag operations (no-op; stateless shim) ───────────────────────────

func (a *Adapter) AddTags(_ context.Context, _ *gen.AddTagsInput) (*gen.AddTagsOutput, error) {
	return &gen.AddTagsOutput{}, nil
}

func (a *Adapter) RemoveTags(_ context.Context, _ *gen.RemoveTagsInput) (*gen.RemoveTagsOutput, error) {
	return &gen.RemoveTagsOutput{}, nil
}

func (a *Adapter) DescribeTags(_ context.Context, _ *gen.DescribeTagsInput) (*gen.DescribeTagsOutput, error) {
	return &gen.DescribeTagsOutput{}, nil
}

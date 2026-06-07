// Package aws_elbv2 is the AWS ELBv2 frontend for shimanism's
// load balancer service. It bridges the spec-driven awsQuery generated
// stubs (services/loadbalancer/gen) onto the neutral domain.LoadBalancers
// interface.
//
// Phase 16.D: layer-4 TCP NLB lifecycle.
// Phase 21: layer-7 ALB — HTTPS listeners, HTTP target groups, routing rules (N35).
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
	// Wrap the router to intercept attribute-management operations that
	// are called by Terraform after every create but aren't in the codegen
	// spec (they are stateless no-ops: the shim has no per-LB attribute store).
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := r.FormValue("Action")
		switch action {
		case "ModifyLoadBalancerAttributes", "ModifyTargetGroupAttributes",
			"DescribeLoadBalancerAttributes", "DescribeTargetGroupAttributes":
			awsquery.WriteResult(w, action, struct{}{})
		default:
			router.ServeHTTP(w, r)
		}
	})
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "elasticloadbalancing", Region: arnRegion})
	return sigv4verifier.Middleware(verifier, awsquery.EmitVerifierError)(wrapped)
}

// ─── ARN helpers ──────────────────────────────────────────────────────

// nlbARN produces an ARN for a network load balancer.
func nlbARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/net/%s/shim", arnRegion, arnAccount, id)
}

// albARN produces an ARN for an application load balancer.
func albARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/app/%s/shim", arnRegion, arnAccount, id)
}

func tgARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s/shim", arnRegion, arnAccount, id)
}

func listenerARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener/app/shim/shim/%s", arnRegion, arnAccount, id)
}

func ruleARN(id string) string {
	return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:listener-rule/app/shim/shim/shim/%s", arnRegion, arnAccount, id)
}

// idFromLBARN extracts the domain ID embedded in a fabricated LB ARN.
// Works for both NLB (.../net/{id}/shim) and ALB (.../app/{id}/shim).
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
func idFromTGARN(arn string) string { return idFromLBARN(arn) }

// idFromListenerARN extracts the listener domain ID (last path segment).
func idFromListenerARN(arn string) string {
	for i := len(arn) - 1; i >= 0; i-- {
		if arn[i] == '/' {
			return arn[i+1:]
		}
	}
	return arn
}

// idFromRuleARN extracts the rule domain ID (last path segment).
func idFromRuleARN(arn string) string { return idFromListenerARN(arn) }

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
	var arn string
	if lb.Type == domain.LoadBalancerTypeApplication {
		arn = albARN(lb.ID)
	} else {
		arn = nlbARN(lb.ID)
	}
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
	g := gen.TargetGroup{
		TargetGroupArn:  &arn,
		TargetGroupName: &tg.Name,
		Protocol:        &proto,
		Port:            &port,
		VpcId:           &tg.VpcID,
	}
	if tg.HealthCheck.Path != "" || tg.HealthCheck.Protocol != "" {
		if tg.HealthCheck.Path != "" {
			g.HealthCheckPath = &tg.HealthCheck.Path
		}
		if tg.HealthCheck.Protocol != "" {
			hcProto := gen.ProtocolEnum(tg.HealthCheck.Protocol)
			g.HealthCheckProtocol = &hcProto
		}
		if tg.HealthCheck.Port != "" {
			g.HealthCheckPort = &tg.HealthCheck.Port
		}
		if tg.HealthCheck.HTTPCodes != "" {
			g.Matcher = &gen.Matcher{HttpCode: &tg.HealthCheck.HTTPCodes}
		}
	}
	return g
}

func domainListenerToGen(l domain.Listener) gen.Listener {
	arn := listenerARN(l.ID)
	lbArn := albARN(l.LoadBalancerID)
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
	for _, certID := range l.CertificateIDs {
		certID := certID
		genL.Certificates.Member = append(genL.Certificates.Member, gen.Certificate{
			CertificateArn: &certID,
		})
	}
	return genL
}

func domainRuleToGen(r domain.Rule) gen.Rule {
	arn := ruleARN(r.ID)
	prio := strconv.Itoa(r.Priority)
	gr := gen.Rule{
		RuleArn:  &arn,
		Priority: &prio,
	}
	for _, c := range r.Conditions {
		c := c
		field := string(c.Type)
		rc := gen.RuleCondition{Field: &field}
		rc.Values.Member = append(rc.Values.Member, c.Values...)
		switch c.Type {
		case domain.RuleConditionHostHeader:
			rc.HostHeaderConfig = &gen.HostHeaderConditionConfig{}
			rc.HostHeaderConfig.Values.Member = append(rc.HostHeaderConfig.Values.Member, c.Values...)
		case domain.RuleConditionPathPattern:
			rc.PathPatternConfig = &gen.PathPatternConditionConfig{}
			rc.PathPatternConfig.Values.Member = append(rc.PathPatternConfig.Values.Member, c.Values...)
		}
		gr.Conditions.Member = append(gr.Conditions.Member, rc)
	}
	if r.Action.TargetGroupID != "" {
		tgArn := tgARN(r.Action.TargetGroupID)
		gr.Actions.Member = []gen.Action{{
			Type:           gen.ActionTypeEnum("forward"),
			TargetGroupArn: &tgArn,
		}}
	}
	return gr
}

// ─── Helpers ─────────────────────────────────────────────────────────

func healthCheckFromInput(path *string, proto *gen.ProtocolEnum, port *string, matcher *gen.Matcher) domain.HealthCheck {
	var hc domain.HealthCheck
	if path != nil {
		hc.Path = *path
	}
	if proto != nil {
		hc.Protocol = domain.Protocol(*proto)
	}
	if port != nil {
		hc.Port = *port
	}
	if matcher != nil && matcher.HttpCode != nil {
		hc.HTTPCodes = *matcher.HttpCode
	}
	return hc
}

func protocolFromEnum(p *gen.ProtocolEnum) domain.Protocol {
	if p == nil {
		return domain.ProtocolTCP
	}
	switch string(*p) {
	case "HTTP":
		return domain.ProtocolHTTP
	case "HTTPS":
		return domain.ProtocolHTTPS
	case "UDP":
		return domain.ProtocolUDP
	default:
		return domain.ProtocolTCP
	}
}

// ─── LoadBalancer operations ──────────────────────────────────────────

func (a *Adapter) CreateLoadBalancer(ctx context.Context, in *gen.CreateLoadBalancerInput) (*gen.CreateLoadBalancerOutput, error) {
	lbType := domain.LoadBalancerTypeNetwork
	if in.Type != nil && *in.Type == gen.LoadBalancerTypeEnumAPPLICATION {
		lbType = domain.LoadBalancerTypeApplication
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
	if in.Protocol != nil {
		switch string(*in.Protocol) {
		case "UDP":
			proto = domain.ProtocolUDP
		case "HTTP":
			proto = domain.ProtocolHTTP
		case "HTTPS":
			proto = domain.ProtocolHTTPS
		}
	}
	port := 0
	if in.Port != nil {
		port = int(*in.Port)
	}
	vpcID := ""
	if in.VpcId != nil {
		vpcID = *in.VpcId
	}
	hc := healthCheckFromInput(in.HealthCheckPath, in.HealthCheckProtocol, in.HealthCheckPort, in.Matcher)
	tg, err := a.lb.CreateTargetGroup(ctx, in.Name, domain.CreateTargetGroupOptions{
		Protocol:    proto,
		Port:        port,
		VpcID:       vpcID,
		HealthCheck: hc,
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

func (a *Adapter) ModifyTargetGroup(ctx context.Context, in *gen.ModifyTargetGroupInput) (*gen.ModifyTargetGroupOutput, error) {
	id := idFromTGARN(in.TargetGroupArn)
	hc := healthCheckFromInput(in.HealthCheckPath, in.HealthCheckProtocol, in.HealthCheckPort, in.Matcher)
	tg, err := a.lb.UpdateTargetGroup(ctx, id, domain.UpdateTargetGroupOptions{HealthCheck: hc})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.ModifyTargetGroupOutput{
		TargetGroups: gen.TargetGroups{Member: []gen.TargetGroup{domainTGToGen(tg)}},
	}, nil
}

// ─── Target registration ──────────────────────────────────────────────

func (a *Adapter) RegisterTargets(ctx context.Context, in *gen.RegisterTargetsInput) (*gen.RegisterTargetsOutput, error) {
	tgID := idFromTGARN(in.TargetGroupArn)
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
	proto := protocolFromEnum(in.Protocol)
	port := 0
	if in.Port != nil {
		port = int(*in.Port)
	}
	form := awsquery.FormFromContext(ctx)
	tgID := ""
	if form != nil {
		tgArn := form.Get("DefaultActions.member.1.TargetGroupArn")
		tgID = idFromTGARN(tgArn)
	}
	var certIDs []string
	for _, c := range in.Certificates.Member {
		if c.CertificateArn != nil {
			certIDs = append(certIDs, *c.CertificateArn)
		}
	}
	// Certificates may also come via form for complex lists not decoded by codegen.
	if form != nil && len(certIDs) == 0 {
		for i := 1; ; i++ {
			arn := form.Get(fmt.Sprintf("Certificates.member.%d.CertificateArn", i))
			if arn == "" {
				break
			}
			certIDs = append(certIDs, arn)
		}
	}
	l, err := a.lb.CreateListener(ctx, domain.CreateListenerOptions{
		LoadBalancerID: lbID,
		Protocol:       proto,
		Port:           port,
		TargetGroupID:  tgID,
		CertificateIDs: certIDs,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateListenerOutput{
		Listeners: gen.Listeners{Member: []gen.Listener{domainListenerToGen(l)}},
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

func (a *Adapter) ModifyListener(ctx context.Context, in *gen.ModifyListenerInput) (*gen.ModifyListenerOutput, error) {
	id := idFromListenerARN(in.ListenerArn)
	opt := domain.UpdateListenerOptions{}
	if in.Protocol != nil {
		opt.Protocol = protocolFromEnum(in.Protocol)
	}
	if in.Port != nil {
		opt.Port = int(*in.Port)
	}
	form := awsquery.FormFromContext(ctx)
	if form != nil {
		tgArn := form.Get("DefaultActions.member.1.TargetGroupArn")
		if tgArn != "" {
			opt.TargetGroupID = idFromTGARN(tgArn)
		}
		for i := 1; ; i++ {
			arn := form.Get(fmt.Sprintf("Certificates.member.%d.CertificateArn", i))
			if arn == "" {
				break
			}
			opt.CertificateIDs = append(opt.CertificateIDs, arn)
		}
	}
	for _, c := range in.Certificates.Member {
		if c.CertificateArn != nil {
			opt.CertificateIDs = append(opt.CertificateIDs, *c.CertificateArn)
		}
	}
	l, err := a.lb.UpdateListener(ctx, id, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.ModifyListenerOutput{
		Listeners: gen.Listeners{Member: []gen.Listener{domainListenerToGen(l)}},
	}, nil
}

// ─── Rule operations (L7 only) ────────────────────────────────────────

func (a *Adapter) CreateRule(ctx context.Context, in *gen.CreateRuleInput) (*gen.CreateRuleOutput, error) {
	lsnID := idFromListenerARN(in.ListenerArn)
	form := awsquery.FormFromContext(ctx)
	var conditions []domain.RuleCondition
	if form != nil {
		for i := 1; ; i++ {
			prefix := fmt.Sprintf("Conditions.member.%d", i)
			field := form.Get(prefix + ".Field")
			if field == "" {
				break
			}
			rc := domain.RuleCondition{Type: domain.RuleConditionType(field)}
			for j := 1; ; j++ {
				v := form.Get(fmt.Sprintf("%s.Values.member.%d", prefix, j))
				if v == "" {
					break
				}
				rc.Values = append(rc.Values, v)
			}
			conditions = append(conditions, rc)
		}
	}
	var action domain.RuleAction
	if form != nil {
		tgArn := form.Get("Actions.member.1.TargetGroupArn")
		action.TargetGroupID = idFromTGARN(tgArn)
	}
	rule, err := a.lb.CreateRule(ctx, domain.CreateRuleOptions{
		ListenerID: lsnID,
		Priority:   int(in.Priority),
		Conditions: conditions,
		Action:     action,
	})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.CreateRuleOutput{
		Rules: gen.Rules{Member: []gen.Rule{domainRuleToGen(rule)}},
	}, nil
}

func (a *Adapter) DeleteRule(ctx context.Context, in *gen.DeleteRuleInput) (*gen.DeleteRuleOutput, error) {
	id := idFromRuleARN(in.RuleArn)
	if err := a.lb.DeleteRule(ctx, id); err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.DeleteRuleOutput{}, nil
}

func (a *Adapter) DescribeRules(ctx context.Context, in *gen.DescribeRulesInput) (*gen.DescribeRulesOutput, error) {
	opt := domain.ListRulesOptions{}
	if in.ListenerArn != nil {
		opt.ListenerID = idFromListenerARN(*in.ListenerArn)
	}
	for _, arn := range in.RuleArns.Member {
		opt.IDs = append(opt.IDs, idFromRuleARN(arn))
	}
	res, err := a.lb.ListRules(ctx, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.DescribeRulesOutput{}
	for _, r := range res.Rules {
		out.Rules.Member = append(out.Rules.Member, domainRuleToGen(r))
	}
	if res.NextToken != "" {
		out.NextMarker = &res.NextToken
	}
	return out, nil
}

func (a *Adapter) ModifyRule(ctx context.Context, in *gen.ModifyRuleInput) (*gen.ModifyRuleOutput, error) {
	id := idFromRuleARN(in.RuleArn)
	form := awsquery.FormFromContext(ctx)
	opt := domain.UpdateRuleOptions{}
	if form != nil {
		for i := 1; ; i++ {
			prefix := fmt.Sprintf("Conditions.member.%d", i)
			field := form.Get(prefix + ".Field")
			if field == "" {
				break
			}
			rc := domain.RuleCondition{Type: domain.RuleConditionType(field)}
			for j := 1; ; j++ {
				v := form.Get(fmt.Sprintf("%s.Values.member.%d", prefix, j))
				if v == "" {
					break
				}
				rc.Values = append(rc.Values, v)
			}
			opt.Conditions = append(opt.Conditions, rc)
		}
		tgArn := form.Get("Actions.member.1.TargetGroupArn")
		if tgArn != "" {
			opt.Action.TargetGroupID = idFromTGARN(tgArn)
		}
	}
	rule, err := a.lb.UpdateRule(ctx, id, opt)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	return &gen.ModifyRuleOutput{
		Rules: gen.Rules{Member: []gen.Rule{domainRuleToGen(rule)}},
	}, nil
}

func (a *Adapter) SetRulePriorities(ctx context.Context, in *gen.SetRulePrioritiesInput) (*gen.SetRulePrioritiesOutput, error) {
	form := awsquery.FormFromContext(ctx)
	var pairs []domain.RulePriorityPair
	if form != nil {
		for i := 1; ; i++ {
			prefix := fmt.Sprintf("RulePriorities.member.%d", i)
			arn := form.Get(prefix + ".RuleArn")
			if arn == "" {
				break
			}
			prioStr := form.Get(prefix + ".Priority")
			prio, _ := strconv.Atoi(prioStr)
			pairs = append(pairs, domain.RulePriorityPair{
				ID:       idFromRuleARN(arn),
				Priority: prio,
			})
		}
	}
	rules, err := a.lb.SetRulePriorities(ctx, pairs)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.SetRulePrioritiesOutput{}
	for _, r := range rules {
		out.Rules.Member = append(out.Rules.Member, domainRuleToGen(r))
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

func (a *Adapter) DescribeTags(_ context.Context, in *gen.DescribeTagsInput) (*gen.DescribeTagsOutput, error) {
	out := &gen.DescribeTagsOutput{}
	for _, arn := range in.ResourceArns.Member {
		arn := arn
		out.TagDescriptions.Member = append(out.TagDescriptions.Member, gen.TagDescription{ResourceArn: &arn})
	}
	return out, nil
}

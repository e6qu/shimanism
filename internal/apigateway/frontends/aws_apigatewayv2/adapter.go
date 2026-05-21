// Package aws_apigatewayv2 is the AWS API Gateway HTTP API v2-shaped
// HTTP frontend. Phase 11.10 migrated it from a hand-written
// restJson1 wire layer to spec-driven generated stubs. This file is
// the adapter — it implements gen.ApiGatewayV2Backend by translating
// each generated per-op request into the neutral domain.APIGateway
// interface.
//
// Stateful caveat (carried from the hand-written wire): API Gateway
// v2 splits gateway creation across multiple resources (Api + Routes
// + Integrations), with the final CreateDeployment performing the
// atomic publish. The shim's domain has only DeployGateway (one-shot
// atomic). To bridge the AWS multi-step flow, this adapter
// accumulates per-Api pending routes + integration ID-to-target
// mappings in process memory. The state is cleared after each
// successful CreateDeployment. **This is per-process state by
// design** — the deployed routing table itself lives in the backend
// (statelessly); the accumulation only exists between AWS's
// CreateRoute/Integration calls and the subsequent CreateDeployment
// that publishes them.
package aws_apigatewayv2

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/restxml"
	gen "github.com/e6qu/shimanism/services/apigateway/gen"
)

// Adapter binds gen.ApiGatewayV2Backend to a domain.APIGateway backend.
type Adapter struct {
	s domain.APIGateway

	mu      sync.Mutex
	pending map[string][]domain.Route    // apiID → routes
	intIDs  map[string]map[string]string // apiID → integrationID → targetURL
}

// New returns the http.Handler dispatching through the generated
// restJson1 router into the adapter bound to the given backend.
func New(s domain.APIGateway) http.Handler {
	a := &Adapter{
		s:       s,
		pending: map[string][]domain.Route{},
		intIDs:  map[string]map[string]string{},
	}
	router := &restxml.Router{}
	gen.RegisterApiGatewayV2Routes(router, a)
	return router
}

// ---------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------

func strPtr(s string) *string { return &s }

func strDeref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func apiEndpointURL(name string) string {
	return "https://" + name + ".execute-api.us-east-1.amazonaws.com"
}

func generateID() string {
	t := time.Now().UnixNano()
	return strings.ReplaceAll(time.Unix(0, t).Format("150405.000000000"), ".", "")
}

func splitRouteKey(rk string) (method, path string, ok bool) {
	i := strings.IndexByte(rk, ' ')
	if i < 0 {
		return "", "", false
	}
	return rk[:i], rk[i+1:], true
}

func gatewayToAPI(g domain.Gateway) *gen.Api {
	api := &gen.Api{
		ApiId:                     strPtr(g.Name),
		Name:                      g.Name,
		ProtocolType:              gen.ProtocolType("HTTP"),
		ApiKeySelectionExpression: strPtr("$request.header.x-api-key"),
		RouteSelectionExpression:  "$request.method $request.path",
	}
	if !g.CreatedAt.IsZero() {
		t := g.CreatedAt.UTC()
		api.CreatedDate = &t
	}
	if g.Status == domain.StatusAvailable {
		if g.Endpoint.URL != "" {
			api.ApiEndpoint = strPtr(g.Endpoint.URL)
		} else {
			api.ApiEndpoint = strPtr(apiEndpointURL(g.Name))
		}
	}
	return api
}

func mapDomainErr(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return &awsjson.BackendError{
			HTTPStatus: http.StatusInternalServerError,
			Type:       "ServiceException",
			Message:    err.Error(),
		}
	}
	be := &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Message: de.Error()}
	switch de.Kind {
	case domain.KindNoSuchGateway:
		be.HTTPStatus = http.StatusNotFound
		be.Type = "NotFoundException"
	case domain.KindGatewayAlreadyExists:
		be.HTTPStatus = http.StatusConflict
		be.Type = "ConflictException"
	case domain.KindInvalidArgument:
		be.Type = "BadRequestException"
	default:
		be.HTTPStatus = http.StatusInternalServerError
		be.Type = "ServiceException"
	}
	return be
}

// ---------------------------------------------------------------------
// Api ops.
// ---------------------------------------------------------------------

func (a *Adapter) CreateApi(ctx context.Context, in *gen.CreateApiRequest) (*gen.CreateApiResponse, error) {
	if in.Name == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "BadRequestException", Message: "name is required"}
	}
	if _, err := a.s.CreateGateway(ctx, in.Name, domain.CreateGatewayOptions{}); err != nil {
		return nil, mapDomainErr(err)
	}
	now := time.Now().UTC()
	pt := in.ProtocolType
	return &gen.CreateApiResponse{
		ApiId:                     strPtr(in.Name),
		Name:                      strPtr(in.Name),
		ProtocolType:              &pt,
		ApiEndpoint:               strPtr(apiEndpointURL(in.Name)),
		CreatedDate:               &now,
		ApiKeySelectionExpression: strPtr("$request.header.x-api-key"),
		RouteSelectionExpression:  strPtr("$request.method $request.path"),
	}, nil
}

func (a *Adapter) GetApi(ctx context.Context, in *gen.GetApiRequest) (*gen.GetApiResponse, error) {
	g, err := a.s.DescribeGateway(ctx, in.ApiId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	api := gatewayToAPI(g)
	pt := api.ProtocolType
	return &gen.GetApiResponse{
		ApiId:                     api.ApiId,
		Name:                      strPtr(api.Name),
		ProtocolType:              &pt,
		ApiEndpoint:               api.ApiEndpoint,
		CreatedDate:               api.CreatedDate,
		ApiKeySelectionExpression: api.ApiKeySelectionExpression,
		RouteSelectionExpression:  strPtr(api.RouteSelectionExpression),
	}, nil
}

func (a *Adapter) GetApis(ctx context.Context, in *gen.GetApisRequest) (*gen.GetApisResponse, error) {
	res, err := a.s.ListGateways(ctx, domain.ListGatewaysOptions{})
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.GetApisResponse{}
	for _, g := range res.Gateways {
		out.Items = append(out.Items, *gatewayToAPI(g))
	}
	return out, nil
}

func (a *Adapter) UpdateApi(ctx context.Context, in *gen.UpdateApiRequest) (*gen.UpdateApiResponse, error) {
	g, err := a.s.DescribeGateway(ctx, in.ApiId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	api := gatewayToAPI(g)
	pt := api.ProtocolType
	return &gen.UpdateApiResponse{
		ApiId:                     api.ApiId,
		Name:                      strPtr(api.Name),
		ProtocolType:              &pt,
		ApiEndpoint:               api.ApiEndpoint,
		CreatedDate:               api.CreatedDate,
		ApiKeySelectionExpression: api.ApiKeySelectionExpression,
		RouteSelectionExpression:  strPtr(api.RouteSelectionExpression),
	}, nil
}

func (a *Adapter) DeleteApi(ctx context.Context, in *gen.DeleteApiRequest) (struct{}, error) {
	if err := a.s.DeleteGateway(ctx, in.ApiId); err != nil {
		return struct{}{}, mapDomainErr(err)
	}
	a.mu.Lock()
	delete(a.pending, in.ApiId)
	delete(a.intIDs, in.ApiId)
	a.mu.Unlock()
	return struct{}{}, nil
}

// ---------------------------------------------------------------------
// Route / Integration / Deployment.
// ---------------------------------------------------------------------

func (a *Adapter) CreateRoute(ctx context.Context, in *gen.CreateRouteRequest) (*gen.CreateRouteResult, error) {
	method, path, ok := splitRouteKey(in.RouteKey)
	if !ok {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "BadRequestException", Message: "RouteKey must be METHOD /path (got " + in.RouteKey + ")"}
	}
	backend := ""
	target := strDeref(in.Target)
	if strings.HasPrefix(target, "integrations/") {
		intID := strings.TrimPrefix(target, "integrations/")
		a.mu.Lock()
		if intMap, ok := a.intIDs[in.ApiId]; ok {
			backend = intMap[intID]
		}
		a.mu.Unlock()
	}
	routeID := generateID()
	a.mu.Lock()
	a.pending[in.ApiId] = append(a.pending[in.ApiId], domain.Route{
		Method: method, Path: path, Backend: backend,
	})
	a.mu.Unlock()
	return &gen.CreateRouteResult{
		RouteId:  strPtr(routeID),
		RouteKey: strPtr(in.RouteKey),
		Target:   in.Target,
	}, nil
}

func (a *Adapter) DeleteRoute(ctx context.Context, in *gen.DeleteRouteRequest) (struct{}, error) {
	a.mu.Lock()
	delete(a.pending, in.ApiId)
	a.mu.Unlock()
	_ = in.RouteId
	return struct{}{}, nil
}

func (a *Adapter) GetRoutes(ctx context.Context, in *gen.GetRoutesRequest) (*gen.GetRoutesResponse, error) {
	g, err := a.s.DescribeGateway(ctx, in.ApiId)
	if err != nil {
		return nil, mapDomainErr(err)
	}
	out := &gen.GetRoutesResponse{}
	for _, rt := range g.Routes {
		key := rt.Method + " " + rt.Path
		out.Items = append(out.Items, gen.Route{
			RouteId:  strPtr(generateID()),
			RouteKey: key,
		})
	}
	return out, nil
}

func (a *Adapter) CreateIntegration(ctx context.Context, in *gen.CreateIntegrationRequest) (*gen.CreateIntegrationResult, error) {
	uri := strDeref(in.IntegrationUri)
	if uri == "" {
		return nil, &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "BadRequestException", Message: "integrationUri is required"}
	}
	intID := generateID()
	a.mu.Lock()
	if a.intIDs[in.ApiId] == nil {
		a.intIDs[in.ApiId] = map[string]string{}
	}
	a.intIDs[in.ApiId][intID] = uri
	a.mu.Unlock()
	intType := in.IntegrationType
	return &gen.CreateIntegrationResult{
		IntegrationId:   strPtr(intID),
		IntegrationType: &intType,
		IntegrationUri:  in.IntegrationUri,
	}, nil
}

func (a *Adapter) DeleteIntegration(ctx context.Context, in *gen.DeleteIntegrationRequest) (struct{}, error) {
	a.mu.Lock()
	if intMap, ok := a.intIDs[in.ApiId]; ok {
		delete(intMap, in.IntegrationId)
	}
	a.mu.Unlock()
	return struct{}{}, nil
}

func (a *Adapter) GetIntegrations(ctx context.Context, in *gen.GetIntegrationsRequest) (*gen.GetIntegrationsResponse, error) {
	out := &gen.GetIntegrationsResponse{}
	a.mu.Lock()
	for id, uri := range a.intIDs[in.ApiId] {
		idCopy, uriCopy := id, uri
		intType := gen.IntegrationType("HTTP_PROXY")
		out.Items = append(out.Items, gen.Integration{
			IntegrationId:   &idCopy,
			IntegrationType: &intType,
			IntegrationUri:  &uriCopy,
		})
	}
	a.mu.Unlock()
	return out, nil
}

func (a *Adapter) CreateDeployment(ctx context.Context, in *gen.CreateDeploymentRequest) (*gen.CreateDeploymentResponse, error) {
	a.mu.Lock()
	routes := a.pending[in.ApiId]
	a.pending[in.ApiId] = nil
	a.mu.Unlock()
	if err := a.s.DeployGateway(ctx, in.ApiId, domain.DeployGatewayOptions{Routes: routes}); err != nil {
		return nil, mapDomainErr(err)
	}
	now := time.Now().UTC()
	status := gen.DeploymentStatus("DEPLOYED")
	return &gen.CreateDeploymentResponse{
		DeploymentId:     strPtr(generateID()),
		DeploymentStatus: &status,
		CreatedDate:      &now,
	}, nil
}

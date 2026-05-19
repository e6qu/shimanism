// Package aws_apigatewayv2 is the AWS API Gateway HTTP API v2-shaped
// HTTP frontend. restJson1 wire protocol.
//
// API Gateway v2 splits the gateway-creation flow across multiple
// resources (Api + Routes + Integrations). The shim collapses
// these into the domain's declarative DeployGateway model: the
// frontend tracks per-gateway state internally — no — it doesn't:
// per the stateless rule, the backend remembers the routes.
// CreateApi creates an empty gateway; clients populate routes via
// CreateRoute/CreateIntegration calls; CreateDeployment performs
// the atomic DeployGateway against the accumulated routes.
//
// Sub-phase 8.3 ships the minimal subset: CreateApi, GetApi,
// GetApis, DeleteApi. CreateRoute/Integration/Deployment are
// stubs that store in-frontend state until persisted via
// CreateDeployment; the frontend re-emits the route set as the
// gateway's spec via DeployGateway on the domain.
//
// **Stateless caveat.** Since the AWS multi-step flow accumulates
// in-flight state that doesn't exist in the domain, the frontend
// uses a per-request collection by API ID. This is per-process
// memory only; the deployed routing table lives in the backend.
// Tests that rely on multi-call accumulation are documented as
// best-effort under this phase.
package aws_apigatewayv2

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Server struct {
	s domain.APIGateway

	// In-flight accumulation of routes per ApiId. Used to bridge
	// AWS's multi-call flow (CreateApi → CreateRoute × N →
	// CreateDeployment) onto the domain's atomic DeployGateway.
	// Cleared after each successful CreateDeployment.
	mu      sync.Mutex
	pending map[string][]domain.Route    // apiID → routes
	intIDs  map[string]map[string]string // apiID → integrationID → targetURL
}

func New(s domain.APIGateway) *Server {
	return &Server{
		s:       s,
		pending: map[string][]domain.Route{},
		intIDs:  map[string]map[string]string{},
	}
}

var (
	reApis         = regexp.MustCompile(`^/v2/apis/?$`)
	reApi          = regexp.MustCompile(`^/v2/apis/([^/]+)$`)
	reRoutes       = regexp.MustCompile(`^/v2/apis/([^/]+)/routes/?$`)
	reRoute        = regexp.MustCompile(`^/v2/apis/([^/]+)/routes/([^/]+)$`)
	reIntegrations = regexp.MustCompile(`^/v2/apis/([^/]+)/integrations/?$`)
	reIntegration  = regexp.MustCompile(`^/v2/apis/([^/]+)/integrations/([^/]+)$`)
	reDeployments  = regexp.MustCompile(`^/v2/apis/([^/]+)/deployments/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reRoute.FindStringSubmatch(path); m != nil && method == http.MethodDelete {
		srv.deleteRoute(w, r, m[1], m[2])
		return
	}
	if m := reIntegration.FindStringSubmatch(path); m != nil && method == http.MethodDelete {
		srv.deleteIntegration(w, r, m[1], m[2])
		return
	}
	if m := reRoutes.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPost:
			srv.createRoute(w, r, m[1])
		case http.MethodGet:
			srv.getRoutes(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "BadRequestException", method+" not allowed on routes")
		}
		return
	}
	if m := reIntegrations.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPost:
			srv.createIntegration(w, r, m[1])
		case http.MethodGet:
			srv.getIntegrations(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "BadRequestException", method+" not allowed on integrations")
		}
		return
	}
	if m := reDeployments.FindStringSubmatch(path); m != nil && method == http.MethodPost {
		srv.createDeployment(w, r, m[1])
		return
	}
	if m := reApi.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getApi(w, r, m[1])
		case http.MethodDelete:
			srv.deleteApi(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "BadRequestException", method+" not allowed on api")
		}
		return
	}
	if reApis.MatchString(path) {
		switch method {
		case http.MethodPost:
			srv.createApi(w, r)
			return
		case http.MethodGet:
			srv.getApis(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NotFoundException",
		"no API Gateway v2 route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Wire shapes (subset of API Gateway v2 schema)
// ----------------------------------------------------------------------

// API Gateway v2 uses restJson1 with explicit `@jsonName` traits in
// the Smithy spec — JSON field names are camelCase (apiId, name,
// routeKey), NOT PascalCase (ApiId, Name, RouteKey). The SDK
// silently drops fields that don't match the @jsonName.

type createApiRequest struct {
	Name         string `json:"name"`
	ProtocolType string `json:"protocolType"`
}

type apiResponse struct {
	ApiId        string `json:"apiId"`
	Name         string `json:"name"`
	ProtocolType string `json:"protocolType"`
	ApiEndpoint  string `json:"apiEndpoint,omitempty"`
	CreatedDate  string `json:"createdDate,omitempty"`
}

type getApisResponse struct {
	Items []*apiResponse `json:"items"`
}

type createRouteRequest struct {
	RouteKey string `json:"routeKey"`
	Target   string `json:"target,omitempty"`
}

type routeResponse struct {
	RouteId  string `json:"routeId"`
	RouteKey string `json:"routeKey"`
	Target   string `json:"target,omitempty"`
}

type getRoutesResponse struct {
	Items []*routeResponse `json:"items"`
}

type createIntegrationRequest struct {
	IntegrationType      string `json:"integrationType"`
	IntegrationUri       string `json:"integrationUri"`
	IntegrationMethod    string `json:"integrationMethod,omitempty"`
	PayloadFormatVersion string `json:"payloadFormatVersion,omitempty"`
}

type integrationResponse struct {
	IntegrationId   string `json:"integrationId"`
	IntegrationType string `json:"integrationType"`
	IntegrationUri  string `json:"integrationUri"`
}

type getIntegrationsResponse struct {
	Items []*integrationResponse `json:"items"`
}

type createDeploymentResponse struct {
	DeploymentId     string `json:"deploymentId"`
	DeploymentStatus string `json:"deploymentStatus"`
}

// ----------------------------------------------------------------------
// Handlers — Api
// ----------------------------------------------------------------------

func (srv *Server) createApi(w http.ResponseWriter, r *http.Request) {
	var body createApiRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Name == "" {
		writeError(w, http.StatusBadRequest, "BadRequestException", "Name is required")
		return
	}
	if _, err := srv.s.CreateGateway(r.Context(), body.Name, domain.CreateGatewayOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, &apiResponse{
		ApiId:        body.Name,
		Name:         body.Name,
		ProtocolType: body.ProtocolType,
		ApiEndpoint:  apiEndpointURL(body.Name),
		CreatedDate:  time.Now().UTC().Format(time.RFC3339),
	})
}

func (srv *Server) getApi(w http.ResponseWriter, r *http.Request, name string) {
	g, err := srv.s.DescribeGateway(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gatewayToAPI(g))
}

func (srv *Server) getApis(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListGateways(r.Context(), domain.ListGatewaysOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := getApisResponse{}
	for _, g := range res.Gateways {
		out.Items = append(out.Items, gatewayToAPI(g))
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) deleteApi(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteGateway(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	srv.mu.Lock()
	delete(srv.pending, name)
	delete(srv.intIDs, name)
	srv.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// ----------------------------------------------------------------------
// Handlers — Route / Integration / Deployment
// ----------------------------------------------------------------------

func (srv *Server) createRoute(w http.ResponseWriter, r *http.Request, apiID string) {
	var body createRouteRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	method, path, ok := splitRouteKey(body.RouteKey)
	if !ok {
		writeError(w, http.StatusBadRequest, "BadRequestException",
			"RouteKey must be METHOD /path (got "+body.RouteKey+")")
		return
	}
	backend := ""
	if strings.HasPrefix(body.Target, "integrations/") {
		intID := strings.TrimPrefix(body.Target, "integrations/")
		srv.mu.Lock()
		if intMap, ok := srv.intIDs[apiID]; ok {
			backend = intMap[intID]
		}
		srv.mu.Unlock()
	}
	routeID := generateID()
	srv.mu.Lock()
	srv.pending[apiID] = append(srv.pending[apiID], domain.Route{
		Method:  method,
		Path:    path,
		Backend: backend,
	})
	srv.mu.Unlock()
	writeJSON(w, http.StatusCreated, &routeResponse{
		RouteId:  routeID,
		RouteKey: body.RouteKey,
		Target:   body.Target,
	})
}

func (srv *Server) deleteRoute(w http.ResponseWriter, r *http.Request, apiID, routeID string) {
	srv.mu.Lock()
	delete(srv.pending, apiID)
	srv.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
	_ = routeID
}

func (srv *Server) getRoutes(w http.ResponseWriter, r *http.Request, apiID string) {
	g, err := srv.s.DescribeGateway(r.Context(), apiID)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := getRoutesResponse{}
	for _, rt := range g.Routes {
		out.Items = append(out.Items, &routeResponse{
			RouteId:  generateID(),
			RouteKey: rt.Method + " " + rt.Path,
		})
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) createIntegration(w http.ResponseWriter, r *http.Request, apiID string) {
	var body createIntegrationRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.IntegrationUri == "" {
		writeError(w, http.StatusBadRequest, "BadRequestException", "IntegrationUri is required")
		return
	}
	intID := generateID()
	srv.mu.Lock()
	if srv.intIDs[apiID] == nil {
		srv.intIDs[apiID] = map[string]string{}
	}
	srv.intIDs[apiID][intID] = body.IntegrationUri
	srv.mu.Unlock()
	writeJSON(w, http.StatusCreated, &integrationResponse{
		IntegrationId:   intID,
		IntegrationType: body.IntegrationType,
		IntegrationUri:  body.IntegrationUri,
	})
}

func (srv *Server) deleteIntegration(w http.ResponseWriter, r *http.Request, apiID, intID string) {
	srv.mu.Lock()
	if intMap, ok := srv.intIDs[apiID]; ok {
		delete(intMap, intID)
	}
	srv.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) getIntegrations(w http.ResponseWriter, r *http.Request, apiID string) {
	out := getIntegrationsResponse{}
	srv.mu.Lock()
	for id, uri := range srv.intIDs[apiID] {
		out.Items = append(out.Items, &integrationResponse{
			IntegrationId:   id,
			IntegrationType: "HTTP_PROXY",
			IntegrationUri:  uri,
		})
	}
	srv.mu.Unlock()
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) createDeployment(w http.ResponseWriter, r *http.Request, apiID string) {
	// Publish the accumulated routes via DeployGateway. Atomic swap.
	srv.mu.Lock()
	routes := srv.pending[apiID]
	srv.pending[apiID] = nil
	srv.mu.Unlock()
	if err := srv.s.DeployGateway(r.Context(), apiID, domain.DeployGatewayOptions{Routes: routes}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, &createDeploymentResponse{
		DeploymentId:     generateID(),
		DeploymentStatus: "DEPLOYED",
	})
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func apiEndpointURL(name string) string {
	return "https://" + name + ".execute-api.us-east-1.amazonaws.com"
}

func gatewayToAPI(g domain.Gateway) *apiResponse {
	out := &apiResponse{
		ApiId:        g.Name,
		Name:         g.Name,
		ProtocolType: "HTTP",
		CreatedDate:  g.CreatedAt.UTC().Format(time.RFC3339),
	}
	if g.Status == domain.StatusAvailable {
		if g.Endpoint.URL != "" {
			out.ApiEndpoint = g.Endpoint.URL
		} else {
			out.ApiEndpoint = apiEndpointURL(g.Name)
		}
	}
	return out
}

func splitRouteKey(rk string) (method, path string, ok bool) {
	i := strings.IndexByte(rk, ' ')
	if i < 0 {
		return "", "", false
	}
	return rk[:i], rk[i+1:], true
}

func generateID() string {
	// Short hex from the nanosecond clock; sufficient for test
	// uniqueness within a process.
	t := time.Now().UnixNano()
	return strings.ReplaceAll(
		strings.TrimPrefix(
			strings.ToLower(time.Unix(0, t).Format("150405.000000000")),
			""), ".", "")
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequestException", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequestException", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

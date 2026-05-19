// Package gcp_apigateway is the GCP API Gateway REST/JSON frontend.
package gcp_apigateway

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Server struct {
	s domain.APIGateway
}

func New(s domain.APIGateway) *Server { return &Server{s: s} }

var (
	reGateways   = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/gateways/?$`)
	reGateway    = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/gateways/([^/:]+)$`)
	reApis       = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/global/apis/?$`)
	reApi        = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/global/apis/([^/:]+)$`)
	reApiConfigs = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/global/apis/([^/:]+)/configs/?$`)
	reApiConfig  = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/global/apis/([^/:]+)/configs/([^/:]+)$`)
)

type gatewayResource struct {
	Name            string `json:"name,omitempty"`
	DisplayName     string `json:"displayName,omitempty"`
	ApiConfig       string `json:"apiConfig,omitempty"`
	State           string `json:"state,omitempty"`
	DefaultHostname string `json:"defaultHostname,omitempty"`
	CreateTime      string `json:"createTime,omitempty"`
}

type listResponse struct {
	Gateways []*gatewayResource `json:"gateways"`
}

type operation struct {
	Name string `json:"name"`
	Done bool   `json:"done"`
}

type apiResource struct {
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	CreateTime  string `json:"createTime,omitempty"`
}

type apiConfigResource struct {
	Name             string                    `json:"name,omitempty"`
	DisplayName      string                    `json:"displayName,omitempty"`
	State            string                    `json:"state,omitempty"`
	CreateTime       string                    `json:"createTime,omitempty"`
	OpenapiDocuments []apiConfigOpenAPIDocument `json:"openapiDocuments,omitempty"`
}

type apiConfigOpenAPIDocument struct {
	Document apiConfigFile `json:"document"`
}

type apiConfigFile struct {
	Path     string `json:"path,omitempty"`
	Contents string `json:"contents,omitempty"`
}

type apisListResponse struct {
	Apis []*apiResource `json:"apis"`
}

type apiConfigsListResponse struct {
	ApiConfigs []*apiConfigResource `json:"apiConfigs"`
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	// Most-specific routes first.
	if m := reApiConfig.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getApiConfig(w, r, m[2], m[3])
		case http.MethodDelete:
			srv.deleteApiConfig(w, r, m[2], m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed")
		}
		return
	}
	if m := reApiConfigs.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.listApiConfigs(w, r, m[2])
			return
		case http.MethodPost:
			srv.createApiConfig(w, r, m[2])
			return
		}
	}
	if m := reApi.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getApi(w, r, m[2])
		case http.MethodDelete:
			srv.deleteApi(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed")
		}
		return
	}
	if reApis.MatchString(path) {
		switch method {
		case http.MethodGet:
			srv.listApis(w, r)
			return
		case http.MethodPost:
			srv.createApi(w, r)
			return
		}
	}
	if m := reGateway.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getGateway(w, r, m[3])
		case http.MethodDelete:
			srv.deleteGateway(w, r, m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed")
		}
		return
	}
	if m := reGateways.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.listGateways(w, r, m[1], m[2])
			return
		case http.MethodPost:
			srv.createGateway(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "no route matches "+method+" "+path)
}

func (srv *Server) createGateway(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("gatewayId")
	_ = readBody(w, r)
	if _, err := srv.s.CreateGateway(r.Context(), name, domain.CreateGatewayOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) getGateway(w http.ResponseWriter, r *http.Request, name string) {
	g, err := srv.s.DescribeGateway(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gatewayToGCP(g, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path)))
}

func (srv *Server) deleteGateway(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteGateway(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) listGateways(w http.ResponseWriter, r *http.Request, project, location string) {
	res, err := srv.s.ListGateways(r.Context(), domain.ListGatewaysOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listResponse{}
	for _, g := range res.Gateways {
		out.Gateways = append(out.Gateways, gatewayToGCP(g, project, location))
	}
	writeJSON(w, http.StatusOK, &out)
}

func gatewayToGCP(g domain.Gateway, project, location string) *gatewayResource {
	state := "STATE_UNSPECIFIED"
	switch g.Status {
	case domain.StatusAvailable:
		state = "ACTIVE"
	case domain.StatusCreating:
		state = "CREATING"
	case domain.StatusUpdating:
		state = "UPDATING"
	case domain.StatusDeleting:
		state = "DELETING"
	}
	out := &gatewayResource{
		Name:        fmt.Sprintf("projects/%s/locations/%s/gateways/%s", project, location, g.Name),
		DisplayName: g.Name,
		State:       state,
	}
	if !g.CreatedAt.IsZero() {
		out.CreateTime = g.CreatedAt.UTC().Format(time.RFC3339)
	}
	if g.Status == domain.StatusAvailable && g.Endpoint.URL != "" {
		out.DefaultHostname = strings.TrimPrefix(strings.TrimPrefix(g.Endpoint.URL, "https://"), "http://")
	}
	return out
}

func projectFromPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/v1/projects/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func locationFromPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/v1/projects/"), "/")
	if len(parts) >= 3 && parts[1] == "locations" {
		return parts[2]
	}
	return "us-central1"
}

func readBody(w http.ResponseWriter, r *http.Request) []byte {
	body, _ := io.ReadAll(r.Body)
	return body
}

// --- Api endpoint family --------------------------------------
//
// In GCP API Gateway, an `Api` is the umbrella object; a Gateway
// points at an ApiConfig which belongs to an Api. The shim
// collapses Api ↔ Gateway 1:1 — the apiId becomes the gateway
// name. This is the simplest honest mapping for the intersection;
// it works because Phase 8's intersection is 1 Api : 1 Gateway.

func (srv *Server) createApi(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("apiId")
	_ = readBody(w, r)
	if _, err := srv.s.CreateGateway(r.Context(), name, domain.CreateGatewayOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) getApi(w http.ResponseWriter, r *http.Request, name string) {
	g, err := srv.s.DescribeGateway(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := &apiResource{
		Name:        fmt.Sprintf("projects/%s/locations/global/apis/%s", projectFromPath(r.URL.Path), name),
		DisplayName: name,
	}
	if !g.CreatedAt.IsZero() {
		out.CreateTime = g.CreatedAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteApi(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteGateway(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) listApis(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListGateways(r.Context(), domain.ListGatewaysOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := apisListResponse{}
	project := projectFromPath(r.URL.Path)
	for _, g := range res.Gateways {
		out.Apis = append(out.Apis, &apiResource{
			Name:        fmt.Sprintf("projects/%s/locations/global/apis/%s", project, g.Name),
			DisplayName: g.Name,
		})
	}
	writeJSON(w, http.StatusOK, &out)
}

// --- ApiConfigs endpoint family --------------------------------
//
// ApiConfigs.Create with an OpenAPI document is the GCP-shaped
// route-deployment surface (the migration counterpart to AWS's
// CreateDeployment). The frontend parses the OpenAPI document's
// paths into domain.Route entries and dispatches to
// domain.DeployGateway. No fake — this is exactly what GCP API
// Gateway does behind its own API.

func (srv *Server) createApiConfig(w http.ResponseWriter, r *http.Request, apiName string) {
	body := readBody(w, r)
	var cfg apiConfigResource
	if len(body) > 0 {
		if err := json.Unmarshal(body, &cfg); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body: "+err.Error())
			return
		}
	}
	routes, err := routesFromOpenAPI(cfg.OpenapiDocuments)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	if err := srv.s.DeployGateway(r.Context(), apiName, domain.DeployGatewayOptions{Routes: routes}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) getApiConfig(w http.ResponseWriter, r *http.Request, apiName, cfgID string) {
	g, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	state := "ACTIVE"
	if g.Status != domain.StatusAvailable {
		state = "CREATING"
	}
	out := &apiConfigResource{
		Name:        fmt.Sprintf("projects/%s/locations/global/apis/%s/configs/%s", projectFromPath(r.URL.Path), apiName, cfgID),
		DisplayName: cfgID,
		State:       state,
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteApiConfig(w http.ResponseWriter, r *http.Request, apiName, cfgID string) {
	// Deleting a config equates to clearing the route table on the gateway.
	_ = cfgID
	if err := srv.s.DeployGateway(r.Context(), apiName, domain.DeployGatewayOptions{Routes: nil}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &operation{Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano())})
}

func (srv *Server) listApiConfigs(w http.ResponseWriter, r *http.Request, apiName string) {
	_, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	// Intersection collapses Api ↔ Gateway 1:1, so each Api has one
	// canonical "current" config.
	out := apiConfigsListResponse{ApiConfigs: []*apiConfigResource{{
		Name:        fmt.Sprintf("projects/%s/locations/global/apis/%s/configs/current", projectFromPath(r.URL.Path), apiName),
		DisplayName: "current",
		State:       "ACTIVE",
	}}}
	writeJSON(w, http.StatusOK, &out)
}

// routesFromOpenAPI extracts (method, path, backend) tuples from
// the OpenAPI 2.0 / 3.0 document a Cloud SDK client embeds in
// ApiConfig.openapiDocuments[0].document.contents (YAML). The
// document is the user's source-of-truth for the route table; the
// shim parses it into domain.Route entries and lets the backend
// deploy them.
func routesFromOpenAPI(docs []apiConfigOpenAPIDocument) ([]domain.Route, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	contents := docs[0].Document.Contents
	if contents == "" {
		return nil, nil
	}
	// The contents come base64-encoded per the GCP API; decode if so.
	if decoded, err := base64.StdEncoding.DecodeString(contents); err == nil && len(decoded) > 0 {
		contents = string(decoded)
	}
	// Minimal OpenAPI YAML parser: scan for `<indent>/<path>:` blocks
	// followed by lowercase HTTP method keys. This deliberately
	// avoids pulling in a full OpenAPI library — the shim only needs
	// method+path+backend out of the spec.
	var routes []domain.Route
	lines := strings.Split(contents, "\n")
	var currentPath string
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// Path entry like `  /healthz:`.
		if strings.HasPrefix(strings.TrimLeft(l, " "), "/") && strings.HasSuffix(trimmed, ":") {
			currentPath = strings.TrimSuffix(trimmed, ":")
			continue
		}
		// Method entry under a path.
		if currentPath != "" {
			lowered := strings.TrimSuffix(trimmed, ":")
			switch lowered {
			case "get", "post", "put", "delete", "patch", "options", "head":
				backend := scanBackendAddress(lines, i)
				routes = append(routes, domain.Route{
					Method:  strings.ToUpper(lowered),
					Path:    currentPath,
					Backend: backend,
				})
			}
		}
	}
	return routes, nil
}

// scanBackendAddress looks ahead from idx for the
// `x-google-backend: address: <url>` extension under the same
// operation. Returns "" if not found within the next 30 lines.
func scanBackendAddress(lines []string, idx int) string {
	limit := idx + 30
	if limit > len(lines) {
		limit = len(lines)
	}
	for i := idx + 1; i < limit; i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "address:") {
			return strings.TrimSpace(strings.TrimPrefix(l, "address:"))
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Package azure_apim is the Azure API Management REST frontend.
// ARM URL shape with Microsoft.ApiManagement/service/{svc}/apis/{name}.
package azure_apim

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Server struct {
	s domain.APIGateway
}

func New(s domain.APIGateway) *Server { return &Server{s: s} }

var (
	reApi     = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/([^/]+)/?$`)
	reApiList = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/?$`)
	reOp      = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/([^/]+)/operations/([^/]+)/?$`)
	reOpList  = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/([^/]+)/operations/?$`)
)

type apiBody struct {
	Properties *apiProperties `json:"properties,omitempty"`
}

type apiProperties struct {
	DisplayName       string   `json:"displayName,omitempty"`
	Path              string   `json:"path,omitempty"`
	Protocols         []string `json:"protocols,omitempty"`
	ServiceURL        string   `json:"serviceUrl,omitempty"`
	ProvisioningState string   `json:"provisioningState,omitempty"`
}

type apiResource struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	Properties *apiProperties `json:"properties,omitempty"`
}

type listResponse struct {
	Value []*apiResource `json:"value"`
}

type operationBody struct {
	Properties *operationProperties `json:"properties,omitempty"`
}

type operationProperties struct {
	DisplayName string `json:"displayName,omitempty"`
	Method      string `json:"method,omitempty"`
	URLTemplate string `json:"urlTemplate,omitempty"`
}

type operationResource struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Type       string               `json:"type"`
	Properties *operationProperties `json:"properties,omitempty"`
}

type operationListResponse struct {
	Value []*operationResource `json:"value"`
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	// Most-specific routes first.
	if m := reOp.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createOrUpdateOp(w, r, m[1], m[2])
		case http.MethodGet:
			srv.getOp(w, r, m[1], m[2])
		case http.MethodDelete:
			srv.deleteOp(w, r, m[1], m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed")
		}
		return
	}
	if m := reOpList.FindStringSubmatch(path); m != nil && method == http.MethodGet {
		srv.listOps(w, r, m[1])
		return
	}
	if m := reApi.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.create(w, r, m[1])
		case http.MethodGet:
			srv.get(w, r, m[1])
		case http.MethodDelete:
			srv.delete(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed")
		}
		return
	}
	if reApiList.MatchString(path) && method == http.MethodGet {
		srv.list(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound", "no APIM route matches "+method+" "+path)
}

// --- Operations subresource ------------------------------------
//
// APIM Operations are the per-route surface. `armapimanagement`'s
// `APIOperationClient` exposes Create/Update/Get/Delete/List against
// `/apis/{api}/operations/{op}`. The shim collects the requested
// operations and (re-)dispatches them as a single
// `domain.DeployGateway` call so the backend sees the full route
// table atomically.

func (srv *Server) createOrUpdateOp(w http.ResponseWriter, r *http.Request, apiName, opID string) {
	var body operationBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Properties == nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "properties.{method,urlTemplate} required")
		return
	}
	// Read current routes via DescribeGateway, merge in this op,
	// dispatch the full set. The shim is stateless — the gateway's
	// current routes (as stored by the backend) are the source of
	// truth.
	g, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	routes := dropRouteByID(g.Routes, opID)
	routes = append(routes, domain.Route{
		Method: body.Properties.Method,
		Path:   body.Properties.URLTemplate,
		ID:     opID,
	})
	if err := srv.s.DeployGateway(r.Context(), apiName, domain.DeployGatewayOptions{Routes: routes}); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, operationToARM(apiName, opID, body.Properties))
}

func (srv *Server) getOp(w http.ResponseWriter, r *http.Request, apiName, opID string) {
	g, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	for _, rt := range g.Routes {
		if rt.ID == opID {
			writeJSON(w, http.StatusOK, operationToARM(apiName, opID, &operationProperties{
				DisplayName: opID,
				Method:      rt.Method,
				URLTemplate: rt.Path,
			}))
			return
		}
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound", "operation "+opID+" not found")
}

func (srv *Server) deleteOp(w http.ResponseWriter, r *http.Request, apiName, opID string) {
	g, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	routes := dropRouteByID(g.Routes, opID)
	if err := srv.s.DeployGateway(r.Context(), apiName, domain.DeployGatewayOptions{Routes: routes}); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) listOps(w http.ResponseWriter, r *http.Request, apiName string) {
	g, err := srv.s.DescribeGateway(r.Context(), apiName)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := operationListResponse{}
	for _, rt := range g.Routes {
		id := rt.ID
		if id == "" {
			id = strings.ReplaceAll(rt.Method+rt.Path, "/", "_")
		}
		out.Value = append(out.Value, operationToARM(apiName, id, &operationProperties{
			DisplayName: id,
			Method:      rt.Method,
			URLTemplate: rt.Path,
		}))
	}
	writeJSON(w, http.StatusOK, &out)
}

func operationToARM(apiName, opID string, p *operationProperties) *operationResource {
	return &operationResource{
		Name:       opID,
		Type:       "Microsoft.ApiManagement/service/apis/operations",
		Properties: p,
	}
}

func dropRouteByID(routes []domain.Route, id string) []domain.Route {
	out := make([]domain.Route, 0, len(routes))
	for _, r := range routes {
		if r.ID == id {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (srv *Server) create(w http.ResponseWriter, r *http.Request, name string) {
	var body apiBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, err := srv.s.CreateGateway(r.Context(), name, domain.CreateGatewayOptions{}); err != nil {
		mapDomainError(w, err)
		return
	}
	g, _ := srv.s.DescribeGateway(r.Context(), name)
	writeJSON(w, http.StatusCreated, gatewayToARM(g))
}

func (srv *Server) get(w http.ResponseWriter, r *http.Request, name string) {
	g, err := srv.s.DescribeGateway(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gatewayToARM(g))
}

func (srv *Server) delete(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteGateway(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) list(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListGateways(r.Context(), domain.ListGatewaysOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listResponse{}
	for _, g := range res.Gateways {
		out.Value = append(out.Value, gatewayToARM(g))
	}
	writeJSON(w, http.StatusOK, &out)
}

func gatewayToARM(g domain.Gateway) *apiResource {
	state := "Succeeded"
	switch g.Status {
	case domain.StatusCreating, domain.StatusUpdating:
		state = "InProgress"
	case domain.StatusDeleting:
		state = "Deleting"
	}
	return &apiResource{
		Name: g.Name,
		Type: "Microsoft.ApiManagement/service/apis",
		Properties: &apiProperties{
			DisplayName:       g.Name,
			Path:              "/" + g.Name,
			Protocols:         []string{"https"},
			ServiceURL:        g.Endpoint.URL,
			ProvisioningState: state,
		},
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

type armErrorResponse struct {
	Error armError `json:"error"`
}

type armError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(armErrorResponse{Error: armError{Code: code, Message: message}})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchGateway:
		writeError(w, http.StatusNotFound, "ResourceNotFound", de.Error())
	case domain.KindGatewayAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Error())
	default:
		writeError(w, http.StatusBadRequest, "BadRequest", de.Error())
	}
}

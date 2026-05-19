// Package azure_apim is the Azure API Management REST frontend.
// ARM URL shape with Microsoft.ApiManagement/service/{svc}/apis/{name}.
package azure_apim

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type Server struct {
	s domain.APIGateway
}

func New(s domain.APIGateway) *Server { return &Server{s: s} }

var (
	reApi     = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/([^/]+)/?$`)
	reApiList = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.ApiManagement/service/[^/]+/apis/?$`)
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

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
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

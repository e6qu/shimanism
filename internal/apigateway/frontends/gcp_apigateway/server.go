// Package gcp_apigateway is the GCP API Gateway REST/JSON frontend.
package gcp_apigateway

import (
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
	reGateways = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/gateways/?$`)
	reGateway  = regexp.MustCompile(`^/v1/projects/([^/]+)/locations/([^/]+)/gateways/([^/:]+)$`)
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

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
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

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

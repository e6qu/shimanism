// Package gcp_cloudrun is the GCP Cloud Run-shaped REST/JSON
// frontend.
package gcp_cloudrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	runapi "google.golang.org/api/run/v2"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Server struct {
	s domain.Functions
}

func New(s domain.Functions) *Server { return &Server{s: s} }

var (
	reServices = regexp.MustCompile(`^/v2/projects/([^/]+)/locations/([^/]+)/services/?$`)
	reService  = regexp.MustCompile(`^/v2/projects/([^/]+)/locations/([^/]+)/services/([^/]+)$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	if m := reService.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getService(w, r, m[3])
		case http.MethodDelete:
			srv.deleteService(w, r, m[3])
		case http.MethodPatch:
			srv.patchService(w, r, m[3])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on service")
		}
		return
	}
	if m := reServices.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.listServices(w, r)
			return
		case http.MethodPost:
			srv.createService(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no Cloud Run route matches "+method+" "+path)
}

func (srv *Server) createService(w http.ResponseWriter, r *http.Request) {
	var body runapi.GoogleCloudRunV2Service
	if !decodeJSON(w, r, &body) {
		return
	}
	name := r.URL.Query().Get("serviceId")
	opt := domain.CreateFunctionOptions{}
	if body.Template != nil && len(body.Template.Containers) > 0 {
		c := body.Template.Containers[0]
		opt.Image = c.Image
		if len(c.Env) > 0 {
			opt.Environment = map[string]string{}
			for _, e := range c.Env {
				opt.Environment[e.Name] = e.Value
			}
		}
		if c.Resources != nil {
			if m, ok := c.Resources.Limits["memory"]; ok {
				opt.MemoryBytes = parseMemoryString(m)
			}
			if cpu, ok := c.Resources.Limits["cpu"]; ok {
				opt.CPUMilliCores = parseCPUString(cpu)
			}
		}
	}
	if _, err := srv.s.CreateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name)
}

func (srv *Server) getService(w http.ResponseWriter, r *http.Request, name string) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, functionToGCP(fn, projectFromPath(r.URL.Path), locationFromPath(r.URL.Path)))
}

func (srv *Server) listServices(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListFunctions(r.Context(), domain.ListFunctionsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	location := locationFromPath(r.URL.Path)
	out := runapi.GoogleCloudRunV2ListServicesResponse{}
	for _, fn := range res.Functions {
		out.Services = append(out.Services, functionToGCP(fn, project, location))
	}
	writeJSON(w, http.StatusOK, &out)
}

func (srv *Server) deleteService(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteFunction(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name)
}

func (srv *Server) patchService(w http.ResponseWriter, r *http.Request, name string) {
	var body runapi.GoogleCloudRunV2Service
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.UpdateFunctionOptions{}
	if body.Template != nil && len(body.Template.Containers) > 0 {
		c := body.Template.Containers[0]
		opt.Image = c.Image
		if len(c.Env) > 0 {
			opt.Environment = map[string]string{}
			for _, e := range c.Env {
				opt.Environment[e.Name] = e.Value
			}
		}
	}
	if err := srv.s.UpdateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	writeOperation(w, name)
}

// ----------------------------------------------------------------------

func parseMemoryString(s string) int64 {
	if strings.HasSuffix(s, "Mi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dMi", &n)
		return n * 1024 * 1024
	}
	if strings.HasSuffix(s, "Gi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dGi", &n)
		return n * 1024 * 1024 * 1024
	}
	return 0
}

func parseCPUString(s string) int {
	if strings.HasSuffix(s, "m") {
		var n int
		_, _ = fmt.Sscanf(s, "%dm", &n)
		return n
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err == nil {
		return int(f * 1000)
	}
	return 0
}

func projectFromPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/v2/projects/"), "/")
	if len(parts) > 0 {
		return parts[0]
	}
	return ""
}

func locationFromPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/v2/projects/"), "/")
	if len(parts) >= 3 && parts[1] == "locations" {
		return parts[2]
	}
	return "us-central1"
}

func gcpStateFromDomain(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "CONDITION_SUCCEEDED"
	case domain.StatusCreating, domain.StatusUpdating:
		return "CONDITION_PENDING"
	default:
		return "CONDITION_FAILED"
	}
}

func functionToGCP(fn domain.Function, project, location string) *runapi.GoogleCloudRunV2Service {
	out := &runapi.GoogleCloudRunV2Service{
		Name: fmt.Sprintf("projects/%s/locations/%s/services/%s", project, location, fn.Name),
		Conditions: []*runapi.GoogleCloudRunV2Condition{{
			Type:  "Ready",
			State: gcpStateFromDomain(fn.Status),
		}},
		Template: &runapi.GoogleCloudRunV2RevisionTemplate{
			Containers: []*runapi.GoogleCloudRunV2Container{{
				Image: fn.Image,
			}},
		},
	}
	if !fn.CreatedAt.IsZero() {
		out.CreateTime = fn.CreatedAt.UTC().Format(time.RFC3339)
	}
	if fn.Status == domain.StatusAvailable && fn.Endpoint.URL != "" {
		out.Uri = fn.Endpoint.URL
	}
	return out
}

func writeOperation(w http.ResponseWriter, target string) {
	writeJSON(w, http.StatusOK, &runapi.GoogleLongrunningOperation{
		Name: fmt.Sprintf("operations/op-%d", time.Now().UnixNano()),
		Done: false,
	})
	_ = target
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var _ = errors.As

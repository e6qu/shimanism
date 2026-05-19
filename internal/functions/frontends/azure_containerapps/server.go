// Package azure_containerapps is the Azure Container Apps REST
// frontend. ARM URL shape:
// /subscriptions/{s}/resourceGroups/{rg}/providers/Microsoft.App/containerApps/{name}
package azure_containerapps

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type Server struct {
	s domain.Functions
}

func New(s domain.Functions) *Server { return &Server{s: s} }

var (
	reApp     = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.App/containerApps/([^/]+)/?$`)
	reAppList = regexp.MustCompile(`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.App/containerApps/?$`)
)

type containerAppBody struct {
	Location   string             `json:"location,omitempty"`
	Properties *containerAppProps `json:"properties,omitempty"`
}

type containerAppProps struct {
	ManagedEnvironmentID string            `json:"managedEnvironmentId,omitempty"`
	ProvisioningState    string            `json:"provisioningState,omitempty"`
	Configuration        *appConfiguration `json:"configuration,omitempty"`
	Template             *appTemplate      `json:"template,omitempty"`
}

type appConfiguration struct {
	Ingress *appIngress `json:"ingress,omitempty"`
}

type appIngress struct {
	External   bool   `json:"external,omitempty"`
	Fqdn       string `json:"fqdn,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
}

type appTemplate struct {
	Containers []*appContainer `json:"containers,omitempty"`
}

type appContainer struct {
	Name      string        `json:"name,omitempty"`
	Image     string        `json:"image,omitempty"`
	Env       []*appEnv     `json:"env,omitempty"`
	Resources *appResources `json:"resources,omitempty"`
}

type appEnv struct {
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type appResources struct {
	CPU    float64 `json:"cpu,omitempty"`
	Memory string  `json:"memory,omitempty"`
}

type appResource struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Type       string             `json:"type"`
	Location   string             `json:"location"`
	Properties *containerAppProps `json:"properties"`
}

type listResponse struct {
	Value []*appResource `json:"value"`
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reApp.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.createApp(w, r, m[1])
		case http.MethodGet:
			srv.getApp(w, r, m[1])
		case http.MethodDelete:
			srv.deleteApp(w, r, m[1])
		case http.MethodPatch:
			srv.patchApp(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", method+" not allowed on containerApp")
		}
		return
	}
	if reAppList.MatchString(path) && method == http.MethodGet {
		srv.listApps(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "ResourceNotFound",
		"no Container Apps route matches "+method+" "+path)
}

func (srv *Server) createApp(w http.ResponseWriter, r *http.Request, name string) {
	var body containerAppBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateFunctionOptions{}
	if body.Properties != nil && body.Properties.Template != nil && len(body.Properties.Template.Containers) > 0 {
		c := body.Properties.Template.Containers[0]
		opt.Image = c.Image
		if len(c.Env) > 0 {
			opt.Environment = map[string]string{}
			for _, e := range c.Env {
				opt.Environment[e.Name] = e.Value
			}
		}
		if c.Resources != nil {
			if c.Resources.CPU > 0 {
				opt.CPUMilliCores = int(c.Resources.CPU * 1000)
			}
			if c.Resources.Memory != "" {
				opt.MemoryBytes = parseMemoryString(c.Resources.Memory)
			}
		}
	}
	if _, err := srv.s.CreateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusCreated, functionToARM(fn))
}

func (srv *Server) getApp(w http.ResponseWriter, r *http.Request, name string) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, functionToARM(fn))
}

func (srv *Server) deleteApp(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteFunction(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) patchApp(w http.ResponseWriter, r *http.Request, name string) {
	var body containerAppBody
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.UpdateFunctionOptions{}
	if body.Properties != nil && body.Properties.Template != nil && len(body.Properties.Template.Containers) > 0 {
		c := body.Properties.Template.Containers[0]
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
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusOK, functionToARM(fn))
}

func (srv *Server) listApps(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListFunctions(r.Context(), domain.ListFunctionsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	out := listResponse{}
	for _, fn := range res.Functions {
		out.Value = append(out.Value, functionToARM(fn))
	}
	writeJSON(w, http.StatusOK, &out)
}

func parseMemoryString(s string) int64 {
	if strings.HasSuffix(s, "Gi") {
		var n float64
		_, _ = fmt.Sscanf(s, "%fGi", &n)
		return int64(n * 1024 * 1024 * 1024)
	}
	if strings.HasSuffix(s, "Mi") {
		var n int64
		_, _ = fmt.Sscanf(s, "%dMi", &n)
		return n * 1024 * 1024
	}
	return 0
}

func statusToARM(s domain.Status) string {
	switch s {
	case domain.StatusAvailable:
		return "Succeeded"
	case domain.StatusCreating, domain.StatusUpdating:
		return "InProgress"
	case domain.StatusDeleting:
		return "Deleting"
	default:
		return "Failed"
	}
}

func functionToARM(fn domain.Function) *appResource {
	res := &appResource{
		Name:     fn.Name,
		Type:     "Microsoft.App/containerApps",
		Location: "eastus",
		Properties: &containerAppProps{
			ProvisioningState: statusToARM(fn.Status),
			Template: &appTemplate{
				Containers: []*appContainer{{
					Name:  fn.Name,
					Image: fn.Image,
				}},
			},
		},
	}
	if fn.Status == domain.StatusAvailable && fn.Endpoint.URL != "" {
		host := strings.TrimPrefix(fn.Endpoint.URL, "https://")
		host = strings.TrimPrefix(host, "http://")
		res.Properties.Configuration = &appConfiguration{
			Ingress: &appIngress{External: true, Fqdn: host},
		}
	}
	return res
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

// Package azure_containerapps is the Azure Container Apps ARM frontend.
// Wire types + routing come from the spec-driven generated stubs in
// services/functions/gen/azure (cmd/azure-codegen). The adapter on
// Server implements gen.ServerInterface; gen.HandlerWithOptions
// dispatches each request.
//
// ARM URL shape (path-routed by the gen mux):
//
//	/subscriptions/{subscriptionId}
//	  /resourceGroups/{resourceGroupName}
//	    /providers/Microsoft.App/containerApps/{containerAppName}
package azure_containerapps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/functions/domain"
	gen "github.com/e6qu/shimanism/services/functions/gen/azure"
)

// Server is the Azure-Container-Apps-shaped HTTP frontend.
type Server struct {
	s   domain.Functions
	mux http.Handler
}

// New returns a frontend bound to the given backend.
func New(s domain.Functions) *Server {
	srv := &Server{s: s}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP delegates to the generated routing layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the ARM "operation not supported" envelope
// for spec ops outside the cross-cloud functions intersection.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "OperationNotSupported",
		op+" is not in the cross-cloud functions intersection")
}

// =====================================================================
// In-intersection handlers
// =====================================================================

func (srv *Server) ContainerAppsCreateOrUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ContainerAppsCreateOrUpdateParams) {
	var body gen.ContainerApp
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateFunctionOptions{}
	extractFromCreateBody(&body, &opt)
	if _, err := srv.s.CreateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusCreated, functionToARM(fn))
}

func (srv *Server) ContainerAppsGet(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ContainerAppsGetParams) {
	fn, err := srv.s.DescribeFunction(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, functionToARM(fn))
}

func (srv *Server) ContainerAppsDelete(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ContainerAppsDeleteParams) {
	if err := srv.s.DeleteFunction(r.Context(), name); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) ContainerAppsUpdate(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, name string, _ gen.ContainerAppsUpdateParams) {
	var body gen.ContainerApp
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.UpdateFunctionOptions{}
	extractFromUpdateBody(&body, &opt)
	if err := srv.s.UpdateFunction(r.Context(), name, opt); err != nil {
		mapDomainError(w, err)
		return
	}
	fn, _ := srv.s.DescribeFunction(r.Context(), name)
	writeJSON(w, http.StatusOK, functionToARM(fn))
}

func (srv *Server) ContainerAppsListByResourceGroup(w http.ResponseWriter, r *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ gen.ContainerAppsListByResourceGroupParams) {
	res, err := srv.s.ListFunctions(r.Context(), domain.ListFunctionsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	values := make([]gen.ContainerApp, 0, len(res.Functions))
	for _, fn := range res.Functions {
		values = append(values, *functionToARM(fn))
	}
	writeJSON(w, http.StatusOK, &gen.ContainerAppCollection{Value: values})
}

// =====================================================================
// Out-of-intersection stubs — honest 501s
// =====================================================================

func (srv *Server) ContainerAppsListBySubscription(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ContainerAppsListBySubscriptionParams) {
	notImplemented(w, "ContainerAppsListBySubscription")
}

func (srv *Server) ContainerAppsGetAuthToken(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ContainerAppsGetAuthTokenParams) {
	notImplemented(w, "ContainerAppsGetAuthToken")
}

func (srv *Server) ContainerAppsListCustomHostNameAnalysis(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ContainerAppsListCustomHostNameAnalysisParams) {
	notImplemented(w, "ContainerAppsListCustomHostNameAnalysis")
}

func (srv *Server) ContainerAppsListSecrets(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ContainerAppsListSecretsParams) {
	notImplemented(w, "ContainerAppsListSecrets")
}

func (srv *Server) ContainerAppsStart(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ContainerAppsStartParams) {
	notImplemented(w, "ContainerAppsStart")
}

func (srv *Server) ContainerAppsStop(w http.ResponseWriter, _ *http.Request, _ gen.SubscriptionIdParameter, _ gen.ResourceGroupNameParameter, _ string, _ gen.ContainerAppsStopParams) {
	notImplemented(w, "ContainerAppsStop")
}

// =====================================================================
// Helpers — domain ↔ gen.ContainerApp
// =====================================================================

// extractFromCreateBody pulls the cross-cloud-intersection fields
// (image, env, CPU, memory) out of a gen.ContainerApp create body
// into domain.CreateFunctionOptions. Lots of pointer derefs because
// oapi-codegen emits OpenAPI optional fields as `*T`.
func extractFromCreateBody(body *gen.ContainerApp, opt *domain.CreateFunctionOptions) {
	if body.Properties == nil || body.Properties.Template == nil || body.Properties.Template.Containers == nil {
		return
	}
	containers := *body.Properties.Template.Containers
	if len(containers) == 0 {
		return
	}
	c := containers[0]
	if c.Image != nil {
		opt.Image = *c.Image
	}
	if c.Env != nil {
		opt.Environment = map[string]string{}
		for _, e := range *c.Env {
			if e.Name != nil && e.Value != nil {
				opt.Environment[*e.Name] = *e.Value
			}
		}
		if len(opt.Environment) == 0 {
			opt.Environment = nil
		}
	}
	if c.Resources != nil {
		if c.Resources.Cpu != nil && *c.Resources.Cpu > 0 {
			opt.CPUMilliCores = int(*c.Resources.Cpu * 1000)
		}
		if c.Resources.Memory != nil && *c.Resources.Memory != "" {
			opt.MemoryBytes = parseMemoryString(*c.Resources.Memory)
		}
	}
}

func extractFromUpdateBody(body *gen.ContainerApp, opt *domain.UpdateFunctionOptions) {
	if body.Properties == nil || body.Properties.Template == nil || body.Properties.Template.Containers == nil {
		return
	}
	containers := *body.Properties.Template.Containers
	if len(containers) == 0 {
		return
	}
	c := containers[0]
	if c.Image != nil {
		opt.Image = *c.Image
	}
	if c.Env != nil {
		opt.Environment = map[string]string{}
		for _, e := range *c.Env {
			if e.Name != nil && e.Value != nil {
				opt.Environment[*e.Name] = *e.Value
			}
		}
		if len(opt.Environment) == 0 {
			opt.Environment = nil
		}
	}
}

// parseMemoryString parses Kubernetes-style memory quantities ("1Gi",
// "512Mi") into bytes.
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

// statusToARM maps the domain status enum to Azure's
// ProvisioningState string.
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

// functionToARM maps a domain.Function to the gen.ContainerApp shape.
// gen.ContainerApp.Properties is an anonymous inline struct (the
// OpenAPI v3 emitter style after flattenARMAllOf in Phase 12.A.24).
// We construct it via JSON unmarshal from a concrete intermediate
// shape — far cleaner than restating the anonymous struct's full
// field list at every call site, and the field set tracks the gen
// file automatically (anonymous-struct extra fields stay zero-
// valued / omitted).
func functionToARM(fn domain.Function) *gen.ContainerApp {
	location := "eastus"
	typ := "Microsoft.App/containerApps"
	resourceID := "/subscriptions/shim/resourceGroups/shim/providers/Microsoft.App/containerApps/" + fn.Name

	res := &gen.ContainerApp{
		Id:       &resourceID,
		Name:     &fn.Name,
		Type:     &typ,
		Location: location,
	}

	// Encode the properties subset we populate, then decode it
	// into the anonymous struct via the gen.ContainerApp envelope.
	// Fields not in our intermediate stay zero-valued in the gen.
	propsBlob := map[string]any{
		"provisioningState": statusToARM(fn.Status),
		"template": map[string]any{
			"containers": []map[string]any{
				{"name": fn.Name, "image": fn.Image},
			},
		},
	}
	if fn.Status == domain.StatusAvailable && fn.Endpoint.URL != "" {
		host := strings.TrimPrefix(fn.Endpoint.URL, "https://")
		host = strings.TrimPrefix(host, "http://")
		propsBlob["configuration"] = map[string]any{
			"ingress": map[string]any{
				"external":   true,
				"fqdn":       host,
				"targetPort": 0,
			},
		}
	}
	envelope := map[string]any{
		"location":   location,
		"properties": propsBlob,
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return res
	}
	var hydrated gen.ContainerApp
	if err := json.Unmarshal(raw, &hydrated); err != nil {
		return res
	}
	res.Properties = hydrated.Properties
	return res
}

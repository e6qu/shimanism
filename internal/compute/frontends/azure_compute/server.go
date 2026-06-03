// Package azure_compute is the Azure Compute ARM frontend for
// shimanism's compute service (Phase 16.C instance lifecycle). It speaks
// the ARM REST/JSON protocol for:
//
//   - Microsoft.Compute/virtualMachines → domain.Instance lifecycle
//   - Microsoft.Compute/locations/{loc}/vmSizes → domain.InstanceTypeInfo
//
// Wire types come from github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6 —
// the same types the armcompute SDK uses under the hood.
//
// Auth: Azure Bearer verifier (same as azure_network).
package azure_compute

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/compute/domain"
)

// ComputeBackend is satisfied by any type implementing domain.Instances.
// (domain.Networking is not needed for the Azure Compute frontend since
// network primitives live in the azure_network frontend.)
type ComputeBackend interface {
	domain.Instances
}

// Config carries optional Server configuration.
type Config struct {
	// Passthrough forwards ARM paths not handled by this frontend
	// (resource groups, subscriptions, Microsoft.Network, Entra ID
	// token requests when MetadataLoginURL is unset, …) to the
	// upstream handler. Typically a reverse proxy to sockerless.
	Passthrough http.Handler

	// MetadataLoginURL is the base URL for endpoints the shim does
	// not intercept (Entra ID loginEndpoint, graph, batch, …).
	// When set, the frontend serves GET /metadata/endpoints returning
	// a cloud-environment JSON document whose resourceManager points
	// at the shim itself and whose loginEndpoint points here.
	MetadataLoginURL string

	// BearerOptions configures the Azure Bearer-token verifier. Through-
	// shim tests set JWKS + Issuer so the shim accepts tokens issued
	// by sockerless's Entra stub. Zero value falls back to the default
	// test-key HMAC verifier.
	BearerOptions azurebearer.Options
}

// Server is an Azure Compute ARM HTTP frontend.
type Server struct {
	inst             domain.Instances
	upstream         http.Handler
	metadataLoginURL string
}

// New returns a frontend bound to the given backend.
func New(inst domain.Instances) *Server { return &Server{inst: inst} }

// NewWithConfig is the general constructor; honors every Config field.
func NewWithConfig(inst domain.Instances, c Config) *Server {
	return &Server{
		inst:             inst,
		upstream:         c.Passthrough,
		metadataLoginURL: c.MetadataLoginURL,
	}
}

// Handler wraps Server with the Azure bearer verifier middleware.
func Handler(inst domain.Instances) http.Handler {
	return HandlerWithConfig(inst, Config{})
}

// HandlerWithConfig is the verifier-wrapped form of NewWithConfig.
// The metadata endpoint is served without bearer auth (public discovery).
func HandlerWithConfig(inst domain.Instances, c Config) http.Handler {
	server := NewWithConfig(inst, c)
	if c.MetadataLoginURL == "" {
		return wrapWithBearerVM(server, c.BearerOptions)
	}
	bearerWrapped := wrapWithBearerVM(server, c.BearerOptions)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/metadata/endpoints" {
			server.ServeHTTP(w, r)
			return
		}
		bearerWrapped.ServeHTTP(w, r)
	})
}

func wrapWithBearerVM(h http.Handler, opts azurebearer.Options) http.Handler {
	if opts.JWKS == nil && opts.JWKSURL == "" && len(opts.TestKey) == 0 {
		opts.TestKey = []byte("test-key-do-not-use-in-prod")
	}
	if opts.Audience == "" {
		opts.Audience = "https://management.azure.com/"
	}
	verifier := azurebearer.New(opts)
	return azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))(h)
}

// ServeHTTP routes ARM paths for Microsoft.Compute resources.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Public metadata discovery endpoint — answer without bearer auth.
	if r.Method == http.MethodGet && path == "/metadata/endpoints" && srv.metadataLoginURL != "" {
		srv.serveMetadata(w, r)
		return
	}

	// vmSizes: /subscriptions/.../providers/Microsoft.Compute/locations/{loc}/vmSizes
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "/providers/microsoft.compute/locations/") &&
		strings.HasSuffix(lowerPath, "/vmsizes") {
		srv.listVMSizes(w, r)
		return
	}

	idx := strings.Index(lowerPath, "/providers/microsoft.compute/")
	if idx < 0 {
		srv.passthroughOr404(w, r)
		return
	}
	tail := path[idx+len("/providers/microsoft.compute/"):]
	parts := strings.SplitN(tail, "/", 4)
	resourceType := strings.ToLower(parts[0])
	resourceName := ""
	if len(parts) >= 2 {
		resourceName = parts[1]
	}

	switch resourceType {
	case "virtualmachines":
		// Sub-resource action: /virtualMachines/{name}/start|deallocate|restart
		if len(parts) == 3 {
			srv.routeVMAction(w, r, resourceName, strings.ToLower(parts[2]))
			return
		}
		srv.routeVMs(w, r, resourceName)
	default:
		srv.passthroughOr404(w, r)
	}
}

// passthroughOr404 forwards to the configured upstream when present;
// otherwise emits an Azure-shaped 404 envelope.
func (srv *Server) passthroughOr404(w http.ResponseWriter, r *http.Request) {
	if srv.upstream != nil {
		srv.upstream.ServeHTTP(w, r)
		return
	}
	writeAzureError(w, http.StatusNotFound, "NotFound", "path not matched: "+r.URL.Path)
}

// serveMetadata returns the Azure cloud-environment JSON document the
// azurerm provider fetches via metadata_host. Mirrors the shape from
// azure_dns.serveMetadata (BUG-46 pattern).
func (srv *Server) serveMetadata(w http.ResponseWriter, r *http.Request) {
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		scheme = strings.ToLower(fp)
	}
	shimBase := fmt.Sprintf("%s://%s", scheme, r.Host)
	env := map[string]any{
		"name": "AzureCloud",
		"authentication": map[string]any{
			"loginEndpoint": srv.metadataLoginURL,
			"audiences": []string{
				srv.metadataLoginURL + "/",
				"https://management.core.windows.net/",
				"https://management.azure.com/",
			},
			"tenant":           "common",
			"identityProvider": "AAD",
		},
		"resourceManager":          shimBase,
		"microsoftGraphResourceId": srv.metadataLoginURL + "/",
		"graph":                    srv.metadataLoginURL,
		"portal":                   srv.metadataLoginURL,
		"gallery":                  srv.metadataLoginURL,
		"batch":                    srv.metadataLoginURL,
		"suffixes": map[string]any{
			"keyVaultDns":       "vault.localhost",
			"storage":           "storage.localhost",
			"acrLoginServer":    "localhost",
			"sqlServerHostname": "localhost",
		},
	}
	apiVersion := r.URL.Query().Get("api-version")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if apiVersion == "2022-09-01" {
		_ = json.NewEncoder(w).Encode(env)
	} else {
		_ = json.NewEncoder(w).Encode([]any{env})
	}
}

// ─── VirtualMachines ─────────────────────────────────────────────────

func (srv *Server) routeVMs(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listVMs(w, r)
		default:
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateVM(w, r, name)
	case http.MethodGet:
		srv.getVM(w, r, name)
	case http.MethodDelete:
		srv.deleteVM(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) routeVMAction(w http.ResponseWriter, r *http.Request, name, action string) {
	if r.Method != http.MethodPost {
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		return
	}
	inst := srv.findVMByName(r, name)
	if inst == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualMachine '"+name+"' not found")
		return
	}
	switch action {
	case "start":
		if _, err := srv.inst.StartInstances(r.Context(), []string{inst.ID}); err != nil {
			writeComputeErr(w, err)
			return
		}
	case "deallocate":
		if _, err := srv.inst.StopInstances(r.Context(), []string{inst.ID}); err != nil {
			writeComputeErr(w, err)
			return
		}
	case "restart":
		if err := srv.inst.RebootInstances(r.Context(), []string{inst.ID}); err != nil {
			writeComputeErr(w, err)
			return
		}
	default:
		writeAzureError(w, http.StatusNotFound, "NotFound", "VM action not supported: "+action)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) createOrUpdateVM(w http.ResponseWriter, r *http.Request, name string) {
	var req armcompute.VirtualMachine
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	// Idempotent: if already exists return 200.
	if existing := srv.findVMByName(r, name); existing != nil {
		writeJSON(w, http.StatusOK, domainVMToAzure(*existing, req.Location))
		return
	}
	opts := domain.RunInstancesOptions{
		MinCount: 1,
		MaxCount: 1,
		Tags:     map[string]string{"Name": name},
	}
	if req.Properties != nil && req.Properties.HardwareProfile != nil &&
		req.Properties.HardwareProfile.VMSize != nil {
		opts.InstanceType = string(*req.Properties.HardwareProfile.VMSize)
	}
	if req.Properties != nil && req.Properties.StorageProfile != nil &&
		req.Properties.StorageProfile.ImageReference != nil {
		ref := req.Properties.StorageProfile.ImageReference
		if ref.ID != nil {
			opts.ImageID = *ref.ID
		} else if ref.Offer != nil {
			opts.ImageID = fmt.Sprintf("%s/%s/%s",
				strDeref(ref.Publisher), strDeref(ref.Offer), strDeref(ref.SKU))
		}
	}
	if opts.ImageID == "" {
		opts.ImageID = "unknown"
	}
	instances, err := srv.inst.RunInstances(r.Context(), opts)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainVMToAzure(instances[0], req.Location))
}

func (srv *Server) getVM(w http.ResponseWriter, r *http.Request, name string) {
	inst := srv.findVMByName(r, name)
	if inst == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualMachine '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainVMToAzure(*inst, nil))
}

func (srv *Server) listVMs(w http.ResponseWriter, r *http.Request) {
	res, err := srv.inst.DescribeInstances(r.Context(), domain.DescribeInstancesOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	type vmListResult struct {
		Value []*armcompute.VirtualMachine `json:"value"`
	}
	result := vmListResult{Value: []*armcompute.VirtualMachine{}}
	for _, inst := range res.Instances {
		inst := inst
		result.Value = append(result.Value, domainVMToAzure(inst, nil))
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deleteVM(w http.ResponseWriter, r *http.Request, name string) {
	inst := srv.findVMByName(r, name)
	if inst == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualMachine '"+name+"' not found")
		return
	}
	if _, err := srv.inst.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── VM Sizes ─────────────────────────────────────────────────────────

func (srv *Server) listVMSizes(w http.ResponseWriter, r *http.Request) {
	res, err := srv.inst.DescribeInstanceTypes(r.Context(), domain.DescribeInstanceTypesOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	type vmSizeListResult struct {
		Value []*armcompute.VirtualMachineSize `json:"value"`
	}
	result := vmSizeListResult{Value: []*armcompute.VirtualMachineSize{}}
	for _, t := range res.InstanceTypes {
		t := t
		vcpus := int32(t.VCPUs)
		mem := int32(t.MemoryMiB)
		name := t.InstanceType
		result.Value = append(result.Value, &armcompute.VirtualMachineSize{
			Name:          &name,
			NumberOfCores: &vcpus,
			MemoryInMB:    &mem,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

// ─── Lookup helpers ───────────────────────────────────────────────────

func (srv *Server) findVMByName(r *http.Request, name string) *domain.Instance {
	res, err := srv.inst.DescribeInstances(r.Context(), domain.DescribeInstancesOptions{})
	if err != nil {
		return nil
	}
	for _, inst := range res.Instances {
		if inst.Name == name || inst.ID == name {
			inst := inst
			return &inst
		}
	}
	return nil
}

// ─── Converters ───────────────────────────────────────────────────────

func domainVMToAzure(inst domain.Instance, location *string) *armcompute.VirtualMachine {
	id := fmt.Sprintf("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Compute/virtualMachines/%s", inst.Name)
	provState := "Succeeded"
	vm := &armcompute.VirtualMachine{
		ID:       &id,
		Name:     &inst.Name,
		Location: location,
		Type:     strPtr("Microsoft.Compute/virtualMachines"),
		Properties: &armcompute.VirtualMachineProperties{
			ProvisioningState: &provState,
		},
	}
	return vm
}

func strPtr(s string) *string { return &s }

func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ─── Error helpers ────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type azureErrorBody struct {
	Error azureErrorDetail `json:"error"`
}

type azureErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAzureError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, azureErrorBody{Error: azureErrorDetail{Code: code, Message: message}})
}

func writeComputeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		writeAzureError(w, http.StatusConflict, "ResourceAlreadyExists", err.Error())
	case errors.Is(err, domain.ErrNotSupported):
		writeAzureError(w, http.StatusBadRequest, "OperationNotSupported", err.Error())
	default:
		writeAzureError(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

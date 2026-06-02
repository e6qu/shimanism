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

// Server is an Azure Compute ARM HTTP frontend.
type Server struct {
	inst domain.Instances
}

// New returns a frontend bound to the given backend.
func New(inst domain.Instances) *Server { return &Server{inst: inst} }

// Handler wraps Server with the Azure bearer verifier middleware.
func Handler(inst domain.Instances) http.Handler {
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))
	return mw(New(inst))
}

// ServeHTTP routes ARM paths for Microsoft.Compute resources.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// vmSizes: /subscriptions/.../providers/Microsoft.Compute/locations/{loc}/vmSizes
	lowerPath := strings.ToLower(path)
	if strings.Contains(lowerPath, "/providers/microsoft.compute/locations/") &&
		strings.HasSuffix(lowerPath, "/vmsizes") {
		srv.listVMSizes(w, r)
		return
	}

	idx := strings.Index(lowerPath, "/providers/microsoft.compute/")
	if idx < 0 {
		writeAzureError(w, http.StatusNotFound, "NotFound", "path not matched: "+path)
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
		writeAzureError(w, http.StatusNotFound, "NotFound", "resource type not supported: "+resourceType)
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

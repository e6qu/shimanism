// Package azure_lb is the Azure Network load balancer ARM frontend for
// shimanism's load balancer service (Phase 16.D).
// It speaks the ARM REST/JSON protocol for:
//
//   - Microsoft.Network/loadBalancers → domain.LoadBalancer + domain.Listener
//   - Microsoft.Network/loadBalancers/{name}/backendAddressPools
//     → domain.TargetGroup + target registration
//
// Auth: Azure Bearer verifier.
package azure_lb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

// Server is an Azure-LB-shaped HTTP frontend.
type Server struct {
	lb domain.LoadBalancers
}

// New returns a frontend bound to the given backend.
func New(lb domain.LoadBalancers) *Server { return &Server{lb: lb} }

// Handler wraps Server with the Azure bearer verifier middleware.
func Handler(lb domain.LoadBalancers) http.Handler {
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))
	return mw(New(lb))
}

// ServeHTTP routes ARM paths for Microsoft.Network/loadBalancers.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	idx := strings.Index(strings.ToLower(path), "/providers/microsoft.network/")
	if idx < 0 {
		writeAzureError(w, http.StatusNotFound, "NotFound", "path not matched: "+path)
		return
	}
	tail := path[idx+len("/providers/microsoft.network/"):]
	parts := strings.SplitN(tail, "/", 4)
	resourceType := strings.ToLower(parts[0])
	resourceName := ""
	if len(parts) >= 2 {
		resourceName = parts[1]
	}

	switch resourceType {
	case "loadbalancers":
		// Sub-resource: /loadBalancers/{name}/backendAddressPools/{pool}
		if len(parts) == 4 && strings.ToLower(parts[2]) == "backendaddresspools" {
			srv.routeBackendPools(w, r, resourceName, parts[3])
			return
		}
		srv.routeLBs(w, r, resourceName)
	default:
		writeAzureError(w, http.StatusNotFound, "NotFound", "resource type not supported: "+resourceType)
	}
}

// ─── LoadBalancers ────────────────────────────────────────────────────

func (srv *Server) routeLBs(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listLBs(w, r)
		default:
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateLB(w, r, name)
	case http.MethodGet:
		srv.getLB(w, r, name)
	case http.MethodDelete:
		srv.deleteLB(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateLB(w http.ResponseWriter, r *http.Request, name string) {
	var req armnetwork.LoadBalancer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	// Check if it exists already.
	existing := srv.findLBByName(r, name)
	if existing != nil {
		writeJSON(w, http.StatusOK, domainLBToAzure(*existing, req.Location))
		return
	}
	lb, err := srv.lb.CreateLoadBalancer(r.Context(), name, domain.CreateLoadBalancerOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	// Create listener from frontendIPConfigurations + loadBalancingRules.
	if req.Properties != nil && len(req.Properties.LoadBalancingRules) > 0 {
		rule := req.Properties.LoadBalancingRules[0]
		if rule.Properties != nil {
			port := 80
			if rule.Properties.FrontendPort != nil {
				port = int(*rule.Properties.FrontendPort)
			}
			// Find target group from backend pool name.
			tgID := ""
			if rule.Properties.BackendAddressPool != nil && rule.Properties.BackendAddressPool.ID != nil {
				poolName := azureLastPathSeg(*rule.Properties.BackendAddressPool.ID)
				tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
				for _, tg := range tgs.TargetGroups {
					if tg.Name == poolName {
						tgID = tg.ID
						break
					}
				}
			}
			_, _ = srv.lb.CreateListener(r.Context(), domain.CreateListenerOptions{
				LoadBalancerID: lb.ID,
				Protocol:       domain.ProtocolTCP,
				Port:           port,
				TargetGroupID:  tgID,
			})
		}
	}
	writeJSON(w, http.StatusCreated, domainLBToAzure(lb, req.Location))
}

func (srv *Server) getLB(w http.ResponseWriter, r *http.Request, name string) {
	lb := srv.findLBByName(r, name)
	if lb == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "LoadBalancer '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainLBToAzure(*lb, nil))
}

func (srv *Server) listLBs(w http.ResponseWriter, r *http.Request) {
	res, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	type lbListResult struct {
		Value []*armnetwork.LoadBalancer `json:"value"`
	}
	result := lbListResult{Value: []*armnetwork.LoadBalancer{}}
	for _, lb := range res.LoadBalancers {
		lb := lb
		result.Value = append(result.Value, domainLBToAzure(lb, nil))
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deleteLB(w http.ResponseWriter, r *http.Request, name string) {
	lb := srv.findLBByName(r, name)
	if lb == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "LoadBalancer '"+name+"' not found")
		return
	}
	if err := srv.lb.DeleteLoadBalancer(r.Context(), lb.ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Backend Address Pools (TargetGroup) ─────────────────────────────

func (srv *Server) routeBackendPools(w http.ResponseWriter, r *http.Request, lbName, poolName string) {
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdatePool(w, r, lbName, poolName)
	case http.MethodGet:
		srv.getPool(w, r, poolName)
	case http.MethodDelete:
		srv.deletePool(w, r, poolName)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdatePool(w http.ResponseWriter, r *http.Request, lbName, poolName string) {
	existing := srv.findTGByName(r, poolName)
	if existing != nil {
		writeJSON(w, http.StatusOK, domainTGToAzurePool(*existing))
		return
	}
	tg, err := srv.lb.CreateTargetGroup(r.Context(), poolName, domain.CreateTargetGroupOptions{
		Protocol: domain.ProtocolTCP,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainTGToAzurePool(tg))
}

func (srv *Server) getPool(w http.ResponseWriter, r *http.Request, name string) {
	tg := srv.findTGByName(r, name)
	if tg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "BackendAddressPool '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainTGToAzurePool(*tg))
}

func (srv *Server) deletePool(w http.ResponseWriter, r *http.Request, name string) {
	tg := srv.findTGByName(r, name)
	if tg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "BackendAddressPool '"+name+"' not found")
		return
	}
	if err := srv.lb.DeleteTargetGroup(r.Context(), tg.ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Lookup helpers ───────────────────────────────────────────────────

func (srv *Server) findLBByName(r *http.Request, name string) *domain.LoadBalancer {
	res, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{Names: []string{name}})
	if err != nil || len(res.LoadBalancers) == 0 {
		return nil
	}
	lb := res.LoadBalancers[0]
	return &lb
}

func (srv *Server) findTGByName(r *http.Request, name string) *domain.TargetGroup {
	res, err := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	if err != nil {
		return nil
	}
	for _, tg := range res.TargetGroups {
		if tg.Name == name {
			tg := tg
			return &tg
		}
	}
	return nil
}

// ─── Converters ───────────────────────────────────────────────────────

func domainLBToAzure(lb domain.LoadBalancer, location *string) *armnetwork.LoadBalancer {
	id := fmt.Sprintf("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/loadBalancers/%s", lb.Name)
	return &armnetwork.LoadBalancer{
		ID:       &id,
		Name:     &lb.Name,
		Location: location,
		Type:     strPtr("Microsoft.Network/loadBalancers"),
		Properties: &armnetwork.LoadBalancerPropertiesFormat{
			ProvisioningState: provStatePtr(armnetwork.ProvisioningStateSucceeded),
		},
	}
}

func domainTGToAzurePool(tg domain.TargetGroup) map[string]any {
	id := fmt.Sprintf("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/loadBalancers/shim/backendAddressPools/%s", tg.Name)
	return map[string]any{
		"id":   id,
		"name": tg.Name,
		"type": "Microsoft.Network/loadBalancers/backendAddressPools",
		"properties": map[string]any{
			"provisioningState": "Succeeded",
		},
	}
}

func strPtr(s string) *string { return &s }

func provStatePtr(s armnetwork.ProvisioningState) *armnetwork.ProvisioningState { return &s }

func azureLastPathSeg(url string) string {
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return url
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

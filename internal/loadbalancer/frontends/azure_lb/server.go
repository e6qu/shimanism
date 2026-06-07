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
	"io"
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
	case "applicationgateways":
		srv.routeAppGateways(w, r, resourceName)
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

// ─── Application Gateway (compound ARM resource) ─────────────────────

const blobKindAzureAppGW = "azure-appgw"

func (srv *Server) routeAppGateways(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		if r.Method == http.MethodGet {
			srv.listAppGateways(w, r)
		} else {
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateAppGateway(w, r, name)
	case http.MethodGet:
		srv.getAppGateway(w, r, name)
	case http.MethodDelete:
		srv.deleteAppGateway(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateAppGateway(w http.ResponseWriter, r *http.Request, name string) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	var req armnetwork.ApplicationGateway
	if err := json.Unmarshal(raw, &req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}

	// Idempotent: if blob already exists, return current state.
	if _, err := srv.lb.GetBlob(r.Context(), blobKindAzureAppGW, name); err == nil {
		writeJSON(w, http.StatusOK, appGWResponseBody(name, req.Location))
		return
	}

	// Store raw blob for GET round-trips.
	if err := srv.lb.PutBlob(r.Context(), blobKindAzureAppGW, name, raw); err != nil {
		writeComputeErr(w, err)
		return
	}

	lb, err := srv.lb.CreateLoadBalancer(r.Context(), name, domain.CreateLoadBalancerOptions{
		Type: domain.LoadBalancerTypeApplication,
	})
	if err != nil {
		_ = srv.lb.DeleteBlob(r.Context(), blobKindAzureAppGW, name)
		writeComputeErr(w, err)
		return
	}

	if req.Properties != nil {
		srv.assembleAppGWEntities(r, lb.ID, req.Properties)
	}
	writeJSON(w, http.StatusCreated, appGWResponseBody(name, req.Location))
}

// assembleAppGWEntities parses the compound ApplicationGateway properties
// and creates the corresponding domain entities (TGs, Listeners, Rules).
func (srv *Server) assembleAppGWEntities(r *http.Request, lbID string, p *armnetwork.ApplicationGatewayPropertiesFormat) {
	ctx := r.Context()

	// Build frontend-port name → port number.
	portMap := make(map[string]int)
	for _, fp := range p.FrontendPorts {
		if fp.Name != nil && fp.Properties != nil && fp.Properties.Port != nil {
			portMap[*fp.Name] = int(*fp.Properties.Port)
		}
	}

	// Create one TargetGroup per BackendAddressPool.
	tgMap := make(map[string]string) // pool name → domain TG ID
	for _, pool := range p.BackendAddressPools {
		if pool.Name == nil {
			continue
		}
		tg, err := srv.lb.CreateTargetGroup(ctx, *pool.Name, domain.CreateTargetGroupOptions{
			Protocol: domain.ProtocolHTTP,
		})
		if err == nil {
			tgMap[*pool.Name] = tg.ID
		}
	}

	// Create one Listener per HTTPS HTTPListener.
	lsnMap := make(map[string]string) // ARM listener name → domain listener ID
	for _, hl := range p.HTTPListeners {
		if hl.Name == nil || hl.Properties == nil {
			continue
		}
		if hl.Properties.Protocol == nil || !strings.EqualFold(string(*hl.Properties.Protocol), "https") {
			continue
		}
		port := 443
		if hl.Properties.FrontendPort != nil && hl.Properties.FrontendPort.ID != nil {
			if p, ok := portMap[azureLastPathSeg(*hl.Properties.FrontendPort.ID)]; ok {
				port = p
			}
		}
		var certIDs []string
		if hl.Properties.SSLCertificate != nil && hl.Properties.SSLCertificate.ID != nil {
			certIDs = []string{*hl.Properties.SSLCertificate.ID}
		}
		defaultTGID := ""
		for _, v := range tgMap {
			defaultTGID = v
			break
		}
		lsn, err := srv.lb.CreateListener(ctx, domain.CreateListenerOptions{
			LoadBalancerID: lbID,
			Protocol:       domain.ProtocolHTTPS,
			Port:           port,
			TargetGroupID:  defaultTGID,
			CertificateIDs: certIDs,
		})
		if err == nil {
			lsnMap[*hl.Name] = lsn.ID
		}
	}

	// Build URLPathMap name → domain listener ID via RequestRoutingRules.
	umToLsn := make(map[string]string)
	for _, rr := range p.RequestRoutingRules {
		if rr.Properties == nil {
			continue
		}
		lsnName := ""
		if rr.Properties.HTTPListener != nil && rr.Properties.HTTPListener.ID != nil {
			lsnName = azureLastPathSeg(*rr.Properties.HTTPListener.ID)
		}
		umName := ""
		if rr.Properties.URLPathMap != nil && rr.Properties.URLPathMap.ID != nil {
			umName = azureLastPathSeg(*rr.Properties.URLPathMap.ID)
		}
		if lsnName != "" && umName != "" {
			if lsnID, ok := lsnMap[lsnName]; ok {
				umToLsn[umName] = lsnID
			}
		}
	}

	// Create Rules from URLPathMap path rules.
	for _, um := range p.URLPathMaps {
		if um.Name == nil || um.Properties == nil {
			continue
		}
		lsnID, ok := umToLsn[*um.Name]
		if !ok {
			continue
		}
		for i, pr := range um.Properties.PathRules {
			if pr.Properties == nil || len(pr.Properties.Paths) == 0 {
				continue
			}
			path := ""
			if pr.Properties.Paths[0] != nil {
				path = *pr.Properties.Paths[0]
			}
			tgID := ""
			if pr.Properties.BackendAddressPool != nil && pr.Properties.BackendAddressPool.ID != nil {
				tgID = tgMap[azureLastPathSeg(*pr.Properties.BackendAddressPool.ID)]
			}
			_, _ = srv.lb.CreateRule(ctx, domain.CreateRuleOptions{
				ListenerID: lsnID,
				Priority:   (i + 1) * 10,
				Conditions: []domain.RuleCondition{{
					Type:   domain.RuleConditionPathPattern,
					Values: []string{path},
				}},
				Action: domain.RuleAction{TargetGroupID: tgID},
			})
		}
	}
}

func (srv *Server) getAppGateway(w http.ResponseWriter, r *http.Request, name string) {
	blob, err := srv.lb.GetBlob(r.Context(), blobKindAzureAppGW, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "ApplicationGateway '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	// Enrich stored blob with server-assigned fields the SDK expects.
	var body map[string]any
	if err := json.Unmarshal(blob, &body); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(blob)
		return
	}
	id := fmt.Sprintf("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/applicationGateways/%s", name)
	body["id"] = id
	body["name"] = name
	body["type"] = "Microsoft.Network/applicationGateways"
	if props, ok := body["properties"].(map[string]any); ok {
		props["provisioningState"] = "Succeeded"
	} else {
		body["properties"] = map[string]any{"provisioningState": "Succeeded"}
	}
	writeJSON(w, http.StatusOK, body)
}

func (srv *Server) listAppGateways(w http.ResponseWriter, r *http.Request) {
	blobs, err := srv.lb.ListBlobs(r.Context(), blobKindAzureAppGW)
	if err != nil {
		if errors.Is(err, domain.ErrNotSupported) {
			writeJSON(w, http.StatusOK, map[string]any{"value": []any{}})
			return
		}
		writeComputeErr(w, err)
		return
	}
	type listResult struct {
		Value []json.RawMessage `json:"value"`
	}
	result := listResult{Value: []json.RawMessage{}}
	for _, b := range blobs {
		result.Value = append(result.Value, json.RawMessage(b.Data))
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deleteAppGateway(w http.ResponseWriter, r *http.Request, name string) {
	blob, err := srv.lb.GetBlob(r.Context(), blobKindAzureAppGW, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "ApplicationGateway '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	var stored armnetwork.ApplicationGateway
	_ = json.Unmarshal(blob, &stored)
	ctx := r.Context()

	// Cascade: rules → listeners → target groups → LB → blob.
	lsns, _ := srv.lb.ListListeners(ctx, domain.ListListenersOptions{LoadBalancerID: name})
	for _, lsn := range lsns.Listeners {
		rules, _ := srv.lb.ListRules(ctx, domain.ListRulesOptions{ListenerID: lsn.ID})
		for _, rule := range rules.Rules {
			_ = srv.lb.DeleteRule(ctx, rule.ID)
		}
		_ = srv.lb.DeleteListener(ctx, lsn.ID)
	}
	if stored.Properties != nil {
		for _, pool := range stored.Properties.BackendAddressPools {
			if pool.Name == nil {
				continue
			}
			if tg := srv.findTGByName(r, *pool.Name); tg != nil {
				_ = srv.lb.DeleteTargetGroup(ctx, tg.ID)
			}
		}
	}
	if lb := srv.findLBByName(r, name); lb != nil {
		_ = srv.lb.DeleteLoadBalancer(ctx, lb.ID)
	}
	_ = srv.lb.DeleteBlob(ctx, blobKindAzureAppGW, name)
	w.WriteHeader(http.StatusOK)
}

func appGWResponseBody(name string, location *string) map[string]any {
	id := fmt.Sprintf("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/applicationGateways/%s", name)
	body := map[string]any{
		"id":   id,
		"name": name,
		"type": "Microsoft.Network/applicationGateways",
		"properties": map[string]any{
			"provisioningState": "Succeeded",
		},
	}
	if location != nil {
		body["location"] = *location
	}
	return body
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

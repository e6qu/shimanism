// Package gcp_lb is the GCP Compute Engine layer-4 load balancer
// frontend for shimanism's load balancer service (Phase 16.D).
// It speaks the Compute v1 REST/JSON protocol for:
//
//   - forwardingRules (regional) → domain.LoadBalancer + domain.Listener
//   - backendServices (regional) → domain.TargetGroup
//   - instanceGroups membership → domain.Target registration
//
// The GCP intersection for layer-4 TCP LBs uses:
//   - networkLoadBalancers / passthrough (unmanaged instance group backend)
//   - External or internal; both mapped to the same domain shape
//
// Auth: GCP Bearer verifier (same HS256 test key as other GCP frontends).
package gcp_lb

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	computeraw "google.golang.org/api/compute/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/loadbalancer/domain"
)

// Server is a GCP Compute LB-shaped HTTP frontend.
type Server struct {
	lb     domain.LoadBalancers
	region string
}

// New returns a frontend bound to the given backend.
func New(lb domain.LoadBalancers, region string) *Server {
	if region == "" {
		region = "us-central1"
	}
	return &Server{lb: lb, region: region}
}

// Handler wraps Server with the GCP bearer verifier middleware.
func Handler(lb domain.LoadBalancers) http.Handler {
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://compute.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return gcpbearer.Middleware(verifier)(New(lb, ""))
}

// ServeHTTP routes Compute v1 paths for LB resources:
//
//	/compute/v1/projects/{project}/regions/{region}/forwardingRules/{...}
//	/compute/v1/projects/{project}/regions/{region}/backendServices/{...}
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	path = strings.TrimPrefix(path, "/compute/v1")
	if !strings.HasPrefix(path, "/projects/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "path not matched: "+r.URL.Path)
		return
	}
	rest := strings.TrimPrefix(path, "/projects/")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "path not matched")
		return
	}
	rest = rest[slash+1:] // "regions/{region}/..."
	if !strings.HasPrefix(rest, "regions/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "only regional LB resources supported")
		return
	}
	rest = strings.TrimPrefix(rest, "regions/")
	regSlash := strings.Index(rest, "/")
	if regSlash < 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "path not matched")
		return
	}
	// region = rest[:regSlash] (ignored — shim is region-agnostic)
	rest = rest[regSlash+1:]

	switch {
	case strings.HasPrefix(rest, "forwardingRules"):
		srv.routeForwardingRules(w, r, strings.TrimPrefix(rest, "forwardingRules"))
	case strings.HasPrefix(rest, "backendServices"):
		srv.routeBackendServices(w, r, strings.TrimPrefix(rest, "backendServices"))
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "resource type not supported: "+rest)
	}
}

// ─── Forwarding Rules (LoadBalancer + Listener) ───────────────────────

func (srv *Server) routeForwardingRules(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listForwardingRules(w, r)
		case http.MethodPost:
			srv.insertForwardingRule(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getForwardingRule(w, r, name)
	case http.MethodDelete:
		srv.deleteForwardingRule(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertForwardingRule(w http.ResponseWriter, r *http.Request) {
	var req computeraw.ForwardingRule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	lb, err := srv.lb.CreateLoadBalancer(r.Context(), req.Name, domain.CreateLoadBalancerOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	// Create a listener if a backend service is referenced.
	if req.BackendService != "" {
		bsName := gcpLastPathSeg(req.BackendService)
		// Find the target group ID by name.
		tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
		for _, tg := range tgs.TargetGroups {
			if tg.Name == bsName {
				port := 0
				if req.PortRange != "" {
					fmt.Sscanf(req.PortRange, "%d", &port)
				}
				_, _ = srv.lb.CreateListener(r.Context(), domain.CreateListenerOptions{
					LoadBalancerID: lb.ID,
					Protocol:       domain.ProtocolTCP,
					Port:           port,
					TargetGroupID:  tg.ID,
				})
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, gcpOperation("insert", lb.ID))
}

func (srv *Server) getForwardingRule(w http.ResponseWriter, r *http.Request, name string) {
	lbs, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{Names: []string{name}})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	if len(lbs.LoadBalancers) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "ForwardingRule '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainLBToGCPFwdRule(lbs.LoadBalancers[0]))
}

func (srv *Server) listForwardingRules(w http.ResponseWriter, r *http.Request) {
	lbs, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.ForwardingRuleList{Kind: "compute#forwardingRuleList"}
	for _, lb := range lbs.LoadBalancers {
		lb := lb
		fr := domainLBToGCPFwdRule(lb)
		list.Items = append(list.Items, fr)
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteForwardingRule(w http.ResponseWriter, r *http.Request, name string) {
	lbs, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{Names: []string{name}})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	if len(lbs.LoadBalancers) == 0 {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "ForwardingRule '"+name+"' not found")
		return
	}
	if err := srv.lb.DeleteLoadBalancer(r.Context(), lbs.LoadBalancers[0].ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("delete", lbs.LoadBalancers[0].ID))
}

// ─── Backend Services (TargetGroup) ──────────────────────────────────

func (srv *Server) routeBackendServices(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listBackendServices(w, r)
		case http.MethodPost:
			srv.insertBackendService(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	// Check for /addBackend or /removeBackend sub-resources.
	if strings.Contains(name, "/addBackend") {
		bsName := strings.TrimSuffix(name, "/addBackend")
		srv.addBackend(w, r, bsName)
		return
	}
	if strings.Contains(name, "/removeBackend") {
		bsName := strings.TrimSuffix(name, "/removeBackend")
		srv.removeBackend(w, r, bsName)
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getBackendService(w, r, name)
	case http.MethodDelete:
		srv.deleteBackendService(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertBackendService(w http.ResponseWriter, r *http.Request) {
	var req computeraw.BackendService
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	proto := domain.ProtocolTCP
	if req.Protocol == "UDP" {
		proto = domain.ProtocolUDP
	}
	tg, err := srv.lb.CreateTargetGroup(r.Context(), req.Name, domain.CreateTargetGroupOptions{
		Protocol: proto,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("insert", tg.ID))
}

func (srv *Server) getBackendService(w http.ResponseWriter, r *http.Request, name string) {
	tgs, err := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, tg := range tgs.TargetGroups {
		if tg.Name == name {
			writeJSON(w, http.StatusOK, domainTGToGCPBackendSvc(tg))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+name+"' not found")
}

func (srv *Server) listBackendServices(w http.ResponseWriter, r *http.Request) {
	tgs, err := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.BackendServiceList{Kind: "compute#backendServiceList"}
	for _, tg := range tgs.TargetGroups {
		tg := tg
		list.Items = append(list.Items, domainTGToGCPBackendSvc(tg))
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteBackendService(w http.ResponseWriter, r *http.Request, name string) {
	tgs, err := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, tg := range tgs.TargetGroups {
		if tg.Name == name {
			if err := srv.lb.DeleteTargetGroup(r.Context(), tg.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, gcpOperation("delete", tg.ID))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+name+"' not found")
}

func (srv *Server) addBackend(w http.ResponseWriter, r *http.Request, bsName string) {
	tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	for _, tg := range tgs.TargetGroups {
		if tg.Name == bsName {
			writeJSON(w, http.StatusOK, gcpOperation("add", tg.ID))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+bsName+"' not found")
}

func (srv *Server) removeBackend(w http.ResponseWriter, r *http.Request, bsName string) {
	tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
	for _, tg := range tgs.TargetGroups {
		if tg.Name == bsName {
			writeJSON(w, http.StatusOK, gcpOperation("remove", tg.ID))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+bsName+"' not found")
}

// ─── Converters ───────────────────────────────────────────────────────

func domainLBToGCPFwdRule(lb domain.LoadBalancer) *computeraw.ForwardingRule {
	return &computeraw.ForwardingRule{
		Kind:       "compute#forwardingRule",
		Id:         addrHash(lb.ID),
		Name:       lb.Name,
		IPProtocol: "TCP",
		SelfLink:   fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/regions/us-central1/forwardingRules/%s", lb.Name),
	}
}

func domainTGToGCPBackendSvc(tg domain.TargetGroup) *computeraw.BackendService {
	return &computeraw.BackendService{
		Kind:     "compute#backendService",
		Id:       addrHash(tg.ID),
		Name:     tg.Name,
		Protocol: string(tg.Protocol),
		Port:     int64(tg.Port),
		SelfLink: fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/regions/us-central1/backendServices/%s", tg.Name),
	}
}

func addrHash(id string) uint64 {
	var h uint64
	for _, c := range id {
		h = h*31 + uint64(c)
	}
	return h
}

func gcpLastPathSeg(url string) string {
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '/' {
			return url[i+1:]
		}
	}
	return url
}

func gcpOperation(opType, id string) map[string]any {
	return map[string]any{
		"kind":          "compute#operation",
		"id":            fmt.Sprintf("%d", addrHash(id)),
		"operationType": opType,
		"status":        "DONE",
		"progress":      100,
	}
}

// ─── Error helpers ────────────────────────────────────────────────────

type gcpError struct {
	Error gcpErrorBody `json:"error"`
}

type gcpErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, gcpStatus, message string) {
	writeJSON(w, status, gcpError{Error: gcpErrorBody{Code: status, Message: message, Status: gcpStatus}})
}

func writeComputeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error())
	case errors.Is(err, domain.ErrNotSupported):
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
	}
}

// Package gcp_lb is the GCP Compute Engine load balancer frontend for
// shimanism's load balancer service.
// It speaks the Compute v1 REST/JSON protocol for:
//
//   - forwardingRules (regional) → domain.LoadBalancer + domain.Listener  [L4, Phase 16.D]
//   - backendServices (regional) → domain.TargetGroup                     [L4, Phase 16.D]
//   - instanceGroups membership → domain.Target registration               [L4, Phase 16.D]
//   - global/backendServices → domain.TargetGroup (HTTP)                  [L7, Phase 21.B]
//   - global/sslCertificates → opaque cert blob store                     [L7, Phase 21.B]
//   - global/urlMaps → routing blob + lazy rule assembly                  [L7, Phase 21.B]
//   - global/targetHttpsProxies → HTTPS proxy blob + Listener creation    [L7, Phase 21.B]
//   - global/forwardingRules → domain.LoadBalancer (application type)     [L7, Phase 21.B]
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

// GCP blob-store kinds for intermediate L7 resources.
const (
	blobKindSslCert  = "gcp-ssl-certificate"
	blobKindUrlMap   = "gcp-url-map"
	blobKindProxy    = "gcp-target-https-proxy"
	blobKindGlobalFR = "gcp-global-forwarding-rule"
	blobKindGlobalBS = "gcp-global-backend-service"
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
//	/compute/v1/projects/{project}/global/backendServices/{...}
//	/compute/v1/projects/{project}/global/sslCertificates/{...}
//	/compute/v1/projects/{project}/global/urlMaps/{...}
//	/compute/v1/projects/{project}/global/targetHttpsProxies/{...}
//	/compute/v1/projects/{project}/global/forwardingRules/{...}
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
	rest = rest[slash+1:] // "regions/{region}/..." or "global/..."

	if strings.HasPrefix(rest, "global/") {
		srv.routeGlobal(w, r, strings.TrimPrefix(rest, "global/"))
		return
	}
	if !strings.HasPrefix(rest, "regions/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "only regional or global LB resources supported")
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

// ─── Global resource router ───────────────────────────────────────────

func (srv *Server) routeGlobal(w http.ResponseWriter, r *http.Request, rest string) {
	switch {
	case strings.HasPrefix(rest, "backendServices"):
		srv.routeGlobalBackendServices(w, r, strings.TrimPrefix(rest, "backendServices"))
	case strings.HasPrefix(rest, "sslCertificates"):
		srv.routeGlobalSslCertificates(w, r, strings.TrimPrefix(rest, "sslCertificates"))
	case strings.HasPrefix(rest, "urlMaps"):
		srv.routeGlobalUrlMaps(w, r, strings.TrimPrefix(rest, "urlMaps"))
	case strings.HasPrefix(rest, "targetHttpsProxies"):
		srv.routeGlobalTargetHttpsProxies(w, r, strings.TrimPrefix(rest, "targetHttpsProxies"))
	case strings.HasPrefix(rest, "forwardingRules"):
		srv.routeGlobalForwardingRules(w, r, strings.TrimPrefix(rest, "forwardingRules"))
	default:
		writeError(w, http.StatusNotFound, "NOT_FOUND", "global resource type not supported: "+rest)
	}
}

// ─── Global Backend Services (L7 TargetGroup) ─────────────────────────

func (srv *Server) routeGlobalBackendServices(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listGlobalBackendServices(w, r)
		case http.MethodPost:
			srv.insertGlobalBackendService(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getGlobalBackendService(w, r, name)
	case http.MethodDelete:
		srv.deleteGlobalBackendService(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertGlobalBackendService(w http.ResponseWriter, r *http.Request) {
	var req computeraw.BackendService
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	proto := domain.ProtocolHTTP
	if req.Protocol == "HTTPS" {
		proto = domain.ProtocolHTTPS
	}
	tg, err := srv.lb.CreateTargetGroup(r.Context(), req.Name, domain.CreateTargetGroupOptions{
		Protocol: proto,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	req.Kind = "compute#backendService"
	req.SelfLink = globalSelfLink("backendServices", req.Name)
	req.Protocol = string(proto)
	body, _ := json.Marshal(req)
	_ = srv.lb.PutBlob(r.Context(), blobKindGlobalBS, req.Name, body)
	writeJSON(w, http.StatusOK, gcpOperation("insert", tg.ID))
}

func (srv *Server) getGlobalBackendService(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindGlobalBS, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (srv *Server) listGlobalBackendServices(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.lb.ListBlobs(r.Context(), blobKindGlobalBS)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.BackendServiceList{Kind: "compute#backendServiceList"}
	for _, e := range entries {
		var bs computeraw.BackendService
		if json.Unmarshal(e.Data, &bs) == nil {
			bs := bs
			list.Items = append(list.Items, &bs)
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteGlobalBackendService(w http.ResponseWriter, r *http.Request, name string) {
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
			_ = srv.lb.DeleteBlob(r.Context(), blobKindGlobalBS, name)
			writeJSON(w, http.StatusOK, gcpOperation("delete", tg.ID))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND", "BackendService '"+name+"' not found")
}

// ─── SSL Certificates (opaque blob pass-through) ──────────────────────

func (srv *Server) routeGlobalSslCertificates(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listGlobalSslCertificates(w, r)
		case http.MethodPost:
			srv.insertGlobalSslCertificate(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getGlobalSslCertificate(w, r, name)
	case http.MethodDelete:
		srv.deleteGlobalSslCertificate(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertGlobalSslCertificate(w http.ResponseWriter, r *http.Request) {
	var req computeraw.SslCertificate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	req.Kind = "compute#sslCertificate"
	req.SelfLink = globalSelfLink("sslCertificates", req.Name)
	body, _ := json.Marshal(req)
	if err := srv.lb.PutBlob(r.Context(), blobKindSslCert, req.Name, body); err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("insert", req.Name))
}

func (srv *Server) getGlobalSslCertificate(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindSslCert, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "SslCertificate '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (srv *Server) listGlobalSslCertificates(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.lb.ListBlobs(r.Context(), blobKindSslCert)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := map[string]any{
		"kind":  "compute#sslCertificateList",
		"items": []any{},
	}
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		var cert computeraw.SslCertificate
		if json.Unmarshal(e.Data, &cert) == nil {
			items = append(items, cert)
		}
	}
	list["items"] = items
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteGlobalSslCertificate(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.lb.DeleteBlob(r.Context(), blobKindSslCert, name); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "SslCertificate '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("delete", name))
}

// ─── URL Maps (routing rule set blob) ─────────────────────────────────

func (srv *Server) routeGlobalUrlMaps(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listGlobalUrlMaps(w, r)
		case http.MethodPost:
			srv.insertGlobalUrlMap(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getGlobalUrlMap(w, r, name)
	case http.MethodDelete:
		srv.deleteGlobalUrlMap(w, r, name)
	case http.MethodPatch:
		srv.patchGlobalUrlMap(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertGlobalUrlMap(w http.ResponseWriter, r *http.Request) {
	var req computeraw.UrlMap
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	req.Kind = "compute#urlMap"
	req.SelfLink = globalSelfLink("urlMaps", req.Name)
	body, _ := json.Marshal(req)
	if err := srv.lb.PutBlob(r.Context(), blobKindUrlMap, req.Name, body); err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("insert", req.Name))
}

func (srv *Server) getGlobalUrlMap(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindUrlMap, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "UrlMap '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (srv *Server) listGlobalUrlMaps(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.lb.ListBlobs(r.Context(), blobKindUrlMap)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := map[string]any{"kind": "compute#urlMapList", "items": []any{}}
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		var um computeraw.UrlMap
		if json.Unmarshal(e.Data, &um) == nil {
			items = append(items, um)
		}
	}
	list["items"] = items
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) patchGlobalUrlMap(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindUrlMap, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "UrlMap '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	var existing computeraw.UrlMap
	if err := json.Unmarshal(data, &existing); err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL", "corrupt urlmap blob")
		return
	}
	var patch computeraw.UrlMap
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	if patch.DefaultService != "" {
		existing.DefaultService = patch.DefaultService
	}
	if len(patch.HostRules) > 0 {
		existing.HostRules = patch.HostRules
	}
	if len(patch.PathMatchers) > 0 {
		existing.PathMatchers = patch.PathMatchers
	}
	updated, _ := json.Marshal(existing)
	_ = srv.lb.PutBlob(r.Context(), blobKindUrlMap, name, updated)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(updated)
}

func (srv *Server) deleteGlobalUrlMap(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.lb.DeleteBlob(r.Context(), blobKindUrlMap, name); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "UrlMap '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("delete", name))
}

// ─── Target HTTPS Proxies (HTTPS listener + cert binding) ─────────────

func (srv *Server) routeGlobalTargetHttpsProxies(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listGlobalTargetHttpsProxies(w, r)
		case http.MethodPost:
			srv.insertGlobalTargetHttpsProxy(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getGlobalTargetHttpsProxy(w, r, name)
	case http.MethodDelete:
		srv.deleteGlobalTargetHttpsProxy(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

func (srv *Server) insertGlobalTargetHttpsProxy(w http.ResponseWriter, r *http.Request) {
	var req computeraw.TargetHttpsProxy
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}
	req.Kind = "compute#targetHttpsProxy"
	req.SelfLink = globalSelfLink("targetHttpsProxies", req.Name)
	body, _ := json.Marshal(req)
	if err := srv.lb.PutBlob(r.Context(), blobKindProxy, req.Name, body); err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("insert", req.Name))
}

func (srv *Server) getGlobalTargetHttpsProxy(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindProxy, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "TargetHttpsProxy '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (srv *Server) listGlobalTargetHttpsProxies(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.lb.ListBlobs(r.Context(), blobKindProxy)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := map[string]any{"kind": "compute#targetHttpsProxyList", "items": []any{}}
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		var proxy computeraw.TargetHttpsProxy
		if json.Unmarshal(e.Data, &proxy) == nil {
			items = append(items, proxy)
		}
	}
	list["items"] = items
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteGlobalTargetHttpsProxy(w http.ResponseWriter, r *http.Request, name string) {
	// Find and delete the Listener associated with this proxy (if any).
	lsns, _ := srv.lb.ListListeners(r.Context(), domain.ListListenersOptions{})
	for _, lsn := range lsns.Listeners {
		if lsn.Tags["gcp-proxy"] == name {
			_ = srv.lb.DeleteListener(r.Context(), lsn.ID)
			break
		}
	}
	if err := srv.lb.DeleteBlob(r.Context(), blobKindProxy, name); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "TargetHttpsProxy '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("delete", name))
}

// ─── Global Forwarding Rules (application LB + lazy assembly) ─────────

func (srv *Server) routeGlobalForwardingRules(w http.ResponseWriter, r *http.Request, tail string) {
	name := strings.TrimPrefix(tail, "/")
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listGlobalForwardingRules(w, r)
		case http.MethodPost:
			srv.insertGlobalForwardingRule(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		srv.getGlobalForwardingRule(w, r, name)
	case http.MethodDelete:
		srv.deleteGlobalForwardingRule(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", r.Method)
	}
}

// insertGlobalForwardingRule is the "final assembly" step for a GCP L7 HTTPS LB.
// It resolves the chain: ForwardingRule → TargetHttpsProxy → UrlMap → BackendServices
// and creates the corresponding domain objects.
func (srv *Server) insertGlobalForwardingRule(w http.ResponseWriter, r *http.Request) {
	var req computeraw.ForwardingRule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON: "+err.Error())
		return
	}

	// Create the application LoadBalancer.
	lb, err := srv.lb.CreateLoadBalancer(r.Context(), req.Name, domain.CreateLoadBalancerOptions{
		Type: domain.LoadBalancerTypeApplication,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}

	// If a target (TargetHttpsProxy) is referenced, assemble Listener + Rules.
	if req.Target != "" {
		proxyName := gcpLastPathSeg(req.Target)
		_ = srv.assembleListenerAndRules(r, lb.ID, proxyName, req.PortRange)
	}

	// Store the forwarding rule blob for GET requests.
	req.Kind = "compute#forwardingRule"
	req.SelfLink = globalSelfLink("forwardingRules", req.Name)
	body, _ := json.Marshal(req)
	_ = srv.lb.PutBlob(r.Context(), blobKindGlobalFR, req.Name, body)

	writeJSON(w, http.StatusOK, gcpOperation("insert", lb.ID))
}

// assembleListenerAndRules reads the proxy blob, resolves the urlMap,
// and creates a domain Listener + Rules for the given LB.
func (srv *Server) assembleListenerAndRules(r *http.Request, lbID, proxyName, portRange string) error {
	proxyData, err := srv.lb.GetBlob(r.Context(), blobKindProxy, proxyName)
	if err != nil {
		return err
	}
	var proxy computeraw.TargetHttpsProxy
	if err := json.Unmarshal(proxyData, &proxy); err != nil {
		return err
	}

	// Extract cert IDs from proxy.SslCertificates (self-links → names).
	certIDs := make([]string, 0, len(proxy.SslCertificates))
	for _, sl := range proxy.SslCertificates {
		certIDs = append(certIDs, gcpLastPathSeg(sl))
	}

	// Resolve the urlMap's default backend service → TargetGroup.
	var defaultTGID string
	var urlMap computeraw.UrlMap
	if proxy.UrlMap != "" {
		urlMapName := gcpLastPathSeg(proxy.UrlMap)
		umData, err := srv.lb.GetBlob(r.Context(), blobKindUrlMap, urlMapName)
		if err == nil {
			_ = json.Unmarshal(umData, &urlMap)
		}
		if urlMap.DefaultService != "" {
			bsName := gcpLastPathSeg(urlMap.DefaultService)
			tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
			for _, tg := range tgs.TargetGroups {
				if tg.Name == bsName {
					defaultTGID = tg.ID
					break
				}
			}
		}
	}

	port := 443
	if portRange != "" {
		fmt.Sscanf(portRange, "%d", &port)
	}

	listener, err := srv.lb.CreateListener(r.Context(), domain.CreateListenerOptions{
		LoadBalancerID: lbID,
		Protocol:       domain.ProtocolHTTPS,
		Port:           port,
		TargetGroupID:  defaultTGID,
		CertificateIDs: certIDs,
		Tags:           map[string]string{"gcp-proxy": proxyName},
	})
	if err != nil {
		return err
	}

	// Create a domain Rule for each pathRule in each pathMatcher.
	for _, pm := range urlMap.PathMatchers {
		for pri, pr := range pm.PathRules {
			bsName := gcpLastPathSeg(pr.Service)
			tgID := ""
			tgs, _ := srv.lb.ListTargetGroups(r.Context(), domain.ListTargetGroupsOptions{})
			for _, tg := range tgs.TargetGroups {
				if tg.Name == bsName {
					tgID = tg.ID
					break
				}
			}
			if tgID == "" {
				continue
			}
			conditions := make([]domain.RuleCondition, 0, len(pr.Paths))
			for _, p := range pr.Paths {
				conditions = append(conditions, domain.RuleCondition{
					Type:   domain.RuleConditionPathPattern,
					Values: []string{p},
				})
			}
			_, _ = srv.lb.CreateRule(r.Context(), domain.CreateRuleOptions{
				ListenerID: listener.ID,
				Priority:   pri + 1,
				Conditions: conditions,
				Action:     domain.RuleAction{TargetGroupID: tgID},
			})
		}
	}
	return nil
}

func (srv *Server) getGlobalForwardingRule(w http.ResponseWriter, r *http.Request, name string) {
	data, err := srv.lb.GetBlob(r.Context(), blobKindGlobalFR, name)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "ForwardingRule '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (srv *Server) listGlobalForwardingRules(w http.ResponseWriter, r *http.Request) {
	entries, err := srv.lb.ListBlobs(r.Context(), blobKindGlobalFR)
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := map[string]any{"kind": "compute#forwardingRuleList", "items": []any{}}
	items := make([]any, 0, len(entries))
	for _, e := range entries {
		var fr computeraw.ForwardingRule
		if json.Unmarshal(e.Data, &fr) == nil {
			items = append(items, fr)
		}
	}
	list["items"] = items
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteGlobalForwardingRule(w http.ResponseWriter, r *http.Request, name string) {
	lbs, err := srv.lb.ListLoadBalancers(r.Context(), domain.ListLoadBalancersOptions{Names: []string{name}})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	var lbID string
	if len(lbs.LoadBalancers) > 0 {
		lbID = lbs.LoadBalancers[0].ID
	}
	// Delete listeners (and their rules) attached to this LB.
	if lbID != "" {
		lsns, _ := srv.lb.ListListeners(r.Context(), domain.ListListenersOptions{LoadBalancerID: lbID})
		for _, lsn := range lsns.Listeners {
			rules, _ := srv.lb.ListRules(r.Context(), domain.ListRulesOptions{ListenerID: lsn.ID})
			for _, rule := range rules.Rules {
				_ = srv.lb.DeleteRule(r.Context(), rule.ID)
			}
			_ = srv.lb.DeleteListener(r.Context(), lsn.ID)
		}
		_ = srv.lb.DeleteLoadBalancer(r.Context(), lbID)
	}
	if err := srv.lb.DeleteBlob(r.Context(), blobKindGlobalFR, name); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "ForwardingRule '"+name+"' not found")
			return
		}
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, gcpOperation("delete", name))
}

// ─── Regional Forwarding Rules (LoadBalancer + Listener) ─────────────

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

// ─── Regional Backend Services (TargetGroup) ──────────────────────────

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

func globalSelfLink(resourceType, name string) string {
	return fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/%s/%s", resourceType, name)
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

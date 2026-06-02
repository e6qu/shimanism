// Package gcp_compute is the GCP Compute Engine v1 frontend for
// shimanism's compute service, Phase 16.B (networking primitives).
// It speaks the HTTP+JSON wire protocol that
// google.golang.org/api/compute/v1 (the Discovery-generated REST SDK)
// and `gcloud compute` drive, and translates each request into a call
// on the neutral domain.Networking interface.
//
// Per AGENTS.md's reuse-over-reinvention rule, the request/response
// wire types come from google.golang.org/api/compute/v1 directly —
// the same raw types the SDK is generated from.
//
// Routes covered in Phase 16.B (networking only):
//   - networks.insert / delete / list / get
//   - subnetworks.insert / delete / list / get
//   - firewalls.insert / delete / patch / list / get
//   - addresses.insert / delete / list / get  (regional)
//
// Instances and other compute operations are added in Phase 16.C.
package gcp_compute

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	computeraw "google.golang.org/api/compute/v1"

	"github.com/e6qu/shimanism/internal/compute/domain"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	_ "github.com/e6qu/shimanism/services/compute/gen/gcp" // spec-drift contract
)

// Server is a Compute-Engine-v1-shaped HTTP frontend dispatching to a
// domain.Networking backend.
type Server struct {
	n domain.Networking
}

// New returns a frontend bound to the given backend.
func New(n domain.Networking) *Server { return &Server{n: n} }

// Handler wraps Server with the GCP bearer verifier middleware.
func Handler(n domain.Networking) http.Handler {
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://compute.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return gcpbearer.Middleware(verifier)(New(n))
}

// ServeHTTP dispatches Compute v1 REST paths for Phase 16.B
// networking resources. Path template:
//
//	/compute/v1/projects/{project}/global/networks/{...}
//	/compute/v1/projects/{project}/global/firewalls/{...}
//	/compute/v1/projects/{project}/regions/{region}/subnetworks/{...}
//	/compute/v1/projects/{project}/regions/{region}/addresses/{...}
//
// The leading /compute/v1/projects/{project}/ segment is stripped
// before routing so the sub-paths start with global/ or regions/.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// Strip leading /compute/v1 or /compute/v1/projects/{project}
	path = strings.TrimPrefix(path, "/compute/v1")
	if !strings.HasPrefix(path, "/projects/") {
		writeError(w, http.StatusNotFound, "Resource not found: "+r.URL.Path)
		return
	}
	// Drop /projects/{project}/
	rest := strings.TrimPrefix(path, "/projects/")
	slash := strings.Index(rest, "/")
	if slash < 0 {
		writeError(w, http.StatusNotFound, "Resource not found: "+r.URL.Path)
		return
	}
	// project = rest[:slash]; we don't use project as the shim is
	// multi-tenant via the backend only
	rest = rest[slash+1:] // starts with "global/" or "regions/"

	switch {
	case strings.HasPrefix(rest, "global/networks"):
		srv.routeNetworks(w, r, strings.TrimPrefix(rest, "global/networks"))
	case strings.HasPrefix(rest, "global/firewalls"):
		srv.routeFirewalls(w, r, strings.TrimPrefix(rest, "global/firewalls"))
	case strings.HasPrefix(rest, "regions/"):
		srv.routeRegional(w, r, strings.TrimPrefix(rest, "regions/"))
	default:
		writeError(w, http.StatusNotFound, "Resource not found: "+r.URL.Path)
	}
}

// ─── Networks (VPC) ──────────────────────────────────────────────────

func (srv *Server) routeNetworks(w http.ResponseWriter, r *http.Request, tail string) {
	// tail is either "" (collection) or "/{resourceName}" (item)
	tail = strings.TrimPrefix(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listNetworks(w, r)
		case http.MethodPost:
			srv.insertNetwork(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
		}
		return
	}
	name := tail
	switch r.Method {
	case http.MethodGet:
		srv.getNetwork(w, r, name)
	case http.MethodDelete:
		srv.deleteNetwork(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
	}
}

func (srv *Server) insertNetwork(w http.ResponseWriter, r *http.Request) {
	var req computeraw.Network
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	name := req.Name
	n, err := srv.n.CreateNetwork(r.Context(), name, domain.CreateNetworkOptions{
		CIDR: req.IPv4Range,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	op := insertOperation("networks", n.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(op)
}

func (srv *Server) getNetwork(w http.ResponseWriter, r *http.Request, name string) {
	// GCP uses name (not ID) to fetch networks.
	res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, n := range res.Networks {
		if n.Name == name {
			raw := domainNetworkToGCP(n)
			writeJSON(w, http.StatusOK, raw)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource 'projects/.../global/networks/%s' was not found", name))
}

func (srv *Server) listNetworks(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.NetworkList{Kind: "compute#networkList"}
	for _, n := range res.Networks {
		gcpN := domainNetworkToGCP(n)
		list.Items = append(list.Items, gcpN)
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteNetwork(w http.ResponseWriter, r *http.Request, name string) {
	// Look up the ID by name first.
	res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, n := range res.Networks {
		if n.Name == name {
			if err := srv.n.DeleteNetwork(r.Context(), n.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			op := deleteOperation("networks", n.ID)
			writeJSON(w, http.StatusOK, op)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource 'projects/.../global/networks/%s' was not found", name))
}

// ─── Firewalls (Security Groups) ─────────────────────────────────────

func (srv *Server) routeFirewalls(w http.ResponseWriter, r *http.Request, tail string) {
	tail = strings.TrimPrefix(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listFirewalls(w, r)
		case http.MethodPost:
			srv.insertFirewall(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
		}
		return
	}
	name := tail
	switch r.Method {
	case http.MethodGet:
		srv.getFirewall(w, r, name)
	case http.MethodDelete:
		srv.deleteFirewall(w, r, name)
	case http.MethodPatch, http.MethodPut:
		srv.patchFirewall(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
	}
}

func (srv *Server) insertFirewall(w http.ResponseWriter, r *http.Request) {
	var req computeraw.Firewall
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	// GCP firewalls are not attached to a specific VPC in the request
	// (the network field is a resource URL). Extract the network name.
	networkName := gcpNetworkNameFromURL(req.Network)
	networkID := ""
	if networkName != "" {
		res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
		if err != nil {
			writeComputeErr(w, err)
			return
		}
		for _, n := range res.Networks {
			if n.Name == networkName {
				networkID = n.ID
				break
			}
		}
	}
	sg, err := srv.n.CreateSecurityGroup(r.Context(), req.Name, domain.CreateSecurityGroupOptions{
		NetworkID:   networkID,
		Description: req.Description,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	// Add allow rules from the firewall definition.
	for _, allow := range req.Allowed {
		for _, cidr := range req.SourceRanges {
			rule := gcpAllowedToDomainRule(allow, cidr, domain.Inbound)
			_ = srv.n.AddRule(r.Context(), sg.ID, rule)
		}
		if len(req.SourceRanges) == 0 {
			rule := gcpAllowedToDomainRule(allow, "0.0.0.0/0", domain.Inbound)
			_ = srv.n.AddRule(r.Context(), sg.ID, rule)
		}
	}
	op := insertOperation("firewalls", sg.ID)
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) getFirewall(w http.ResponseWriter, r *http.Request, name string) {
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, sg := range res.SecurityGroups {
		if sg.Name == name {
			writeJSON(w, http.StatusOK, domainSGToGCPFirewall(sg))
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource 'projects/.../global/firewalls/%s' was not found", name))
}

func (srv *Server) listFirewalls(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.FirewallList{Kind: "compute#firewallList"}
	for _, sg := range res.SecurityGroups {
		list.Items = append(list.Items, domainSGToGCPFirewall(sg))
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteFirewall(w http.ResponseWriter, r *http.Request, name string) {
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, sg := range res.SecurityGroups {
		if sg.Name == name {
			if err := srv.n.DeleteSecurityGroup(r.Context(), sg.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			op := deleteOperation("firewalls", sg.ID)
			writeJSON(w, http.StatusOK, op)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource 'projects/.../global/firewalls/%s' was not found", name))
}

func (srv *Server) patchFirewall(w http.ResponseWriter, r *http.Request, name string) {
	var req computeraw.Firewall
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, sg := range res.SecurityGroups {
		if sg.Name == name {
			// Replace all rules by removing current and re-adding from req.
			current, _ := srv.n.GetSecurityGroup(r.Context(), sg.ID)
			for _, rule := range current.Rules {
				_ = srv.n.RemoveRule(r.Context(), sg.ID, rule)
			}
			for _, allow := range req.Allowed {
				for _, cidr := range req.SourceRanges {
					rule := gcpAllowedToDomainRule(allow, cidr, domain.Inbound)
					_ = srv.n.AddRule(r.Context(), sg.ID, rule)
				}
			}
			op := insertOperation("firewalls", sg.ID)
			writeJSON(w, http.StatusOK, op)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource 'projects/.../global/firewalls/%s' was not found", name))
}

// ─── Regional: Subnetworks + Addresses ───────────────────────────────

func (srv *Server) routeRegional(w http.ResponseWriter, r *http.Request, rest string) {
	// rest = "{region}/subnetworks/{...}" or "{region}/addresses/{...}"
	slash := strings.Index(rest, "/")
	if slash < 0 {
		writeError(w, http.StatusNotFound, "Resource not found")
		return
	}
	region := rest[:slash]
	tail := rest[slash+1:]

	switch {
	case strings.HasPrefix(tail, "subnetworks"):
		srv.routeSubnetworks(w, r, region, strings.TrimPrefix(tail, "subnetworks"))
	case strings.HasPrefix(tail, "addresses"):
		srv.routeAddresses(w, r, region, strings.TrimPrefix(tail, "addresses"))
	default:
		writeError(w, http.StatusNotFound, "Resource not found: "+r.URL.Path)
	}
}

func (srv *Server) routeSubnetworks(w http.ResponseWriter, r *http.Request, region, tail string) {
	tail = strings.TrimPrefix(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listSubnetworks(w, r, region)
		case http.MethodPost:
			srv.insertSubnetwork(w, r, region)
		default:
			writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
		}
		return
	}
	name := tail
	switch r.Method {
	case http.MethodGet:
		srv.getSubnetwork(w, r, region, name)
	case http.MethodDelete:
		srv.deleteSubnetwork(w, r, region, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
	}
}

func (srv *Server) insertSubnetwork(w http.ResponseWriter, r *http.Request, region string) {
	var req computeraw.Subnetwork
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	networkName := gcpNetworkNameFromURL(req.Network)
	networkID := ""
	res, _ := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	for _, n := range res.Networks {
		if n.Name == networkName {
			networkID = n.ID
			break
		}
	}
	s, err := srv.n.CreateSubnet(r.Context(), req.Name, domain.CreateSubnetOptions{
		NetworkID: networkID,
		CIDR:      req.IpCidrRange,
		Zone:      region,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	op := insertOperation("subnetworks", s.ID)
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) getSubnetwork(w http.ResponseWriter, r *http.Request, region, name string) {
	res, err := srv.n.ListSubnets(r.Context(), domain.ListSubnetsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, s := range res.Subnets {
		if s.Name == name {
			writeJSON(w, http.StatusOK, domainSubnetToGCP(s))
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource '%s' was not found", name))
}

func (srv *Server) listSubnetworks(w http.ResponseWriter, r *http.Request, region string) {
	res, err := srv.n.ListSubnets(r.Context(), domain.ListSubnetsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.SubnetworkList{Kind: "compute#subnetworkList"}
	for _, s := range res.Subnets {
		if s.Zone == "" || s.Zone == region {
			list.Items = append(list.Items, domainSubnetToGCP(s))
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteSubnetwork(w http.ResponseWriter, r *http.Request, region, name string) {
	res, err := srv.n.ListSubnets(r.Context(), domain.ListSubnetsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, s := range res.Subnets {
		if s.Name == name {
			if err := srv.n.DeleteSubnet(r.Context(), s.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			op := deleteOperation("subnetworks", s.ID)
			writeJSON(w, http.StatusOK, op)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource '%s' was not found", name))
}

func (srv *Server) routeAddresses(w http.ResponseWriter, r *http.Request, region, tail string) {
	tail = strings.TrimPrefix(tail, "/")
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listAddresses(w, r, region)
		case http.MethodPost:
			srv.insertAddress(w, r, region)
		default:
			writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
		}
		return
	}
	name := tail
	switch r.Method {
	case http.MethodGet:
		srv.getAddress(w, r, region, name)
	case http.MethodDelete:
		srv.deleteAddress(w, r, region, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
	}
}

func (srv *Server) insertAddress(w http.ResponseWriter, r *http.Request, region string) {
	var req computeraw.Address
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ip, err := srv.n.AllocatePublicIP(r.Context(), domain.AllocatePublicIPOptions{Region: region})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	op := insertOperation("addresses", ip.ID)
	writeJSON(w, http.StatusOK, op)
}

func (srv *Server) getAddress(w http.ResponseWriter, r *http.Request, region, name string) {
	res, err := srv.n.ListPublicIPs(r.Context(), domain.ListPublicIPsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, ip := range res.PublicIPs {
		if ip.Name == name {
			writeJSON(w, http.StatusOK, domainIPToGCP(ip))
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource '%s' was not found", name))
}

func (srv *Server) listAddresses(w http.ResponseWriter, r *http.Request, region string) {
	res, err := srv.n.ListPublicIPs(r.Context(), domain.ListPublicIPsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	list := &computeraw.AddressList{Kind: "compute#addressList"}
	for _, ip := range res.PublicIPs {
		if ip.Region == "" || ip.Region == region {
			list.Items = append(list.Items, domainIPToGCP(ip))
		}
	}
	writeJSON(w, http.StatusOK, list)
}

func (srv *Server) deleteAddress(w http.ResponseWriter, r *http.Request, region, name string) {
	res, err := srv.n.ListPublicIPs(r.Context(), domain.ListPublicIPsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, ip := range res.PublicIPs {
		if ip.Name == name {
			if err := srv.n.ReleasePublicIP(r.Context(), ip.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			op := deleteOperation("addresses", ip.ID)
			writeJSON(w, http.StatusOK, op)
			return
		}
	}
	writeError(w, http.StatusNotFound, fmt.Sprintf("The resource '%s' was not found", name))
}

// ─── Wire-type converters ────────────────────────────────────────────

func domainNetworkToGCP(n domain.Network) *computeraw.Network {
	return &computeraw.Network{
		Kind:      "compute#network",
		Id:        uint64(addrID(n.ID)),
		Name:      n.Name,
		IPv4Range: n.CIDR,
		SelfLink:  fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/networks/%s", n.Name),
	}
}

func domainSubnetToGCP(s domain.Subnet) *computeraw.Subnetwork {
	return &computeraw.Subnetwork{
		Kind:        "compute#subnetwork",
		Id:          uint64(addrID(s.ID)),
		Name:        s.Name,
		IpCidrRange: s.CIDR,
		Region:      s.Zone,
		Network:     fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/networks/%s", s.NetworkID),
		SelfLink:    fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/regions/%s/subnetworks/%s", s.Zone, s.Name),
	}
}

func domainSGToGCPFirewall(sg domain.SecurityGroup) *computeraw.Firewall {
	fw := &computeraw.Firewall{
		Kind:        "compute#firewall",
		Id:          uint64(addrID(sg.ID)),
		Name:        sg.Name,
		Description: sg.Description,
		Network:     fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/networks/%s", sg.NetworkID),
		SelfLink:    fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/firewalls/%s", sg.Name),
		Direction:   "INGRESS",
	}
	for _, r := range sg.Rules {
		if r.Direction == domain.Outbound {
			fw.Direction = "EGRESS"
		}
		allowed := &computeraw.FirewallAllowed{
			IPProtocol: r.Protocol,
		}
		if r.PortFrom != 0 {
			if r.PortTo != 0 && r.PortTo != r.PortFrom {
				allowed.Ports = []string{fmt.Sprintf("%d-%d", r.PortFrom, r.PortTo)}
			} else {
				allowed.Ports = []string{fmt.Sprintf("%d", r.PortFrom)}
			}
		}
		fw.Allowed = append(fw.Allowed, allowed)
		fw.SourceRanges = append(fw.SourceRanges, r.CIDRs...)
	}
	return fw
}

func domainIPToGCP(ip domain.PublicIP) *computeraw.Address {
	a := &computeraw.Address{
		Kind:     "compute#address",
		Id:       uint64(addrID(ip.ID)),
		Name:     ip.Name,
		Address:  ip.Address,
		Region:   ip.Region,
		Status:   "RESERVED",
		SelfLink: fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/regions/%s/addresses/%s", ip.Region, ip.Name),
	}
	if ip.InstanceID != "" {
		a.Status = "IN_USE"
		a.Users = []string{ip.InstanceID}
	}
	return a
}

func gcpAllowedToDomainRule(allow *computeraw.FirewallAllowed, cidr string, dir domain.RuleDirection) domain.SecurityGroupRule {
	r := domain.SecurityGroupRule{Protocol: allow.IPProtocol, Direction: dir}
	if len(allow.Ports) > 0 {
		portStr := allow.Ports[0]
		if idx := strings.Index(portStr, "-"); idx >= 0 {
			fmt.Sscanf(portStr[:idx], "%d", &r.PortFrom)
			fmt.Sscanf(portStr[idx+1:], "%d", &r.PortTo)
		} else {
			fmt.Sscanf(portStr, "%d", &r.PortFrom)
			r.PortTo = r.PortFrom
		}
	}
	if cidr != "" {
		r.CIDRs = []string{cidr}
	}
	return r
}

func gcpNetworkNameFromURL(networkURL string) string {
	// Extract the last path segment from a GCP resource URL like
	// "https://.../global/networks/my-network".
	parts := strings.Split(networkURL, "/")
	if len(parts) == 0 {
		return networkURL
	}
	return parts[len(parts)-1]
}

// addrID converts a string ID like "net-00000001" to a stable uint64
// for the GCP wire format's Id field.
func addrID(id string) uint64 {
	var h uint64
	for _, c := range id {
		h = h*31 + uint64(c)
	}
	return h
}

// insertOperation returns a synthetic compute#operation for insert/create.
// GCP's Operation.Id is a uint64 serialized as a JSON string (has `,string`
// tag in the SDK struct), so emit it as a quoted string.
func insertOperation(resourceType, id string) map[string]any {
	return map[string]any{
		"kind":          "compute#operation",
		"id":            fmt.Sprintf("%d", addrID(id)),
		"operationType": "insert",
		"targetLink":    fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/%s/%s", resourceType, id),
		"status":        "DONE",
		"progress":      100,
	}
}

// deleteOperation returns a synthetic compute#operation for delete.
func deleteOperation(resourceType, id string) map[string]any {
	return map[string]any{
		"kind":          "compute#operation",
		"id":            fmt.Sprintf("%d", addrID(id)),
		"operationType": "delete",
		"targetLink":    fmt.Sprintf("https://www.googleapis.com/compute/v1/projects/shim/global/%s/%s", resourceType, id),
		"status":        "DONE",
		"progress":      100,
	}
}

// ─── Error helpers ────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type gcpError struct {
	Error gcpErrorBody `json:"error"`
}

type gcpErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	gcpStatus := httpStatusToGCPStatus(status)
	writeJSON(w, status, gcpError{Error: gcpErrorBody{Code: status, Message: message, Status: gcpStatus}})
}

func writeComputeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, domain.ErrNotSupported):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func httpStatusToGCPStatus(code int) string {
	switch code {
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusConflict:
		return "ALREADY_EXISTS"
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusMethodNotAllowed:
		return "UNIMPLEMENTED"
	default:
		return "INTERNAL"
	}
}

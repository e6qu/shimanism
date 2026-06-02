// Package azure_network is the Azure Network ARM frontend for
// shimanism's compute service, Phase 16.B (networking primitives).
// It speaks the HTTP+JSON ARM REST wire protocol that
// azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6 and
// `az network` CLI drive, and translates each request into a call on
// the neutral domain.Networking interface.
//
// Per AGENTS.md reuse-over-reinvention rule, request/response wire
// types come from armnetwork/v6 directly — the same types the SDK uses.
//
// Resource types covered in Phase 16.B (networking only):
//   - Microsoft.Network/virtualNetworks (VNets → domain Networks)
//   - Microsoft.Network/virtualNetworks/{name}/subnets (Subnets)
//   - Microsoft.Network/networkSecurityGroups (NSGs → domain SGs)
//   - Microsoft.Network/networkSecurityGroups/{name}/securityRules
//   - Microsoft.Network/publicIPAddresses (EIPs → domain PublicIPs)
//
// Azure ARM path template:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/
//	   Microsoft.Network/{resourceType}/{name}[/{subType}/{subName}]
//
// The shim ignores subscription + resource-group (identity-free per
// the stateless-shim rule). Resource names are the primary key.
package azure_network

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v6"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/compute/domain"
)

// Server is an Azure-Network-shaped HTTP frontend dispatching to a
// domain.Networking backend.
type Server struct {
	n domain.Networking
}

// New returns a frontend bound to the given backend.
func New(n domain.Networking) *Server { return &Server{n: n} }

// Handler wraps Server with the Azure bearer verifier middleware.
func Handler(n domain.Networking) http.Handler {
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))
	return mw(New(n))
}

// ServeHTTP dispatches ARM paths for Microsoft.Network resources.
// Strips subscription/RG prefix; routes on resource type + name.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// Strip /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/
	idx := strings.Index(strings.ToLower(path), "/providers/microsoft.network/")
	if idx < 0 {
		writeAzureError(w, http.StatusNotFound, "NotFound", "path does not match Microsoft.Network provider: "+path)
		return
	}
	tail := path[idx+len("/providers/microsoft.network/"):]

	parts := strings.SplitN(tail, "/", 4)
	// parts[0] = resourceType; parts[1] = resourceName (or absent for collection)
	resourceType := strings.ToLower(parts[0])
	resourceName := ""
	if len(parts) >= 2 {
		resourceName = parts[1]
	}

	switch resourceType {
	case "virtualnetworks":
		if len(parts) == 4 && strings.ToLower(parts[2]) == "subnets" {
			srv.routeSubnets(w, r, resourceName, parts[3])
		} else {
			srv.routeVNets(w, r, resourceName)
		}
	case "networksecuritygroups":
		if len(parts) == 4 && strings.ToLower(parts[2]) == "securityrules" {
			srv.routeSecurityRules(w, r, resourceName, parts[3])
		} else {
			srv.routeNSGs(w, r, resourceName)
		}
	case "publicipaddresses":
		srv.routePublicIPs(w, r, resourceName)
	default:
		writeAzureError(w, http.StatusNotFound, "NotFound", "resource type not supported: "+resourceType)
	}
}

// ─── VirtualNetworks ──────────────────────────────────────────────────

func (srv *Server) routeVNets(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		// Collection route.
		switch r.Method {
		case http.MethodGet:
			srv.listVNets(w, r)
		default:
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateVNet(w, r, name)
	case http.MethodGet:
		srv.getVNet(w, r, name)
	case http.MethodDelete:
		srv.deleteVNet(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateVNet(w http.ResponseWriter, r *http.Request, name string) {
	var req armnetwork.VirtualNetwork
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	cidr := ""
	if req.Properties != nil && len(req.Properties.AddressSpace.AddressPrefixes) > 0 {
		cidr = *req.Properties.AddressSpace.AddressPrefixes[0]
	}
	// Check if VNet exists (update case).
	existing := srv.findNetworkByName(r, name)
	if existing != nil {
		// Update: no-op at domain level (stateless, attribute changes acknowledged)
		n := *existing
		writeJSON(w, http.StatusOK, domainNetworkToAzure(n, req.Location))
		return
	}
	n, err := srv.n.CreateNetwork(r.Context(), name, domain.CreateNetworkOptions{
		CIDR: cidr,
		Tags: azureTagsToDomain(req.Tags),
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainNetworkToAzure(n, req.Location))
}

func (srv *Server) getVNet(w http.ResponseWriter, r *http.Request, name string) {
	n := srv.findNetworkByName(r, name)
	if n == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualNetwork '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainNetworkToAzure(*n, nil))
}

func (srv *Server) listVNets(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	result := armnetwork.VirtualNetworkListResult{Value: []*armnetwork.VirtualNetwork{}}
	for _, n := range res.Networks {
		n := n
		v := domainNetworkToAzure(n, nil)
		result.Value = append(result.Value, v)
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deleteVNet(w http.ResponseWriter, r *http.Request, name string) {
	n := srv.findNetworkByName(r, name)
	if n == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualNetwork '"+name+"' not found")
		return
	}
	if err := srv.n.DeleteNetwork(r.Context(), n.ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Subnets ──────────────────────────────────────────────────────────

func (srv *Server) routeSubnets(w http.ResponseWriter, r *http.Request, vnetName, subnetName string) {
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateSubnet(w, r, vnetName, subnetName)
	case http.MethodGet:
		srv.getSubnet(w, r, vnetName, subnetName)
	case http.MethodDelete:
		srv.deleteSubnet(w, r, vnetName, subnetName)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateSubnet(w http.ResponseWriter, r *http.Request, vnetName, subnetName string) {
	var req armnetwork.Subnet
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	vnet := srv.findNetworkByName(r, vnetName)
	if vnet == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualNetwork '"+vnetName+"' not found")
		return
	}
	cidr := ""
	if req.Properties != nil && req.Properties.AddressPrefix != nil {
		cidr = *req.Properties.AddressPrefix
	}
	s, err := srv.n.CreateSubnet(r.Context(), subnetName, domain.CreateSubnetOptions{
		NetworkID: vnet.ID,
		CIDR:      cidr,
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainSubnetToAzure(s))
}

func (srv *Server) getSubnet(w http.ResponseWriter, r *http.Request, vnetName, subnetName string) {
	vnet := srv.findNetworkByName(r, vnetName)
	if vnet == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualNetwork '"+vnetName+"' not found")
		return
	}
	res, err := srv.n.ListSubnets(r.Context(), domain.ListSubnetsOptions{NetworkID: vnet.ID})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, s := range res.Subnets {
		if s.Name == subnetName {
			writeJSON(w, http.StatusOK, domainSubnetToAzure(s))
			return
		}
	}
	writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "Subnet '"+subnetName+"' not found")
}

func (srv *Server) deleteSubnet(w http.ResponseWriter, r *http.Request, vnetName, subnetName string) {
	vnet := srv.findNetworkByName(r, vnetName)
	if vnet == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "VirtualNetwork '"+vnetName+"' not found")
		return
	}
	res, err := srv.n.ListSubnets(r.Context(), domain.ListSubnetsOptions{NetworkID: vnet.ID})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	for _, s := range res.Subnets {
		if s.Name == subnetName {
			if err := srv.n.DeleteSubnet(r.Context(), s.ID); err != nil {
				writeComputeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "Subnet '"+subnetName+"' not found")
}

// ─── NetworkSecurityGroups (NSGs) ─────────────────────────────────────

func (srv *Server) routeNSGs(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listNSGs(w, r)
		default:
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateNSG(w, r, name)
	case http.MethodGet:
		srv.getNSG(w, r, name)
	case http.MethodDelete:
		srv.deleteNSG(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateNSG(w http.ResponseWriter, r *http.Request, name string) {
	var req armnetwork.SecurityGroup
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	existing := srv.findSGByName(r, name)
	if existing != nil {
		writeJSON(w, http.StatusOK, domainSGToAzure(*existing, req.Location))
		return
	}
	sg, err := srv.n.CreateSecurityGroup(r.Context(), name, domain.CreateSecurityGroupOptions{
		Tags: azureTagsToDomain(req.Tags),
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	// Add security rules from request body.
	if req.Properties != nil {
		for i, rule := range req.Properties.SecurityRules {
			if rule.Properties == nil {
				continue
			}
			domainRule := azureRuleToDomain(rule, i)
			_ = srv.n.AddRule(r.Context(), sg.ID, domainRule)
		}
	}
	updated, _ := srv.n.GetSecurityGroup(r.Context(), sg.ID)
	writeJSON(w, http.StatusCreated, domainSGToAzure(updated, req.Location))
}

func (srv *Server) getNSG(w http.ResponseWriter, r *http.Request, name string) {
	sg := srv.findSGByName(r, name)
	if sg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "NetworkSecurityGroup '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainSGToAzure(*sg, nil))
}

func (srv *Server) listNSGs(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	result := armnetwork.SecurityGroupListResult{Value: []*armnetwork.SecurityGroup{}}
	for _, sg := range res.SecurityGroups {
		sg := sg
		v := domainSGToAzure(sg, nil)
		result.Value = append(result.Value, v)
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deleteNSG(w http.ResponseWriter, r *http.Request, name string) {
	sg := srv.findSGByName(r, name)
	if sg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "NetworkSecurityGroup '"+name+"' not found")
		return
	}
	if err := srv.n.DeleteSecurityGroup(r.Context(), sg.ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── SecurityRules (sub-resource of NSG) ─────────────────────────────

func (srv *Server) routeSecurityRules(w http.ResponseWriter, r *http.Request, nsgName, ruleName string) {
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdateSecurityRule(w, r, nsgName, ruleName)
	case http.MethodGet:
		srv.getSecurityRule(w, r, nsgName, ruleName)
	case http.MethodDelete:
		srv.deleteSecurityRule(w, r, nsgName, ruleName)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdateSecurityRule(w http.ResponseWriter, r *http.Request, nsgName, ruleName string) {
	var req armnetwork.SecurityRule
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	sg := srv.findSGByName(r, nsgName)
	if sg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "NetworkSecurityGroup '"+nsgName+"' not found")
		return
	}
	if req.Properties == nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", "SecurityRule properties required")
		return
	}
	priority := 0
	if req.Properties.Priority != nil {
		priority = int(*req.Properties.Priority)
	}
	domainRule := azureRuleToDomain(&req, priority)
	if err := srv.n.AddRule(r.Context(), sg.ID, domainRule); err != nil {
		writeComputeErr(w, err)
		return
	}
	req.Name = &ruleName
	writeJSON(w, http.StatusCreated, req)
}

func (srv *Server) getSecurityRule(w http.ResponseWriter, r *http.Request, nsgName, ruleName string) {
	sg := srv.findSGByName(r, nsgName)
	if sg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "NetworkSecurityGroup '"+nsgName+"' not found")
		return
	}
	for i, rule := range sg.Rules {
		rname := fmt.Sprintf("rule-%d", i)
		if rname == ruleName {
			writeJSON(w, http.StatusOK, domainRuleToAzure(rule, ruleName, int32(i+100)))
			return
		}
	}
	writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "SecurityRule '"+ruleName+"' not found")
}

func (srv *Server) deleteSecurityRule(w http.ResponseWriter, r *http.Request, nsgName, ruleName string) {
	sg := srv.findSGByName(r, nsgName)
	if sg == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "NetworkSecurityGroup '"+nsgName+"' not found")
		return
	}
	for i, rule := range sg.Rules {
		rname := fmt.Sprintf("rule-%d", i)
		if rname == ruleName {
			if err := srv.n.RemoveRule(r.Context(), sg.ID, rule); err != nil {
				writeComputeErr(w, err)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "SecurityRule '"+ruleName+"' not found")
}

// ─── PublicIPAddresses ────────────────────────────────────────────────

func (srv *Server) routePublicIPs(w http.ResponseWriter, r *http.Request, name string) {
	if name == "" {
		switch r.Method {
		case http.MethodGet:
			srv.listPublicIPs(w, r)
		default:
			writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
		return
	}
	switch r.Method {
	case http.MethodPut:
		srv.createOrUpdatePublicIP(w, r, name)
	case http.MethodGet:
		srv.getPublicIP(w, r, name)
	case http.MethodDelete:
		srv.deletePublicIP(w, r, name)
	default:
		writeAzureError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	}
}

func (srv *Server) createOrUpdatePublicIP(w http.ResponseWriter, r *http.Request, name string) {
	var req armnetwork.PublicIPAddress
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzureError(w, http.StatusBadRequest, "InvalidRequestContent", err.Error())
		return
	}
	// Check if it already exists (update case).
	existing := srv.findPublicIPByName(r, name)
	if existing != nil {
		writeJSON(w, http.StatusOK, domainIPToAzure(*existing, req.Location))
		return
	}
	region := ""
	if req.Location != nil {
		region = *req.Location
	}
	ip, err := srv.n.AllocatePublicIP(r.Context(), domain.AllocatePublicIPOptions{
		Name:   name, // Azure resource name persisted via domain
		Region: region,
		Tags:   azureTagsToDomain(req.Tags),
	})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, domainIPToAzure(ip, req.Location))
}

func (srv *Server) getPublicIP(w http.ResponseWriter, r *http.Request, name string) {
	ip := srv.findPublicIPByName(r, name)
	if ip == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "PublicIPAddress '"+name+"' not found")
		return
	}
	writeJSON(w, http.StatusOK, domainIPToAzure(*ip, nil))
}

func (srv *Server) listPublicIPs(w http.ResponseWriter, r *http.Request) {
	res, err := srv.n.ListPublicIPs(r.Context(), domain.ListPublicIPsOptions{})
	if err != nil {
		writeComputeErr(w, err)
		return
	}
	result := armnetwork.PublicIPAddressListResult{Value: []*armnetwork.PublicIPAddress{}}
	for _, ip := range res.PublicIPs {
		ip := ip
		v := domainIPToAzure(ip, nil)
		result.Value = append(result.Value, v)
	}
	writeJSON(w, http.StatusOK, result)
}

func (srv *Server) deletePublicIP(w http.ResponseWriter, r *http.Request, name string) {
	ip := srv.findPublicIPByName(r, name)
	if ip == nil {
		writeAzureError(w, http.StatusNotFound, "ResourceNotFound", "PublicIPAddress '"+name+"' not found")
		return
	}
	if err := srv.n.ReleasePublicIP(r.Context(), ip.ID); err != nil {
		writeComputeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Lookup helpers ───────────────────────────────────────────────────

func (srv *Server) findNetworkByName(r *http.Request, name string) *domain.Network {
	res, err := srv.n.ListNetworks(r.Context(), domain.ListNetworksOptions{})
	if err != nil {
		return nil
	}
	for _, n := range res.Networks {
		if n.Name == name {
			n := n
			return &n
		}
	}
	return nil
}

func (srv *Server) findSGByName(r *http.Request, name string) *domain.SecurityGroup {
	res, err := srv.n.ListSecurityGroups(r.Context(), domain.ListSecurityGroupsOptions{})
	if err != nil {
		return nil
	}
	for _, sg := range res.SecurityGroups {
		if sg.Name == name {
			full, err := srv.n.GetSecurityGroup(r.Context(), sg.ID)
			if err != nil {
				return nil
			}
			return &full
		}
	}
	return nil
}

func (srv *Server) findPublicIPByName(r *http.Request, name string) *domain.PublicIP {
	res, err := srv.n.ListPublicIPs(r.Context(), domain.ListPublicIPsOptions{})
	if err != nil {
		return nil
	}
	for _, ip := range res.PublicIPs {
		if ip.Name == name {
			ip := ip
			return &ip
		}
	}
	return nil
}

// ─── Wire-type converters ─────────────────────────────────────────────

func domainNetworkToAzure(n domain.Network, location *string) *armnetwork.VirtualNetwork {
	vnet := &armnetwork.VirtualNetwork{
		ID:       to.Ptr("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/virtualNetworks/" + n.Name),
		Name:     &n.Name,
		Location: location,
		Type:     to.Ptr("Microsoft.Network/virtualNetworks"),
		Properties: &armnetwork.VirtualNetworkPropertiesFormat{
			ProvisioningState: to.Ptr(armnetwork.ProvisioningStateSucceeded),
		},
	}
	if n.CIDR != "" {
		vnet.Properties.AddressSpace = &armnetwork.AddressSpace{
			AddressPrefixes: []*string{&n.CIDR},
		}
	}
	if len(n.Tags) > 0 {
		vnet.Tags = domainTagsToAzure(n.Tags)
	}
	return vnet
}

func domainSubnetToAzure(s domain.Subnet) *armnetwork.Subnet {
	sub := &armnetwork.Subnet{
		ID:   to.Ptr("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/virtualNetworks/" + s.NetworkID + "/subnets/" + s.Name),
		Name: &s.Name,
		Type: to.Ptr("Microsoft.Network/virtualNetworks/subnets"),
		Properties: &armnetwork.SubnetPropertiesFormat{
			AddressPrefix:     &s.CIDR,
			ProvisioningState: to.Ptr(armnetwork.ProvisioningStateSucceeded),
		},
	}
	return sub
}

func domainSGToAzure(sg domain.SecurityGroup, location *string) *armnetwork.SecurityGroup {
	azSG := &armnetwork.SecurityGroup{
		ID:       to.Ptr("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/networkSecurityGroups/" + sg.Name),
		Name:     &sg.Name,
		Location: location,
		Type:     to.Ptr("Microsoft.Network/networkSecurityGroups"),
		Properties: &armnetwork.SecurityGroupPropertiesFormat{
			ProvisioningState: to.Ptr(armnetwork.ProvisioningStateSucceeded),
		},
	}
	for i, r := range sg.Rules {
		priority := int32(100 + i*10)
		rule := domainRuleToAzure(r, fmt.Sprintf("rule-%d", i), priority)
		azSG.Properties.SecurityRules = append(azSG.Properties.SecurityRules, rule)
	}
	if len(sg.Tags) > 0 {
		azSG.Tags = domainTagsToAzure(sg.Tags)
	}
	return azSG
}

func domainRuleToAzure(r domain.SecurityGroupRule, name string, priority int32) *armnetwork.SecurityRule {
	dir := armnetwork.SecurityRuleDirectionInbound
	if r.Direction == domain.Outbound {
		dir = armnetwork.SecurityRuleDirectionOutbound
	}
	portRange := "*"
	if r.PortFrom != 0 {
		if r.PortTo != 0 && r.PortTo != r.PortFrom {
			portRange = fmt.Sprintf("%d-%d", r.PortFrom, r.PortTo)
		} else {
			portRange = fmt.Sprintf("%d", r.PortFrom)
		}
	}
	sourcePrefix := "*"
	if len(r.CIDRs) > 0 {
		sourcePrefix = r.CIDRs[0]
	}
	proto := armnetwork.SecurityRuleProtocolTCP
	switch r.Protocol {
	case "udp":
		proto = armnetwork.SecurityRuleProtocolUDP
	case "-1", "":
		proto = armnetwork.SecurityRuleProtocolAsterisk
	}
	return &armnetwork.SecurityRule{
		Name: &name,
		Properties: &armnetwork.SecurityRulePropertiesFormat{
			Access:                   to.Ptr(armnetwork.SecurityRuleAccessAllow),
			Direction:                to.Ptr(dir),
			Priority:                 &priority,
			Protocol:                 to.Ptr(proto),
			SourcePortRange:          to.Ptr("*"),
			DestinationPortRange:     to.Ptr(portRange),
			SourceAddressPrefix:      to.Ptr(sourcePrefix),
			DestinationAddressPrefix: to.Ptr("*"),
			ProvisioningState:        to.Ptr(armnetwork.ProvisioningStateSucceeded),
		},
	}
}

func azureRuleToDomain(rule *armnetwork.SecurityRule, priorityAsIndex int) domain.SecurityGroupRule {
	p := rule.Properties
	if p == nil {
		return domain.SecurityGroupRule{}
	}
	r := domain.SecurityGroupRule{Direction: domain.Inbound}
	if p.Direction != nil && *p.Direction == armnetwork.SecurityRuleDirectionOutbound {
		r.Direction = domain.Outbound
	}
	if p.Protocol != nil {
		switch *p.Protocol {
		case armnetwork.SecurityRuleProtocolTCP:
			r.Protocol = "tcp"
		case armnetwork.SecurityRuleProtocolUDP:
			r.Protocol = "udp"
		default:
			r.Protocol = "-1"
		}
	}
	if p.DestinationPortRange != nil && *p.DestinationPortRange != "*" {
		portStr := *p.DestinationPortRange
		if dash := strings.Index(portStr, "-"); dash >= 0 {
			fmt.Sscanf(portStr[:dash], "%d", &r.PortFrom)
			fmt.Sscanf(portStr[dash+1:], "%d", &r.PortTo)
		} else {
			fmt.Sscanf(portStr, "%d", &r.PortFrom)
			r.PortTo = r.PortFrom
		}
	}
	if p.SourceAddressPrefix != nil && *p.SourceAddressPrefix != "*" {
		r.CIDRs = []string{*p.SourceAddressPrefix}
	}
	return r
}

func domainIPToAzure(ip domain.PublicIP, location *string) *armnetwork.PublicIPAddress {
	static := armnetwork.IPAllocationMethodStatic
	return &armnetwork.PublicIPAddress{
		ID:       to.Ptr("/subscriptions/shim/resourceGroups/shim/providers/Microsoft.Network/publicIPAddresses/" + ip.Name),
		Name:     &ip.Name,
		Location: location,
		Type:     to.Ptr("Microsoft.Network/publicIPAddresses"),
		Properties: &armnetwork.PublicIPAddressPropertiesFormat{
			PublicIPAllocationMethod: to.Ptr(static),
			IPAddress:                &ip.Address,
			ProvisioningState:        to.Ptr(armnetwork.ProvisioningStateSucceeded),
		},
	}
}

// ─── Tag converters ───────────────────────────────────────────────────

func azureTagsToDomain(tags map[string]*string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]string, len(tags))
	for k, v := range tags {
		if v != nil {
			m[k] = *v
		}
	}
	return m
}

func domainTagsToAzure(tags map[string]string) map[string]*string {
	if len(tags) == 0 {
		return nil
	}
	m := make(map[string]*string, len(tags))
	for k, v := range tags {
		v := v
		m[k] = &v
	}
	return m
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

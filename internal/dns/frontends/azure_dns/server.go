// Package azure_dns is the Azure DNS + Azure Private DNS ARM REST
// frontend for shimanism's DNS service. It speaks the HTTP+JSON wire
// protocol that the `armdns` / `armprivatedns` SDKs and `az network`
// CLI drive, and translates each request into a call on the neutral
// `domain.DNS` interface.
//
// Per N17 this is a SINGLE frontend: the path tells us whether the
// caller is addressing `Microsoft.Network/dnsZones` (public) or
// `Microsoft.Network/privateDnsZones` (private). Dispatch lives at
// the path-prefix boundary; the domain layer carries `Visibility`
// uniformly below.
//
// Wire types come from `armdns` + `armprivatedns` directly per the
// reuse-over-reinvention rule (AGENTS.md decision #11).
package azure_dns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/privatedns/armprivatedns"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/dns/domain"
)

// Server is an Azure-DNS-shaped HTTP frontend dispatching to a
// domain.DNS backend. Optional fields turn it into a more complete
// Azure-cloud-control-plane proxy:
//
//   - `upstream` (when non-nil) forwards ARM paths the frontend
//     doesn't handle to a real ARM mock (sockerless in tests, real
//     ARM in prod) — the "ARM passthrough" mode from BUG-44.
//   - `metadataLoginURL` (when set) makes the frontend serve
//     `GET /metadata/endpoints` returning a cloud-environment JSON
//     document where `resourceManager` points at the shim itself
//     (derived from `r.Host`) and the auth + suffix endpoints point
//     at the configured upstream login URL. This is the BUG-46
//     primitive that lets `hashicorp/azurerm`'s
//     `metadata_host = "<shim>"` make the provider acquire Entra ID
//     tokens from sockerless while ARM calls flow through the shim.
type Server struct {
	d                domain.DNS
	upstream         http.Handler
	metadataLoginURL string
}

// Config carries optional Server configuration. SubscriptionID and
// ResourceGroup aren't carried here — the shim is identity-free per
// the stateless-shim rule; ARM calls already encode them in the URL.
type Config struct {
	// Passthrough forwards unmatched ARM paths (resource groups,
	// subscriptions, non-DNS Microsoft.* resources, Entra ID token
	// requests when MetadataLoginURL is unset, …) to the upstream
	// handler. Typically `httputil.NewSingleHostReverseProxy(...)`
	// pointing at sockerless's Azure ARM mock or real ARM.
	Passthrough http.Handler

	// MetadataLoginURL is the base URL for endpoints the shim does
	// **not** intercept (Entra ID `loginEndpoint`, graph, batch, …).
	// Typically the sockerless Azure ARM URL. When empty, the
	// metadata endpoint is not served (the path falls through to
	// Passthrough or 404).
	//
	// The metadata response always points `resourceManager` at the
	// shim itself (derived from the request's Host header) so ARM
	// calls flow through the frontend's DNS dispatch.
	MetadataLoginURL string
}

// New returns a frontend bound to the given backend. Unmatched ARM
// paths return 404 ("no route matches …").
func New(d domain.DNS) *Server { return &Server{d: d} }

// NewWithPassthrough wires an upstream ARM handler that the frontend
// forwards unmatched paths to.
func NewWithPassthrough(d domain.DNS, upstream http.Handler) *Server {
	return &Server{d: d, upstream: upstream}
}

// NewWithConfig is the most general constructor; honors every
// Config field.
func NewWithConfig(d domain.DNS, c Config) *Server {
	return &Server{
		d:                d,
		upstream:         c.Passthrough,
		metadataLoginURL: c.MetadataLoginURL,
	}
}

// Handler wraps Server with the Azure bearer verifier middleware.
// SHIMANISM_TEST_UNAUTHENTICATED_AZURE=1 short-circuits verification.
func Handler(d domain.DNS) http.Handler {
	return HandlerWithPassthrough(d, nil)
}

// HandlerWithPassthrough is the passthrough variant — same middleware,
// non-DNS ARM paths forwarded to the upstream.
func HandlerWithPassthrough(d domain.DNS, upstream http.Handler) http.Handler {
	return HandlerWithConfig(d, Config{Passthrough: upstream})
}

// HandlerWithConfig is the verifier-wrapped form of NewWithConfig.
//
// The Azure cloud-metadata endpoint at `/metadata/endpoints` is a
// **public discovery URL** in real Azure: clients hit it without
// any bearer token to discover where to acquire one. We mirror that
// — the metadata route bypasses the bearer middleware. Every other
// path goes through the verifier.
func HandlerWithConfig(d domain.DNS, c Config) http.Handler {
	server := NewWithConfig(d, c)
	if c.MetadataLoginURL == "" {
		// No metadata endpoint configured; bearer-wrap the whole server.
		return wrapWithBearer(server)
	}
	bearerWrapped := wrapWithBearer(server)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/metadata/endpoints" {
			// Public discovery endpoint — answer directly, no bearer.
			server.ServeHTTP(w, r)
			return
		}
		bearerWrapped.ServeHTTP(w, r)
	})
}

func wrapWithBearer(h http.Handler) http.Handler {
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://management.azure.com/"))(h)
}

// ARM path shape the frontend handles directly:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/
//	  <dnsZones | privateDnsZones>/{zone}[/<recordType>/<recordName>] [?api-version=...]
//
// Plus list shapes:
//
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/dnsZones
//	/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/dnsZones/{zone}/recordsets
//
// Every other ARM path (resource groups, subscriptions, other
// Microsoft.Network resources, …) is forwarded to `srv.upstream`
// when it's set, otherwise returns 404.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Azure cloud metadata endpoint. azurerm sets `metadata_host =
	// <shim>` and fetches /metadata/endpoints to discover the cloud's
	// service URLs. We answer in-band when MetadataLoginURL is set
	// (BUG-46), pointing resourceManager at the shim itself so ARM
	// calls flow through the local DNS dispatch + passthrough, while
	// everything else (login, graph, batch, …) points at the
	// upstream login URL.
	if r.Method == http.MethodGet && r.URL.Path == "/metadata/endpoints" && srv.metadataLoginURL != "" {
		srv.serveMetadata(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/")
	segs := strings.Split(path, "/")
	// Anything not under /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/<dnsZones|privateDnsZones>
	// goes to the upstream when configured.
	if !isLocalDNSPath(segs) {
		srv.passthroughOr404(w, r)
		return
	}
	kind := segs[6]
	var visibility domain.ZoneVisibility
	switch kind {
	case "dnsZones":
		visibility = domain.VisibilityPublic
	case "privateDnsZones":
		visibility = domain.VisibilityPrivate
	default:
		srv.passthroughOr404(w, r)
		return
	}
	tail := segs[7:]
	switch len(tail) {
	case 0:
		// /providers/Microsoft.Network/<kind>
		if r.Method == http.MethodGet {
			srv.listZones(w, r, visibility)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	case 1:
		// /<kind>/{zone}
		zone := tail[0]
		switch r.Method {
		case http.MethodPut:
			srv.putZone(w, r, zone, visibility)
		case http.MethodGet:
			srv.getZone(w, r, zone, visibility)
		case http.MethodDelete:
			srv.deleteZone(w, r, zone, visibility)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
	case 2:
		// /<kind>/{zone}/recordsets — list all records in a zone (public DNS only in spec).
		zone := tail[0]
		if tail[1] == "recordsets" || tail[1] == "all" {
			if r.Method == http.MethodGet {
				srv.listRecordSets(w, r, zone, visibility, "")
				return
			}
		}
		// /<kind>/{zone}/<recordType> — list records of a type (private DNS).
		rtype := tail[1]
		if r.Method == http.MethodGet {
			srv.listRecordSets(w, r, zone, visibility, rtype)
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
	case 3:
		// /<kind>/{zone}/<recordType>/<recordName>
		zone := tail[0]
		rtype := tail[1]
		rname := tail[2]
		switch r.Method {
		case http.MethodPut:
			srv.putRecordSet(w, r, zone, visibility, rtype, rname)
		case http.MethodGet:
			srv.getRecordSet(w, r, zone, visibility, rtype, rname)
		case http.MethodDelete:
			srv.deleteRecordSet(w, r, zone, visibility, rtype, rname)
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		}
	default:
		writeError(w, http.StatusNotFound, "NotFound", "unmatched ARM path tail "+r.URL.Path)
	}
}

// isLocalDNSPath reports whether the segmented path falls under the
// DNS-specific ARM provider this frontend handles directly.
func isLocalDNSPath(segs []string) bool {
	if len(segs) < 7 {
		return false
	}
	if segs[0] != "subscriptions" || segs[2] != "resourceGroups" ||
		segs[4] != "providers" || segs[5] != "Microsoft.Network" {
		return false
	}
	return segs[6] == "dnsZones" || segs[6] == "privateDnsZones"
}

// passthroughOr404 forwards to the configured upstream when present;
// otherwise emits the Azure-shaped 404 error envelope.
func (srv *Server) passthroughOr404(w http.ResponseWriter, r *http.Request) {
	if srv.upstream == nil {
		writeError(w, http.StatusNotFound, "NotFound", "no route matches "+r.URL.Path)
		return
	}
	srv.upstream.ServeHTTP(w, r)
}

// serveMetadata returns the Azure cloud-environment JSON document
// the azurerm provider fetches via `metadata_host = "<shim>"`.
// `resourceManager` points at the shim itself (so ARM calls flow
// through this frontend's local-DNS + passthrough dispatch);
// `authentication.loginEndpoint` and the other service endpoints
// point at the configured `MetadataLoginURL` (sockerless's Azure
// mock in tests, real Azure in prod).
//
// Shape mirrors what real Azure returns at
// `https://management.azure.com/metadata/endpoints?api-version=...`
// and what sockerless's `simulators/azure/metadata.go` emits. Two
// api-version flavours: `2022-09-01` returns a single object
// (azurerm v3/v4); older versions return a singleton array.
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

// --------------- Zones ---------------

func (srv *Server) putZone(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility) {
	body, err := readJSONMap(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	opt := domain.CreateZoneOptions{Visibility: vis, Tags: extractTags(body)}
	// Pass full FQDN (with trailing dot) to the domain — the backend
	// strips it when calling Azure if needed.
	z, err := srv.d.CreateZone(r.Context(), zone+".", opt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if vis == domain.VisibilityPrivate {
		writeJSON(w, http.StatusOK, encodePrivateZone(z, zone, r))
		return
	}
	writeJSON(w, http.StatusOK, encodePublicZone(z, zone, r))
}

func (srv *Server) getZone(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility) {
	z, err := srv.d.GetZone(r.Context(), zone+".")
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if vis == domain.VisibilityPrivate || z.Visibility == domain.VisibilityPrivate {
		writeJSON(w, http.StatusOK, encodePrivateZone(z, zone, r))
		return
	}
	writeJSON(w, http.StatusOK, encodePublicZone(z, zone, r))
}

func (srv *Server) deleteZone(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility) {
	if err := srv.d.DeleteZone(r.Context(), zone+".", false); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) listZones(w http.ResponseWriter, r *http.Request, vis domain.ZoneVisibility) {
	res, err := srv.d.ListZones(r.Context(), domain.ListZonesOptions{VisibilityFilter: vis})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	value := make([]any, 0, len(res.Zones))
	for _, z := range res.Zones {
		bare := strings.TrimSuffix(z.Name, ".")
		if vis == domain.VisibilityPrivate {
			value = append(value, encodePrivateZone(z, bare, r))
		} else {
			value = append(value, encodePublicZone(z, bare, r))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": value})
}

// --------------- Record sets ---------------

func (srv *Server) putRecordSet(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility, rtype, rname string) {
	body, err := readJSONMap(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	rs := decodeRecordSet(body, zone, rname, rtype)
	if err := srv.d.PutRecordSet(r.Context(), zone+".", rs); err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, encodeRecordSet(rs, zone, rname, rtype, vis, r))
}

func (srv *Server) getRecordSet(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility, rtype, rname string) {
	fqdn := recordFQDN(zone, rname)
	rs, err := srv.d.GetRecordSet(r.Context(), zone+".", fqdn, domain.RecordType(rtype))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, encodeRecordSet(rs, zone, rname, rtype, vis, r))
}

func (srv *Server) deleteRecordSet(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility, rtype, rname string) {
	fqdn := recordFQDN(zone, rname)
	if err := srv.d.DeleteRecordSet(r.Context(), zone+".", fqdn, domain.RecordType(rtype)); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) listRecordSets(w http.ResponseWriter, r *http.Request, zone string, vis domain.ZoneVisibility, rtype string) {
	opt := domain.ListRecordSetsOptions{}
	if rtype != "" {
		opt.TypeFilter = domain.RecordType(rtype)
	}
	res, err := srv.d.ListRecordSets(r.Context(), zone+".", opt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	value := make([]any, 0, len(res.RecordSets))
	for _, rs := range res.RecordSets {
		rname := relativeName(zone, rs.Name)
		value = append(value, encodeRecordSet(rs, zone, rname, string(rs.Type), vis, r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"value": value})
}

// --------------- Translation ---------------

func encodePublicZone(z domain.Zone, name string, r *http.Request) *armdns.Zone {
	sub, rg := subRG(r)
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnszones/%s", sub, rg, name)
	out := &armdns.Zone{
		ID:       to.Ptr(id),
		Name:     to.Ptr(name),
		Type:     to.Ptr("Microsoft.Network/dnszones"),
		Location: to.Ptr("global"),
		Tags:     tagsToAzurePub(z.Tags),
	}
	out.Properties = &armdns.ZoneProperties{
		NumberOfRecordSets:    to.Ptr(int64(0)),
		MaxNumberOfRecordSets: to.Ptr(int64(10000)),
	}
	for _, ns := range z.NameServers {
		nsCopy := ns
		out.Properties.NameServers = append(out.Properties.NameServers, &nsCopy)
	}
	return out
}

func encodePrivateZone(z domain.Zone, name string, r *http.Request) *armprivatedns.PrivateZone {
	sub, rg := subRG(r)
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/privateDnsZones/%s", sub, rg, name)
	return &armprivatedns.PrivateZone{
		ID:       to.Ptr(id),
		Name:     to.Ptr(name),
		Type:     to.Ptr("Microsoft.Network/privateDnsZones"),
		Location: to.Ptr("global"),
		Tags:     tagsToAzurePriv(z.Tags),
		Properties: &armprivatedns.PrivateZoneProperties{
			ProvisioningState:     to.Ptr(armprivatedns.ProvisioningStateSucceeded),
			NumberOfRecordSets:    to.Ptr(int64(0)),
			MaxNumberOfRecordSets: to.Ptr(int64(25000)),
		},
	}
}

func decodeRecordSet(body map[string]any, zone, rname, rtype string) domain.RecordSet {
	props, _ := body["properties"].(map[string]any)
	rs := domain.RecordSet{
		Name: recordFQDN(zone, rname),
		Type: domain.RecordType(rtype),
	}
	if props != nil {
		if ttl, ok := props["TTL"].(float64); ok {
			rs.TTL = int(ttl)
		}
		switch domain.RecordType(rtype) {
		case domain.RecordTypeA:
			for _, raw := range arrField(props, "ARecords") {
				if m, ok := raw.(map[string]any); ok {
					if v, ok := m["ipv4Address"].(string); ok {
						rs.Records = append(rs.Records, v)
					}
				}
			}
		case domain.RecordTypeAAAA:
			for _, raw := range arrField(props, "AAAARecords") {
				if m, ok := raw.(map[string]any); ok {
					if v, ok := m["ipv6Address"].(string); ok {
						rs.Records = append(rs.Records, v)
					}
				}
			}
		case domain.RecordTypeCNAME:
			if m, ok := props["CNAMERecord"].(map[string]any); ok {
				if v, ok := m["cname"].(string); ok {
					rs.Records = append(rs.Records, v)
				}
			}
		case domain.RecordTypeMX:
			for _, raw := range arrField(props, "MXRecords") {
				if m, ok := raw.(map[string]any); ok {
					pref, _ := m["preference"].(float64)
					exch, _ := m["exchange"].(string)
					rs.Records = append(rs.Records, fmt.Sprintf("%d %s", int(pref), exch))
				}
			}
		case domain.RecordTypeNS:
			for _, raw := range arrField(props, "NSRecords") {
				if m, ok := raw.(map[string]any); ok {
					if v, ok := m["nsdname"].(string); ok {
						rs.Records = append(rs.Records, v)
					}
				}
			}
		case domain.RecordTypeSRV:
			for _, raw := range arrField(props, "SRVRecords") {
				if m, ok := raw.(map[string]any); ok {
					pri, _ := m["priority"].(float64)
					weight, _ := m["weight"].(float64)
					port, _ := m["port"].(float64)
					tgt, _ := m["target"].(string)
					rs.Records = append(rs.Records, fmt.Sprintf("%d %d %d %s", int(pri), int(weight), int(port), tgt))
				}
			}
		case domain.RecordTypeTXT:
			for _, raw := range arrField(props, "TXTRecords") {
				if m, ok := raw.(map[string]any); ok {
					if vals, ok := m["value"].([]any); ok {
						var joined strings.Builder
						for _, vv := range vals {
							if s, ok := vv.(string); ok {
								joined.WriteString(s)
							}
						}
						rs.Records = append(rs.Records, joined.String())
					}
				}
			}
		}
	}
	return rs
}

func encodeRecordSet(rs domain.RecordSet, zone, rname, rtype string, vis domain.ZoneVisibility, r *http.Request) map[string]any {
	sub, rg := subRG(r)
	kind := "dnszones"
	if vis == domain.VisibilityPrivate {
		kind = "privateDnsZones"
	}
	id := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/%s/%s/%s/%s", sub, rg, kind, zone, rtype, rname)
	props := map[string]any{"TTL": rs.TTL, "fqdn": recordFQDN(zone, rname)}
	switch domain.RecordType(rtype) {
	case domain.RecordTypeA:
		var arr []map[string]any
		for _, v := range rs.Records {
			arr = append(arr, map[string]any{"ipv4Address": v})
		}
		props["ARecords"] = arr
	case domain.RecordTypeAAAA:
		var arr []map[string]any
		for _, v := range rs.Records {
			arr = append(arr, map[string]any{"ipv6Address": v})
		}
		props["AAAARecords"] = arr
	case domain.RecordTypeCNAME:
		if len(rs.Records) > 0 {
			props["CNAMERecord"] = map[string]any{"cname": strings.TrimSuffix(rs.Records[0], ".")}
		}
	case domain.RecordTypeMX:
		var arr []map[string]any
		for _, v := range rs.Records {
			parts := strings.Fields(v)
			if len(parts) != 2 {
				continue
			}
			pref, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			arr = append(arr, map[string]any{"preference": pref, "exchange": strings.TrimSuffix(parts[1], ".")})
		}
		props["MXRecords"] = arr
	case domain.RecordTypeNS:
		var arr []map[string]any
		for _, v := range rs.Records {
			arr = append(arr, map[string]any{"nsdname": strings.TrimSuffix(v, ".")})
		}
		props["NSRecords"] = arr
	case domain.RecordTypeSRV:
		var arr []map[string]any
		for _, v := range rs.Records {
			parts := strings.Fields(v)
			if len(parts) != 4 {
				continue
			}
			pri, _ := strconv.Atoi(parts[0])
			weight, _ := strconv.Atoi(parts[1])
			port, _ := strconv.Atoi(parts[2])
			arr = append(arr, map[string]any{
				"priority": pri, "weight": weight, "port": port,
				"target": strings.TrimSuffix(parts[3], "."),
			})
		}
		props["SRVRecords"] = arr
	case domain.RecordTypeTXT:
		var arr []map[string]any
		for _, v := range rs.Records {
			arr = append(arr, map[string]any{"value": []string{v}})
		}
		props["TXTRecords"] = arr
	}
	return map[string]any{
		"id":         id,
		"name":       rname,
		"type":       fmt.Sprintf("Microsoft.Network/%s/%s", kind, rtype),
		"etag":       fmt.Sprintf("\"%d\"", time.Now().UnixNano()),
		"properties": props,
	}
}

func recordFQDN(zone, rname string) string {
	if rname == "@" || rname == "" {
		return canonicalize(zone)
	}
	return canonicalize(rname + "." + zone)
}

func relativeName(zone, fqdn string) string {
	bare := strings.TrimSuffix(fqdn, ".")
	zoneBare := strings.TrimSuffix(zone, ".")
	if bare == zoneBare {
		return "@"
	}
	if suffix := "." + zoneBare; strings.HasSuffix(bare, suffix) {
		return strings.TrimSuffix(bare, suffix)
	}
	return bare
}

func canonicalize(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if !strings.HasSuffix(n, ".") {
		n += "."
	}
	return n
}

func subRG(r *http.Request) (sub, rg string) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	segs := strings.Split(path, "/")
	if len(segs) >= 4 {
		return segs[1], segs[3]
	}
	return "", ""
}

func extractTags(body map[string]any) map[string]string {
	tags, _ := body["tags"].(map[string]any)
	if tags == nil {
		return nil
	}
	out := make(map[string]string, len(tags))
	for k, v := range tags {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func tagsToAzurePub(in map[string]string) map[string]*string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]*string, len(in))
	for k, v := range in {
		val := v
		out[k] = &val
	}
	return out
}

func tagsToAzurePriv(in map[string]string) map[string]*string { return tagsToAzurePub(in) }

func readJSONMap(r *http.Request) (map[string]any, error) {
	var body map[string]any
	if r.ContentLength == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	return body, nil
}

func arrField(m map[string]any, key string) []any {
	v, _ := m[key].([]any)
	return v
}

// --------------- Error envelope ---------------

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{
		"error": map[string]any{"code": code, "message": message},
	}
	_ = json.NewEncoder(w).Encode(body)
}

func writeDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServerError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchZone, domain.KindNoSuchRecordSet:
		writeError(w, http.StatusNotFound, "NotFound", de.Message)
	case domain.KindZoneAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Message)
	case domain.KindZoneNotEmpty:
		writeError(w, http.StatusBadRequest, "PreconditionFailed", de.Message)
	case domain.KindInvalidArgument, domain.KindUnsupported:
		writeError(w, http.StatusBadRequest, "BadRequest", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerError", de.Message)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Package mockaad serves a minimal Microsoft-Entra-compatible HTTP
// surface for tests that need an Azure AD authority to exchange a
// (synthetic) client_secret for a bearer token. Real Entra is
// out-of-scope: the shim doesn't shim Entra in production. This
// mock only exists so `hashicorp/azurerm` Terraform tests can run
// end-to-end against the shim without an actual Azure AD tenant.
//
// What it serves (under any tenant id prefix):
//
//	GET  /metadata/endpoints?api-version=...
//	  Cloud-metadata document with `authentication.loginEndpoint`
//	  pointing back at this mock + `resourceManager` pointing at the
//	  configured ARM endpoint. azurerm fetches this when
//	  `metadata_host` is set.
//	GET  /{tenant}/.well-known/openid-configuration
//	  Standard OIDC discovery pointing to /{tenant}/oauth2/v2.0/token.
//	POST /{tenant}/oauth2/v2.0/token
//	  Accepts any client_credentials grant. Returns an HS256-signed
//	  JWT minted with internal/azurebearer.TestJWT (audience derived
//	  from the request's `scope` form-field, or
//	  "https://management.azure.com/" by default).
//
// The mock is intentionally permissive: it ignores client_id +
// client_secret entirely. Tests that exercise Entra-side rejection
// belong elsewhere.
package mockaad

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/azurebearer"
)

// TestKey is the HS256 signing key for tokens minted by the mock.
// Matches `internal/azurebearer.TestKey` so the shim's verifier
// accepts tokens this mock issues.
var TestKey = []byte("test-key-do-not-use-in-prod")

// Options configures a Server.
type Options struct {
	// SelfURL is the mock's own base URL. Populated by NewServer
	// from the wrapping httptest.Server; tests typically don't set
	// this directly.
	SelfURL string

	// ResourceManagerURL is returned in the cloud-metadata document
	// as `resourceManager`. Set this to the shim's ARM frontend URL
	// so azurerm routes ARM calls there.
	ResourceManagerURL string
}

// Server is a minimal mock Microsoft Entra authority.
type Server struct {
	mux  *http.ServeMux
	opts *Options
}

// NewServer returns a mock-AAD HTTP handler. `opts.SelfURL` is
// populated lazily by SetSelfURL once the wrapping httptest server
// reveals its address.
func NewServer(opts *Options) *Server {
	if opts == nil {
		opts = &Options{}
	}
	s := &Server{mux: http.NewServeMux(), opts: opts}
	s.mux.HandleFunc("/metadata/endpoints", s.handleMetadata)
	s.mux.HandleFunc("/", s.handleAny)
	return s
}

// SetSelfURL records the mock's externally-reachable base URL.
// Call this after wrapping with httptest.NewServer so the issued
// tokens and metadata document can reference the mock by URL.
func (s *Server) SetSelfURL(url string) {
	s.opts.SelfURL = url
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// handleMetadata serves the cloud-metadata document. azurerm's
// `metadata_host` config makes the provider fetch this; the fields
// it reads to configure its own AAD + ARM endpoints are
// `name` (the environment selector), `authentication.loginEndpoint`,
// `authentication.audiences`, and `resourceManager`.
//
// **Shape note.** First attempt returned a single object — azurerm
// said `name was nil`. Second attempt wrapped it in an array —
// azurerm said `cannot unmarshal array into Go value of type
// metaDataResponse`. So the expected shape IS a single object,
// but the `name` field has to be populated (azurerm matches it
// against its `environment` setting).
func (s *Server) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	doc := map[string]any{
		// Selector field azurerm matches against its `environment`.
		"name": "AzureCloud",

		// Endpoints azurerm uses directly. `resourceManager` →
		// shim's ARM frontend; `authentication.loginEndpoint` →
		// this mock for token exchange.
		"portal":         s.opts.SelfURL,
		"portalEndpoint": s.opts.SelfURL,
		"authentication": map[string]any{
			"loginEndpoint":    s.opts.SelfURL + "/",
			"audiences":        []string{"https://management.core.windows.net/", "https://management.azure.com/"},
			"tenant":           "common",
			"identityProvider": "AAD",
		},
		"resourceManager": s.opts.ResourceManagerURL,
		// Point Graph at this mock too — azurerm calls Graph to
		// discover the authenticated service principal's object ID
		// post-token-exchange. Real Graph rejects HS256 tokens; the
		// mock serves a synthetic /v1.0/me + /v1.0/servicePrincipals
		// response under handleAny.
		"graphAudience":            s.opts.SelfURL + "/",
		"graph":                    s.opts.SelfURL + "/",
		"graphEndpoint":            s.opts.SelfURL + "/",
		"microsoftGraphResourceId": s.opts.SelfURL + "/",

		// Mirror-the-real-metadata fields. azurerm reads several at
		// init-time (any of them being nil/missing has been seen to
		// trip the metadata parser); we include the canonical Azure
		// public-cloud values rather than zeroes so the parser
		// doesn't reject the document.
		"activeDirectoryDataLakeResourceId":     "https://datalake.azure.net/",
		"appInsightsResourceId":                 "https://api.applicationinsights.io",
		"appInsightsTelemetryChannelResourceId": "https://dc.applicationinsights.azure.com/v2/track",
		"batch":                                 "https://batch.core.windows.net/",
		"galleryEndpoint":                       "https://gallery.azure.com/",
		"logAnalyticsResourceId":                "https://api.loganalytics.io",
		"managedHsmResourceId":                  "https://managedhsm.azure.net",
		"media":                                 "https://rest.media.azure.net",
		"mediaResourceId":                       "https://rest.media.azure.net",
		"ossrDbmsResourceId":                    "https://ossrdbms-aad.database.windows.net",
		"sqlManagement":                         "https://management.core.windows.net:8443/",
		"synapseAnalyticsResourceId":            "https://dev.azuresynapse.net",
		"vmImageAliasDoc":                       "https://raw.githubusercontent.com/Azure/azure-rest-api-specs/master/arm-compute/quickstart-templates/aliases.json",

		// DNS suffixes — azurerm uses these to build derived URLs
		// (e.g. {account}.blob.core.windows.net). The synthetic
		// StorageAccount response from the ARM frontend overrides
		// the blob suffix via PrimaryEndpoints.Blob, but other
		// services without explicit endpoint fields fall back to
		// these suffixes.
		"acrLoginServer":             "azurecr.io",
		"keyVaultDns":                "vault.azure.net",
		"resourceManagerVMDNSSuffix": "cloudapp.azure.com",
		"sqlServerHostname":          ".database.windows.net",
		"storageEndpointSuffix":      "core.windows.net",
		"suffixes": map[string]any{
			"storage":        "core.windows.net",
			"keyVaultDns":    "vault.azure.net",
			"acrLoginServer": "azurecr.io",
		},
	}
	writeJSON(w, http.StatusOK, doc)
}

// handleAny dispatches OIDC discovery + token endpoints under any
// tenant prefix, plus the Microsoft Graph endpoints azurerm calls to
// discover the authenticated service principal's object ID.
func (s *Server) handleAny(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/.well-known/openid-configuration"):
		s.handleOpenIDConfig(w, r)
	case strings.HasSuffix(path, "/oauth2/v2.0/token") || strings.HasSuffix(path, "/oauth2/token"):
		s.handleToken(w, r)
	case strings.HasPrefix(path, "/v1.0/me"), strings.HasPrefix(path, "/v1.0/servicePrincipals"), strings.HasPrefix(path, "/v1.0/applications"):
		s.handleGraph(w, r)
	default:
		http.NotFound(w, r)
	}
}

// handleGraph returns a synthetic Microsoft Graph response. azurerm
// calls GET /v1.0/me (or /v1.0/servicePrincipals(appId='{client_id}'))
// post-token-exchange to discover the authenticated service
// principal's object ID, which it uses internally for some resource
// lifecycle operations.
//
// The real Graph endpoint rejects HS256 tokens with "InvalidAuthenticationToken:
// Signing key is invalid" — so the mock has to take this over too.
// We return a fixed object ID (all-zero UUID) regardless of what's
// being requested. The shim doesn't model service principals, and no
// downstream operation actually uses the ID for state-of-record.
func (s *Server) handleGraph(w http.ResponseWriter, _ *http.Request) {
	syntheticID := "00000000-0000-0000-0000-000000000000"
	doc := map[string]any{
		"id":                   syntheticID,
		"appId":                syntheticID,
		"displayName":          "shim-test-principal",
		"servicePrincipalType": "Application",
		"value": []map[string]any{
			{
				"id":                   syntheticID,
				"appId":                syntheticID,
				"displayName":          "shim-test-principal",
				"servicePrincipalType": "Application",
			},
		},
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleOpenIDConfig(w http.ResponseWriter, r *http.Request) {
	tenant := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/.well-known/openid-configuration")
	doc := map[string]any{
		"issuer":                                s.opts.SelfURL + "/" + tenant + "/",
		"token_endpoint":                        s.opts.SelfURL + "/" + tenant + "/oauth2/v2.0/token",
		"authorization_endpoint":                s.opts.SelfURL + "/" + tenant + "/oauth2/v2.0/authorize",
		"jwks_uri":                              s.opts.SelfURL + "/" + tenant + "/discovery/v2.0/keys",
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
		"response_types_supported":              []string{"code", "id_token", "token"},
		"grant_types_supported":                 []string{"client_credentials", "authorization_code", "refresh_token"},
		"subject_types_supported":               []string{"pairwise"},
		"id_token_signing_alg_values_supported": []string{"RS256", "HS256"},
	}
	writeJSON(w, http.StatusOK, doc)
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request", "error_description": err.Error()})
		return
	}
	// Derive aud from the requested scope so per-service verifiers
	// see the audience their config expects. azurerm sends a
	// distinct token per resource; the resource URI is encoded as
	// `<resource>/.default` in the scope form-field.
	//
	// Verifier-side configurations the shim wires up today:
	//   - ARM:     "https://management.azure.com/" (trailing slash)
	//   - KV data: "https://vault.azure.net"       (no trailing slash)
	// azurerm asks for them as:
	//   - ARM scope: "https://management.azure.com//.default" (extra /
	//                because the shim's resourceManager URL ends in /)
	//   - KV scope:  "https://vault.azure.net/.default"
	// Stripping "/.default" then normalising trailing slashes
	// produces the form the verifier expects.
	scope := r.PostForm.Get("scope")
	if scope == "" {
		scope = r.PostForm.Get("resource")
	}
	audience := audienceFromScope(scope)
	token := azurebearer.TestJWT(TestKey, s.opts.SelfURL+"/", audience, time.Hour)
	resp := map[string]any{
		"token_type":     "Bearer",
		"access_token":   token,
		"expires_in":     3600,
		"ext_expires_in": 3600,
	}
	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// audienceFromScope maps an OAuth2 scope form-field to the JWT `aud`
// claim the shim's per-service azurebearer verifier expects.
//
// Strips a trailing `/.default` (the canonical Azure scope suffix
// for client-credentials), then normalises:
//   - "https://management.azure.com/" → keep the trailing slash
//     (the ARM verifier is configured with the slash).
//   - "https://vault.azure.net" → no trailing slash (the KV verifier
//     is configured without).
//
// Defaults to "https://management.azure.com/" when scope is empty
// (legacy callers that don't set scope at all).
func audienceFromScope(scope string) string {
	if scope == "" {
		return "https://management.azure.com/"
	}
	aud := scope
	aud = strings.TrimSuffix(aud, "/.default")
	aud = strings.TrimSuffix(aud, "//.default") // azurerm sometimes joins resource + scope with double-slash
	// ARM's verifier needs the trailing slash; KV's doesn't.
	// Recognise the specific public-cloud hosts the shim wires.
	switch {
	case strings.HasPrefix(aud, "https://management.azure.com"):
		return "https://management.azure.com/"
	case strings.HasPrefix(aud, "https://vault.azure.net"):
		return "https://vault.azure.net"
	}
	return aud
}

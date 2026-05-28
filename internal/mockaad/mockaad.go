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
// The response shape is an array of environment objects — the real
// Azure metadata service returns multiple clouds (AzureCloud,
// AzureChinaCloud, etc.); azurerm picks the one whose `name`
// matches its `environment` setting (default "public").
func (s *Server) handleMetadata(w http.ResponseWriter, _ *http.Request) {
	env := map[string]any{
		"name":                              "AzureCloud",
		"portal":                            s.opts.SelfURL,
		"authentication":                    map[string]any{"loginEndpoint": s.opts.SelfURL + "/", "audiences": []string{"https://management.core.windows.net/", "https://management.azure.com/"}, "tenant": "common", "identityProvider": "AAD"},
		"resourceManager":                   s.opts.ResourceManagerURL,
		"graphAudience":                     "https://graph.windows.net/",
		"graph":                             "https://graph.windows.net/",
		"activeDirectoryDataLakeResourceId": "https://datalake.azure.net/",
		"batch":                             "https://batch.core.windows.net/",
		"media":                             "https://rest.media.azure.net",
		"sqlManagement":                     "https://management.core.windows.net:8443/",
		"vmImageAliasDoc":                   "https://raw.githubusercontent.com/Azure/azure-rest-api-specs/master/arm-compute/quickstart-templates/aliases.json",
		"resourceManagerVMDNSSuffix":        "cloudapp.azure.com",
		"acrLoginServer":                    "azurecr.io",
		"sqlServerHostname":                 ".database.windows.net",
		"galleryEndpoint":                   "https://gallery.azure.com/",
		"keyVaultDns":                       "vault.azure.net",
		"storageEndpointSuffix":             "core.windows.net",
		"suffixes":                          map[string]any{"storage": "core.windows.net", "keyVaultDns": "vault.azure.net", "acrLoginServer": "azurecr.io"},
	}
	writeJSON(w, http.StatusOK, []any{env})
}

// handleAny dispatches OIDC discovery + token endpoints under any
// tenant prefix.
func (s *Server) handleAny(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/.well-known/openid-configuration"):
		s.handleOpenIDConfig(w, r)
	case strings.HasSuffix(path, "/oauth2/v2.0/token") || strings.HasSuffix(path, "/oauth2/token"):
		s.handleToken(w, r)
	default:
		http.NotFound(w, r)
	}
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
	scope := r.PostForm.Get("scope")
	if scope == "" {
		scope = r.PostForm.Get("resource")
	}
	audience := "https://management.azure.com/"
	if scope != "" {
		// Azure scopes look like "https://management.azure.com/.default".
		// Strip the trailing "/.default" suffix if present.
		audience = strings.TrimSuffix(scope, "/.default")
	}
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

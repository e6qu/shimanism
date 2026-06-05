// Package azure_acr is the Azure Container Registry frontend for
// shimanism's container-registry service. ACR has an ARM management
// surface for the registry host itself, plus an ACR/OCI data plane for
// repositories and images:
//
//   - /subscriptions/.../providers/Microsoft.ContainerRegistry/registries/{name}
//     returns the configured registry host shape.
//   - /oauth2/exchange and /oauth2/token implement ACR's Entra-token to
//     registry-token exchange (N31).
//   - /v2/ mounts the shared OCI Distribution router.
//   - /acr/v1/ exposes ACR repository and manifest listing.
package azure_acr

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/internal/registry/ocidistribution"
)

const (
	testKey            = "test-key-do-not-use-in-prod"
	defaultARMResource = "https://management.azure.com/"
)

// Config carries optional Azure ACR frontend configuration.
type Config struct {
	Passthrough      http.Handler
	MetadataLoginURL string
	BearerOptions    azurebearer.Options
}

// Server routes the ACR ARM, auth, OCI, and acr/v1 data-plane surfaces.
type Server struct {
	reg         domain.Registry
	armVerifier *azurebearer.Verifier
	oci         *ocidistribution.Router
	tokenKey    []byte
	upstream    http.Handler
	loginURL    string
}

// New returns an ACR frontend bound to the given backend.
func New(reg domain.Registry) *Server { return NewWithConfig(reg, Config{}) }

// Handler returns the frontend as an http.Handler.
func Handler(reg domain.Registry) http.Handler { return New(reg) }

// NewWithConfig returns an ACR frontend bound to the given backend.
func NewWithConfig(reg domain.Registry, cfg Config) *Server {
	opts := cfg.BearerOptions
	if opts.JWKS == nil && opts.JWKSURL == "" && len(opts.TestKey) == 0 {
		opts.TestKey = []byte(testKey)
	}
	if opts.Audience == "" {
		opts.Audience = defaultARMResource
	}
	return &Server{
		reg:         reg,
		armVerifier: azurebearer.New(opts),
		oci:         ocidistribution.New(reg),
		tokenKey:    []byte(testKey),
		upstream:    cfg.Passthrough,
		loginURL:    cfg.MetadataLoginURL,
	}
}

func HandlerWithConfig(reg domain.Registry, cfg Config) http.Handler { return NewWithConfig(reg, cfg) }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/metadata/endpoints" && s.loginURL != "":
		s.metadata(w, r)
	case r.URL.Path == "/oauth2/exchange":
		s.exchange(w, r)
	case r.URL.Path == "/oauth2/token":
		s.token(w, r)
	case strings.HasPrefix(r.URL.Path, "/v2/") || r.URL.Path == "/v2":
		if !s.validAccess(r) {
			s.challenge(w, r)
			return
		}
		s.oci.ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, "/acr/v1/"):
		if !s.validAccess(r) {
			writeACRError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
			return
		}
		s.serveACRv1(w, r)
	case strings.Contains(strings.ToLower(path), "/providers/microsoft.containerregistry/registries"):
		if err := s.armVerifier.Verify(r); err != nil {
			writeARMError(w, http.StatusUnauthorized, "InvalidAuthenticationToken", err.Error())
			return
		}
		s.serveARM(w, r)
	default:
		if s.upstream != nil {
			s.upstream.ServeHTTP(w, r)
			return
		}
		writeARMError(w, http.StatusNotFound, "NotFound", "path not matched: "+r.URL.Path)
	}
}

func (s *Server) challenge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate",
		`Bearer realm="`+scheme(r)+"://"+r.Host+`/oauth2/token",service="`+r.Host+`"`)
	writeACRError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required")
}

func scheme(r *http.Request) string {
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		return strings.ToLower(fp)
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func form(r *http.Request) url.Values {
	if r.Method == http.MethodGet {
		return r.URL.Query()
	}
	_ = r.ParseForm()
	return r.Form
}

// exchange implements ACR's /oauth2/exchange: an Entra access token is
// validated, then exchanged for a stateless refresh token.
func (s *Server) exchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeACRError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	f := form(r)
	grant := f.Get("grant_type")
	if grant != "access_token" {
		writeACRError(w, http.StatusBadRequest, "UNSUPPORTED", "grant_type must be access_token")
		return
	}
	accessToken := f.Get("access_token")
	if accessToken == "" {
		writeACRError(w, http.StatusBadRequest, "UNAUTHORIZED", "access_token is required")
		return
	}
	req := r.Clone(r.Context())
	req.Header = req.Header.Clone()
	req.Header.Set("Authorization", "Bearer "+accessToken)
	if err := s.armVerifier.Verify(req); err != nil {
		writeACRError(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
		return
	}
	service := f.Get("service")
	if service == "" {
		service = r.Host
	}
	refresh := s.mintToken(tokenClaims{
		Type:    "refresh",
		Service: service,
		Subject: "entra",
		Expiry:  time.Now().Add(3 * time.Hour).Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]string{"refresh_token": refresh})
}

// token implements ACR's /oauth2/token: a refresh token is exchanged for
// a scoped access token used as Bearer auth on /v2/ and /acr/v1/.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeACRError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	f := form(r)
	if f.Get("grant_type") != "refresh_token" {
		writeACRError(w, http.StatusBadRequest, "UNSUPPORTED", "grant_type must be refresh_token")
		return
	}
	refresh, err := s.parseToken(f.Get("refresh_token"))
	if err != nil || refresh.Type != "refresh" {
		writeACRError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid refresh token")
		return
	}
	service := f.Get("service")
	if service == "" {
		service = refresh.Service
	}
	if refresh.Service != "" && service != refresh.Service {
		writeACRError(w, http.StatusUnauthorized, "UNAUTHORIZED", "service does not match refresh token")
		return
	}
	ttl := 15 * time.Minute
	access := s.mintToken(tokenClaims{
		Type:    "access",
		Service: service,
		Scope:   f.Get("scope"),
		Subject: refresh.Subject,
		Expiry:  time.Now().Add(ttl).Unix(),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"expires_in":   int(ttl.Seconds()),
		"issued_at":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) validAccess(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	claims, err := s.parseToken(strings.TrimPrefix(auth, "Bearer "))
	if err != nil || claims.Type != "access" {
		return false
	}
	return claims.Service == "" || claims.Service == r.Host
}

type tokenClaims struct {
	Type    string `json:"typ"`
	Service string `json:"svc,omitempty"`
	Scope   string `json:"scp,omitempty"`
	Subject string `json:"sub,omitempty"`
	Expiry  int64  `json:"exp"`
}

func (s *Server) mintToken(c tokenClaims) string {
	body, _ := json.Marshal(c)
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, s.tokenKey)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

func (s *Server) parseToken(tok string) (tokenClaims, error) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok || payload == "" || sig == "" {
		return tokenClaims{}, fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, s.tokenKey)
	mac.Write([]byte(payload))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return tokenClaims{}, fmt.Errorf("bad signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return tokenClaims{}, err
	}
	var c tokenClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return tokenClaims{}, err
	}
	if c.Expiry > 0 && time.Now().Unix() > c.Expiry {
		return tokenClaims{}, fmt.Errorf("expired")
	}
	return c, nil
}

func (s *Server) serveARM(w http.ResponseWriter, r *http.Request) {
	name, ok := registryNameFromARMPath(r.URL.Path)
	if !ok {
		writeARMError(w, http.StatusNotFound, "NotFound", "registry path not matched")
		return
	}
	switch r.Method {
	case http.MethodPut:
		writeJSON(w, http.StatusCreated, armRegistry(r, name))
	case http.MethodGet:
		writeJSON(w, http.StatusOK, armRegistry(r, name))
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
	default:
		writeARMError(w, http.StatusMethodNotAllowed, "OperationNotSupported", "method not supported")
	}
}

func registryNameFromARMPath(path string) (string, bool) {
	lower := strings.ToLower(path)
	marker := "/providers/microsoft.containerregistry/registries/"
	i := strings.Index(lower, marker)
	if i < 0 {
		return "", false
	}
	tail := path[i+len(marker):]
	name := strings.Split(strings.Trim(tail, "/"), "/")[0]
	return name, name != ""
}

func armRegistry(r *http.Request, name string) map[string]any {
	id := strings.TrimSuffix(r.URL.Path, "/")
	return map[string]any{
		"id":       id,
		"name":     name,
		"type":     "Microsoft.ContainerRegistry/registries",
		"location": "eastus",
		"sku": map[string]any{
			"name": "Basic",
			"tier": "Basic",
		},
		"properties": map[string]any{
			"loginServer":       r.Host,
			"provisioningState": "Succeeded",
			"adminUserEnabled":  false,
		},
	}
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	shimBase := scheme(r) + "://" + r.Host
	env := map[string]any{
		"name": "AzureCloud",
		"authentication": map[string]any{
			"loginEndpoint": s.loginURL,
			"audiences": []string{
				s.loginURL + "/",
				defaultARMResource,
				shimBase,
			},
			"tenant":           "common",
			"identityProvider": "AAD",
		},
		"resourceManager":          shimBase,
		"microsoftGraphResourceId": s.loginURL + "/",
		"graph":                    s.loginURL,
		"portal":                   s.loginURL,
		"gallery":                  s.loginURL,
		"batch":                    s.loginURL,
		"suffixes": map[string]any{
			"acrLoginServer":         r.Host,
			"acrLoginServerEndpoint": r.Host,
			"keyVaultDns":            "vault.localhost",
			"storage":                "storage.localhost",
			"sqlServerHostname":      "localhost",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if r.URL.Query().Get("api-version") == "2022-09-01" {
		_ = json.NewEncoder(w).Encode(env)
		return
	}
	_ = json.NewEncoder(w).Encode([]any{env})
}

func (s *Server) serveACRv1(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeACRError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", "method not allowed")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/acr/v1/")
	switch {
	case rest == "_catalog":
		s.catalog(w, r)
	case strings.HasSuffix(rest, "/_manifests"):
		repo := strings.TrimSuffix(rest, "/_manifests")
		s.manifests(w, r, repo)
	default:
		writeACRError(w, http.StatusNotFound, "NAME_UNKNOWN", "path not matched: "+r.URL.Path)
	}
}

func (s *Server) catalog(w http.ResponseWriter, r *http.Request) {
	res, err := s.reg.ListRepositories(r.Context(), domain.ListOptions{})
	if err != nil {
		writeDomainACRError(w, err)
		return
	}
	repos := make([]string, 0, len(res.Repositories))
	for _, repo := range res.Repositories {
		repos = append(repos, repo.Name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
}

func (s *Server) manifests(w http.ResponseWriter, r *http.Request, repo string) {
	res, err := s.reg.ListImages(r.Context(), repo, domain.ListOptions{})
	if err != nil {
		writeDomainACRError(w, err)
		return
	}
	items := make([]map[string]any, 0, len(res.Images))
	for _, img := range res.Images {
		t := img.PushedAt.UTC().Format(time.RFC3339)
		items = append(items, map[string]any{
			"digest":         img.Digest,
			"imageSize":      img.Size,
			"createdTime":    t,
			"lastUpdateTime": t,
			"tags":           img.Tags,
			"changeableAttributes": map[string]bool{
				"deleteEnabled": true,
				"listEnabled":   true,
				"readEnabled":   true,
				"writeEnabled":  true,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"registry":  r.Host,
		"imageName": repo,
		"manifests": items,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeARMError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

func writeACRError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"errors": []map[string]string{{"code": code, "message": msg}},
	})
}

func writeDomainACRError(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeACRError(w, http.StatusNotFound, "NAME_UNKNOWN", err.Error())
	case domain.IsInvalidInput(err):
		writeACRError(w, http.StatusBadRequest, "NAME_INVALID", err.Error())
	case domain.IsNotSupported(err):
		writeACRError(w, http.StatusMethodNotAllowed, "UNSUPPORTED", err.Error())
	default:
		writeACRError(w, http.StatusInternalServerError, "UNKNOWN", err.Error())
	}
}

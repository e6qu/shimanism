// Package gcp_secretmanager is the GCP Secret Manager REST/JSON
// frontend for shimanism's secrets service. It speaks the HTTP+JSON
// wire protocol that `google.golang.org/api/secretmanager/v1` (the
// official Discovery-generated REST SDK) and `gcloud secrets` drive,
// and translates each request into a call on the neutral
// `domain.Secrets` interface.
//
// Per AGENTS.md's reuse-over-reinvention rule, the request/response
// wire types come from `google.golang.org/api/secretmanager/v1`
// directly — the same raw types the SDK is generated from. The shim
// only emits the routing + dispatch + error-envelope layer.
package gcp_secretmanager

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	smraw "google.golang.org/api/secretmanager/v1"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Server is a GCP-Secret-Manager-shaped HTTP frontend that
// dispatches to a domain.Secrets backend.
type Server struct {
	s domain.Secrets
}

// New returns a frontend bound to the given backend.
func New(s domain.Secrets) *Server { return &Server{s: s} }

// Route patterns. GCP Secret Manager URLs are regular enough that
// regex matching covers every dispatch.
var (
	// /v1/projects/{project}/secrets/{secret}/versions/{version}:access
	reAccessVersion = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/]+)/versions/([^/:]+):access$`)
	// /v1/projects/{project}/secrets/{secret}/versions/{version}
	reGetVersion = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/]+)/versions/([^/]+)$`)
	// /v1/projects/{project}/secrets/{secret}/versions
	reListVersions = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/]+)/versions$`)
	// /v1/projects/{project}/secrets/{secret}:addVersion
	reAddVersion = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/]+):addVersion$`)
	// /v1/projects/{project}/secrets/{secret}
	reSecret = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/([^/]+)$`)
	// /v1/projects/{project}/secrets
	reSecrets = regexp.MustCompile(`^/v1/projects/([^/]+)/secrets/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	if m := reAccessVersion.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.accessSecretVersion(w, r, m[2], m[3])
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed")
		return
	}
	if m := reAddVersion.FindStringSubmatch(path); m != nil {
		if method == http.MethodPost {
			srv.addSecretVersion(w, r, m[2])
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed")
		return
	}
	if m := reListVersions.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.listSecretVersions(w, r, m[2])
			return
		}
	}
	if m := reGetVersion.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.getSecretVersion(w, r, m[2], m[3])
			return
		}
	}
	if m := reSecret.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodGet:
			srv.getSecret(w, r, m[2])
		case http.MethodDelete:
			srv.deleteSecret(w, r, m[2])
		case http.MethodPatch:
			srv.updateSecret(w, r, m[2])
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on secret")
		}
		return
	}
	if reSecrets.MatchString(path) {
		switch method {
		case http.MethodGet:
			srv.listSecrets(w, r)
		case http.MethodPost:
			srv.createSecret(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "FAILED_PRECONDITION", method+" not allowed on secrets")
		}
		return
	}

	writeError(w, http.StatusNotFound, "NOT_FOUND",
		"no GCP Secret Manager route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

func (srv *Server) createSecret(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("secretId")
	if name == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "secretId query parameter is required")
		return
	}
	var body smraw.Secret
	if !decodeJSON(w, r, &body) {
		return
	}
	opt := domain.CreateSecretOptions{}
	if labels := body.Labels; len(labels) > 0 {
		opt.Tags = map[string]string{}
		for k, v := range labels {
			if k == "shim-description" {
				opt.Description = v
				continue
			}
			opt.Tags[k] = v
		}
		if len(opt.Tags) == 0 {
			opt.Tags = nil
		}
	}
	res, err := srv.s.CreateSecret(r.Context(), name, opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := buildSecretResponse(name, "", domain.Secret{
		Name:           name,
		Description:    opt.Description,
		Tags:           opt.Tags,
		CreatedAt:      time.Now().UTC(),
		Enabled:        true,
		CurrentVersion: res.Version,
	})
	writeJSON(w, http.StatusOK, resp)
}

func (srv *Server) getSecret(w http.ResponseWriter, r *http.Request, name string) {
	s, err := srv.s.HeadSecret(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, buildSecretResponse(name, projectFromPath(r.URL.Path), s))
}

func (srv *Server) updateSecret(w http.ResponseWriter, r *http.Request, name string) {
	// HeadSecret only — UpdateSecret on an arbitrary intersection
	// would need to mutate labels and is out of intersection for
	// this phase. Accept the request, return the secret unmodified.
	_, err := srv.s.HeadSecret(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeError(w, http.StatusBadRequest, "FAILED_PRECONDITION",
		"UpdateSecret is not supported by this shim (intersection-only)")
}

func (srv *Server) deleteSecret(w http.ResponseWriter, r *http.Request, name string) {
	// GCP DeleteSecret is hard-delete. force=true on the domain.
	if err := srv.s.DeleteSecret(r.Context(), name, true); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{})
}

func (srv *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	opt := domain.ListSecretsOptions{NextToken: r.URL.Query().Get("pageToken")}
	if pageSize := r.URL.Query().Get("pageSize"); pageSize != "" {
		if n, err := strconv.Atoi(pageSize); err == nil {
			opt.MaxResults = n
		}
	}
	res, err := srv.s.ListSecrets(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := smraw.ListSecretsResponse{NextPageToken: res.NextToken}
	for _, s := range res.Secrets {
		resp.Secrets = append(resp.Secrets, buildSecretResponse(s.Name, project, s))
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) addSecretVersion(w http.ResponseWriter, r *http.Request, name string) {
	var body smraw.AddSecretVersionRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Payload == nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "payload is required")
		return
	}
	// Payload.Data is base64-encoded in the JSON wire form.
	data, err := base64.StdEncoding.DecodeString(body.Payload.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT",
			"payload.data is not valid base64: "+err.Error())
		return
	}
	res, err := srv.s.PutSecretValue(r.Context(), name, data)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := buildVersionResponse(name, project, res.Version, time.Now().UTC())
	writeJSON(w, http.StatusOK, resp)
}

func (srv *Server) accessSecretVersion(w http.ResponseWriter, r *http.Request, name, version string) {
	v, err := parseVersion(version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	val, err := srv.s.GetSecretValue(r.Context(), name, v)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := smraw.AccessSecretVersionResponse{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/%d", project, name, val.Version),
		Payload: &smraw.SecretPayload{
			Data: base64.StdEncoding.EncodeToString(val.Value),
		},
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getSecretVersion(w http.ResponseWriter, r *http.Request, name, version string) {
	v, err := parseVersion(version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", err.Error())
		return
	}
	// Translate "give me the metadata for this version" — listVersions
	// + filter — into a single domain call.
	versions, err := srv.s.ListVersions(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	// version == 0 means "latest".
	target := v
	if target == 0 {
		if len(versions) == 0 {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "no versions exist for "+name)
			return
		}
		target = versions[len(versions)-1].Number
	}
	for _, vv := range versions {
		if vv.Number == target {
			project := projectFromPath(r.URL.Path)
			writeJSON(w, http.StatusOK, buildVersionResponse(name, project, vv.Number, vv.CreatedAt))
			return
		}
	}
	writeError(w, http.StatusNotFound, "NOT_FOUND",
		fmt.Sprintf("secret %q has no version %d", name, v))
}

func (srv *Server) listSecretVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := srv.s.ListVersions(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	project := projectFromPath(r.URL.Path)
	resp := smraw.ListSecretVersionsResponse{}
	for _, v := range versions {
		resp.Versions = append(resp.Versions, buildVersionResponse(name, project, v.Number, v.CreatedAt))
	}
	writeJSON(w, http.StatusOK, &resp)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func parseVersion(s string) (uint64, error) {
	if s == "latest" || s == "" {
		return 0, nil
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("version must be 'latest' or a positive integer (got %q)", s)
	}
	return v, nil
}

func projectFromPath(path string) string {
	const prefix = "/v1/projects/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(path, prefix)
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

func buildSecretResponse(name, project string, s domain.Secret) *smraw.Secret {
	if project == "" {
		project = "shim"
	}
	out := &smraw.Secret{
		Name: fmt.Sprintf("projects/%s/secrets/%s", project, name),
	}
	if !s.CreatedAt.IsZero() {
		out.CreateTime = s.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	labels := map[string]string{}
	for k, v := range s.Tags {
		labels[k] = v
	}
	if s.Description != "" {
		labels["shim-description"] = s.Description
	}
	if len(labels) > 0 {
		out.Labels = labels
	}
	out.Replication = &smraw.Replication{
		Automatic: &smraw.Automatic{},
	}
	return out
}

func buildVersionResponse(name, project string, version uint64, created time.Time) *smraw.SecretVersion {
	if project == "" {
		project = "shim"
	}
	v := &smraw.SecretVersion{
		Name:  fmt.Sprintf("projects/%s/secrets/%s/versions/%d", project, name, version),
		State: "ENABLED",
	}
	if !created.IsZero() {
		v.CreateTime = created.UTC().Format(time.RFC3339Nano)
	}
	return v
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// keep errors-as-import usage discoverable.
var _ = errors.As

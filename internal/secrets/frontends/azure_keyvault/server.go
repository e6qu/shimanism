// Package azure_keyvault is the Azure Key Vault secrets-surface
// REST/JSON frontend for shimanism's secrets service. It speaks
// the HTTP+JSON wire protocol that
// `azure-sdk-for-go/sdk/security/keyvault/azsecrets` and
// `az keyvault secret` drive, and translates each request into a
// call on the neutral `domain.Secrets` interface.
//
// Per AGENTS.md's reuse-over-reinvention rule, the wire shapes
// match the shapes the Azure SDK's `azsecrets/internal/generated`
// package uses to decode. We define them inline here to keep the
// frontend self-contained.
package azure_keyvault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// Server is an Azure-Key-Vault-shaped HTTP frontend.
type Server struct {
	s domain.Secrets
}

// New returns a frontend bound to the given backend.
func New(s domain.Secrets) *Server { return &Server{s: s} }

// Route patterns. Azure Key Vault uses `/secrets/...` paths.
var (
	// /secrets/{name}/versions
	reSecretVersions = regexp.MustCompile(`^/secrets/([^/]+)/versions$`)
	// /secrets/{name}/{version}
	reSecretVersion = regexp.MustCompile(`^/secrets/([^/]+)/([^/]+)$`)
	// /secrets/{name}
	reSecret = regexp.MustCompile(`^/secrets/([^/]+)$`)
	// /deletedsecrets/{name}
	reDeletedSecret = regexp.MustCompile(`^/deletedsecrets/([^/]+)$`)
	// /secrets
	reSecrets = regexp.MustCompile(`^/secrets/?$`)
)

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Azure Key Vault uses a challenge-response auth flow: the client
	// sends a probe request without credentials, the server replies
	// 401 with a WWW-Authenticate header pointing at an OAuth2 token
	// endpoint, then the client re-sends with `Authorization: Bearer
	// <token>`. The shim doesn't validate the token at this phase,
	// but it must still issue the 401 challenge so SDK clients
	// include the body on the second attempt (otherwise the SDK
	// short-circuits on the empty-body 200).
	if r.Header.Get("Authorization") == "" {
		w.Header().Set("WWW-Authenticate", `Bearer authorization="https://login.microsoftonline.com/shim", resource="https://vault.azure.net"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	path := r.URL.Path
	// Azure SDK sometimes appends a trailing slash when the version
	// segment is empty (e.g. /secrets/<name>/). Normalise so route
	// patterns match either shape.
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimRight(path, "/")
	}
	method := r.Method

	if m := reSecretVersions.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.listSecretVersions(w, r, m[1])
			return
		}
	}
	if m := reSecretVersion.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.getSecretVersion(w, r, m[1], m[2])
			return
		}
	}
	if m := reSecret.FindStringSubmatch(path); m != nil {
		switch method {
		case http.MethodPut:
			srv.setSecret(w, r, m[1])
		case http.MethodGet:
			srv.getSecret(w, r, m[1])
		case http.MethodDelete:
			srv.deleteSecret(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
				method+" not allowed on secret")
		}
		return
	}
	if m := reDeletedSecret.FindStringSubmatch(path); m != nil {
		if method == http.MethodDelete {
			// Purge the soft-deleted secret.
			srv.purgeSecret(w, r, m[1])
			return
		}
	}
	if reSecrets.MatchString(path) {
		if method == http.MethodGet {
			srv.listSecrets(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "SecretNotFound",
		"no Key Vault secrets route matches "+method+" "+path)
}

// ----------------------------------------------------------------------
// Wire types — JSON shapes the SDK puts on / reads from the wire.
// ----------------------------------------------------------------------

type setSecretRequest struct {
	Value            string            `json:"value"`
	Tags             map[string]string `json:"tags,omitempty"`
	ContentType      string            `json:"contentType,omitempty"`
	SecretAttributes *secretAttributes `json:"attributes,omitempty"`
}

type secretAttributes struct {
	Enabled       *bool  `json:"enabled,omitempty"`
	Created       int64  `json:"created,omitempty"` // unix seconds
	Updated       int64  `json:"updated,omitempty"` // unix seconds
	NotBefore     int64  `json:"nbf,omitempty"`
	Expires       int64  `json:"exp,omitempty"`
	RecoveryLevel string `json:"recoveryLevel,omitempty"`
}

type secretBundle struct {
	ID         string            `json:"id"`
	Value      *string           `json:"value,omitempty"`
	Attributes *secretAttributes `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type secretItem struct {
	ID         string            `json:"id"`
	Attributes *secretAttributes `json:"attributes,omitempty"`
	Tags       map[string]string `json:"tags,omitempty"`
}

type listSecretsResponse struct {
	Value    []secretItem `json:"value"`
	NextLink string       `json:"nextLink,omitempty"`
}

// ----------------------------------------------------------------------
// Handlers
// ----------------------------------------------------------------------

func (srv *Server) setSecret(w http.ResponseWriter, r *http.Request, name string) {
	var body setSecretRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// Azure SetSecret either creates a new secret or appends a new
	// version to an existing one. Domain CreateSecret returns
	// SecretAlreadyExists; in that case fall through to
	// PutSecretValue.
	tags := body.Tags
	description := ""
	if v, ok := tags["shim-description"]; ok {
		description = v
		delete(tags, "shim-description")
	}
	val := []byte(body.Value)
	createRes, err := srv.s.CreateSecret(r.Context(), name, domain.CreateSecretOptions{
		Description:  description,
		Tags:         tags,
		InitialValue: val,
	})
	if err != nil {
		var de *domain.Error
		if errors.As(err, &de) && de.Kind == domain.KindSecretAlreadyExists {
			putRes, perr := srv.s.PutSecretValue(r.Context(), name, val)
			if perr != nil {
				mapDomainError(w, perr)
				return
			}
			writeSecretBundle(w, http.StatusOK, name, val, putRes.Version, time.Now().UTC(), tags, description, r)
			return
		}
		mapDomainError(w, err)
		return
	}
	writeSecretBundle(w, http.StatusOK, name, val, createRes.Version, time.Now().UTC(), tags, description, r)
}

func (srv *Server) getSecret(w http.ResponseWriter, r *http.Request, name string) {
	val, err := srv.s.GetSecretValue(r.Context(), name, 0)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	s, herr := srv.s.HeadSecret(r.Context(), name)
	if herr != nil {
		mapDomainError(w, herr)
		return
	}
	writeSecretBundle(w, http.StatusOK, name, val.Value, val.Version, val.CreatedAt, s.Tags, s.Description, r)
}

func (srv *Server) getSecretVersion(w http.ResponseWriter, r *http.Request, name, version string) {
	v, err := versionFromGUID(version)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}
	val, gerr := srv.s.GetSecretValue(r.Context(), name, v)
	if gerr != nil {
		mapDomainError(w, gerr)
		return
	}
	s, herr := srv.s.HeadSecret(r.Context(), name)
	if herr != nil {
		mapDomainError(w, herr)
		return
	}
	writeSecretBundle(w, http.StatusOK, name, val.Value, val.Version, val.CreatedAt, s.Tags, s.Description, r)
}

func (srv *Server) deleteSecret(w http.ResponseWriter, r *http.Request, name string) {
	// Azure soft-deletes by default; force-purge happens through
	// /deletedsecrets/{name}. Domain: force=false.
	if err := srv.s.DeleteSecret(r.Context(), name, false); err != nil {
		mapDomainError(w, err)
		return
	}
	// Azure's DeleteSecret returns a DeletedSecretBundle. Approximate.
	resp := map[string]interface{}{
		"id":                 vaultBaseFromHeader(r) + "/secrets/" + name,
		"recoveryLevel":      "Purgeable",
		"scheduledPurgeDate": time.Now().Add(7 * 24 * time.Hour).Unix(),
		"deletedDate":        time.Now().Unix(),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (srv *Server) purgeSecret(w http.ResponseWriter, r *http.Request, name string) {
	if err := srv.s.DeleteSecret(r.Context(), name, true); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	res, err := srv.s.ListSecrets(r.Context(), domain.ListSecretsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	vaultBase := vaultBaseFromHeader(r)
	out := listSecretsResponse{}
	for _, s := range res.Secrets {
		out.Value = append(out.Value, secretItem{
			ID:         vaultBase + "/secrets/" + s.Name,
			Attributes: attributesFromSecret(s),
			Tags:       tagsWithDescription(s.Tags, s.Description),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) listSecretVersions(w http.ResponseWriter, r *http.Request, name string) {
	versions, err := srv.s.ListVersions(r.Context(), name)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	s, herr := srv.s.HeadSecret(r.Context(), name)
	if herr != nil {
		mapDomainError(w, herr)
		return
	}
	vaultBase := vaultBaseFromHeader(r)
	out := listSecretsResponse{}
	for _, v := range versions {
		guid := guidFromVersion(v.Number)
		out.Value = append(out.Value, secretItem{
			ID: vaultBase + "/secrets/" + name + "/" + guid,
			Attributes: &secretAttributes{
				Created: v.CreatedAt.Unix(),
				Updated: v.CreatedAt.Unix(),
			},
			Tags: tagsWithDescription(s.Tags, s.Description),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func writeSecretBundle(w http.ResponseWriter, status int, name string, value []byte, version uint64, created time.Time, tags map[string]string, description string, r *http.Request) {
	val := string(value)
	vaultBase := vaultBaseFromHeader(r)
	bundle := secretBundle{
		ID:    fmt.Sprintf("%s/secrets/%s/%s", vaultBase, name, guidFromVersion(version)),
		Value: &val,
		Attributes: &secretAttributes{
			Enabled: boolPtr(true),
		},
		Tags: tagsWithDescription(tags, description),
	}
	if !created.IsZero() {
		bundle.Attributes.Created = created.Unix()
		bundle.Attributes.Updated = created.Unix()
	}
	writeJSON(w, status, bundle)
}

func boolPtr(b bool) *bool { return &b }

func tagsWithDescription(tags map[string]string, description string) map[string]string {
	if description == "" && len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags)+1)
	for k, v := range tags {
		out[k] = v
	}
	if description != "" {
		out["shim-description"] = description
	}
	return out
}

func attributesFromSecret(s domain.Secret) *secretAttributes {
	a := &secretAttributes{Enabled: boolPtr(s.Enabled)}
	if !s.CreatedAt.IsZero() {
		a.Created = s.CreatedAt.Unix()
	}
	if !s.UpdatedAt.IsZero() {
		a.Updated = s.UpdatedAt.Unix()
	}
	return a
}

// guidFromVersion encodes a monotonic uint64 version as a 32-hex-char
// Azure-style GUID (UUID v4-formatted at the wire layer; the
// monotonic value lives in the low 64 bits). The mapping is
// deterministic so round-trips round-trip without any shim state.
func guidFromVersion(n uint64) string {
	var buf [16]byte
	// Low 8 bytes carry the monotonic version. Upper 8 bytes are
	// zero, which is fine — Azure accepts any 32-hex-char value.
	buf[8] = byte(n >> 56)
	buf[9] = byte(n >> 48)
	buf[10] = byte(n >> 40)
	buf[11] = byte(n >> 32)
	buf[12] = byte(n >> 24)
	buf[13] = byte(n >> 16)
	buf[14] = byte(n >> 8)
	buf[15] = byte(n)
	return hex.EncodeToString(buf[:])
}

func versionFromGUID(guid string) (uint64, error) {
	// Strip dashes (some clients emit hyphenated UUIDs).
	clean := strings.ReplaceAll(guid, "-", "")
	if len(clean) != 32 {
		return 0, fmt.Errorf("version GUID must be 32 hex chars (got %d)", len(clean))
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return 0, fmt.Errorf("version GUID is not hex: %w", err)
	}
	// Low 8 bytes carry the version.
	var n uint64
	for i := 8; i < 16; i++ {
		n = (n << 8) | uint64(b[i])
	}
	return n, nil
}

func vaultBaseFromHeader(r *http.Request) string {
	host := r.Host
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	if r.Header.Get("X-Forwarded-Proto") != "" {
		scheme = r.Header.Get("X-Forwarded-Proto")
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadParameter", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// keep these imports referenced even when an emitted code path
// doesn't exercise them.
var _ = strconv.Itoa
var _ = rand.Reader
var _ = path.Base

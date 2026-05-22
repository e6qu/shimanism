// Package azure_keyvault is the Azure Key Vault secrets-surface
// REST/JSON frontend. Wire shapes + routing come from the
// spec-driven generated stubs in services/secrets/gen
// (cmd/azure-codegen); the adapter on Server implements
// gen.ServerInterface and translates each operation into a call
// on the neutral domain.Secrets interface.
package azure_keyvault

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	gen "github.com/e6qu/shimanism/services/secrets/gen/azure"
)

// Server is an Azure-Key-Vault-shaped HTTP frontend. It implements
// gen.ServerInterface; ServeHTTP routes through the gen-generated
// http.Handler with a small pre-dispatch normalisation pass for the
// SDK's empty-version-trailing-slash idiom.
type Server struct {
	s   domain.Secrets
	mux http.Handler
}

// New returns a frontend bound to the given backend.
func New(s domain.Secrets) *Server {
	srv := &Server{s: s}
	srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})
	return srv
}

// ServeHTTP dispatches through the generated routing. Two SDK
// idioms aren't expressed in the upstream OpenAPI spec and so don't
// have a generated route:
//
//   - `GET /secrets/{name}/` (trailing slash, empty version) means
//     "latest version" — the SDK uses it whenever GetSecret is
//     called without an explicit version. The spec only has the
//     two-segment `/secrets/{secret-name}/{secret-version}` route,
//     so we dispatch this case directly to GetSecret with an empty
//     version (which the handler resolves to version 0).
//   - `GET /secrets/{name}` (no trailing slash) — same intent, same
//     handling.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/secrets/") {
		rest := strings.TrimPrefix(r.URL.Path, "/secrets/")
		rest = strings.TrimSuffix(rest, "/")
		if rest != "" && !strings.Contains(rest, "/") {
			srv.GetSecret(w, r, rest, "", gen.GetSecretParams{})
			return
		}
	}
	if p := r.URL.Path; len(p) > 1 && strings.HasSuffix(p, "/") {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimRight(p, "/")
		srv.mux.ServeHTTP(w, r2)
		return
	}
	srv.mux.ServeHTTP(w, r)
}

// notImplemented writes the Azure-shaped "operation not supported"
// error envelope for spec-defined operations the cross-cloud
// intersection doesn't carry (Backup / Restore / RecoverDeleted /
// UpdateSecret / per-deleted-secret reads). Honest 501.
func notImplemented(w http.ResponseWriter, op string) {
	writeError(w, http.StatusNotImplemented, "Forbidden",
		op+" is not in the cross-cloud secrets intersection")
}

// Wire types come from services/secrets/gen/azure_keyvault.gen.go
// (cmd/azure-codegen). Hand-rolled shapes were retired in 12.A.1.

func (srv *Server) SetSecret(w http.ResponseWriter, r *http.Request, name string, _ gen.SetSecretParams) {
	var body gen.SecretSetParameters
	if !decodeJSON(w, r, &body) {
		return
	}
	// Azure SetSecret either creates a new secret or appends a new
	// version to an existing one. Domain CreateSecret returns
	// SecretAlreadyExists; in that case fall through to
	// PutSecretValue.
	var tags map[string]string
	if body.Tags != nil {
		tags = *body.Tags
	}
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

// GetSecret handles both `GET /secrets/{name}` (latest version, the
// trailing-slash normaliser rewrites `/secrets/{name}/` to this) and
// `GET /secrets/{name}/{version}`. The Azure spec models them as two
// operations; the std-net-http router emits an empty `secretVersion`
// for the no-version variant, which we resolve to version 0 (latest).
func (srv *Server) GetSecret(w http.ResponseWriter, r *http.Request, name, version string, _ gen.GetSecretParams) {
	var v uint64
	if version != "" {
		parsed, err := versionFromGUID(version)
		if err != nil {
			writeError(w, http.StatusBadRequest, "BadParameter", err.Error())
			return
		}
		v = parsed
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

func (srv *Server) DeleteSecret(w http.ResponseWriter, r *http.Request, name string, _ gen.DeleteSecretParams) {
	// Azure soft-deletes by default; force-purge happens through
	// /deletedsecrets/{name}. Domain: force=false.
	if err := srv.s.DeleteSecret(r.Context(), name, false); err != nil {
		mapDomainError(w, err)
		return
	}
	id := vaultBaseFromHeader(r) + "/secrets/" + name
	now := int(time.Now().Unix())
	purge := int(time.Now().Add(7 * 24 * time.Hour).Unix())
	recovery := gen.DeletionRecoveryLevel("Purgeable")
	resp := gen.DeletedSecretBundle{
		Id:                 &id,
		RecoveryId:         &id,
		DeletedDate:        &now,
		ScheduledPurgeDate: &purge,
		Attributes: &gen.SecretAttributes{
			RecoveryLevel: &recovery,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (srv *Server) PurgeDeletedSecret(w http.ResponseWriter, r *http.Request, name string, _ gen.PurgeDeletedSecretParams) {
	if err := srv.s.DeleteSecret(r.Context(), name, true); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (srv *Server) GetSecrets(w http.ResponseWriter, r *http.Request, _ gen.GetSecretsParams) {
	res, err := srv.s.ListSecrets(r.Context(), domain.ListSecretsOptions{})
	if err != nil {
		mapDomainError(w, err)
		return
	}
	vaultBase := vaultBaseFromHeader(r)
	items := make([]gen.SecretItem, 0, len(res.Secrets))
	for _, s := range res.Secrets {
		id := vaultBase + "/secrets/" + s.Name
		items = append(items, gen.SecretItem{
			Id:         &id,
			Attributes: attributesFromSecret(s),
			Tags:       tagsWithDescriptionPtr(s.Tags, s.Description),
		})
	}
	writeJSON(w, http.StatusOK, gen.SecretListResult{Value: &items})
}

func (srv *Server) GetSecretVersions(w http.ResponseWriter, r *http.Request, name string, _ gen.GetSecretVersionsParams) {
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
	tags := tagsWithDescriptionPtr(s.Tags, s.Description)
	items := make([]gen.SecretItem, 0, len(versions))
	for _, v := range versions {
		id := vaultBase + "/secrets/" + name + "/" + guidFromVersion(v.Number)
		created := int(v.CreatedAt.Unix())
		items = append(items, gen.SecretItem{
			Id: &id,
			Attributes: &gen.SecretAttributes{
				Created: &created,
				Updated: &created,
			},
			Tags: tags,
		})
	}
	writeJSON(w, http.StatusOK, gen.SecretListResult{Value: &items})
}

// Spec operations outside the cross-cloud secrets intersection.
// Real Azure Key Vault would honour them; the shim's neutral
// domain doesn't carry the concepts (per-deleted-secret reads,
// per-version attribute updates, raw backup/restore blobs).

func (srv *Server) BackupSecret(w http.ResponseWriter, _ *http.Request, _ string, _ gen.BackupSecretParams) {
	notImplemented(w, "BackupSecret")
}

func (srv *Server) RestoreSecret(w http.ResponseWriter, _ *http.Request, _ gen.RestoreSecretParams) {
	notImplemented(w, "RestoreSecret")
}

func (srv *Server) UpdateSecret(w http.ResponseWriter, _ *http.Request, _, _ string, _ gen.UpdateSecretParams) {
	notImplemented(w, "UpdateSecret")
}

func (srv *Server) GetDeletedSecret(w http.ResponseWriter, _ *http.Request, _ string, _ gen.GetDeletedSecretParams) {
	notImplemented(w, "GetDeletedSecret")
}

func (srv *Server) GetDeletedSecrets(w http.ResponseWriter, _ *http.Request, _ gen.GetDeletedSecretsParams) {
	notImplemented(w, "GetDeletedSecrets")
}

func (srv *Server) RecoverDeletedSecret(w http.ResponseWriter, _ *http.Request, _ string, _ gen.RecoverDeletedSecretParams) {
	notImplemented(w, "RecoverDeletedSecret")
}

func writeSecretBundle(w http.ResponseWriter, status int, name string, value []byte, version uint64, created time.Time, tags map[string]string, description string, r *http.Request) {
	val := string(value)
	id := fmt.Sprintf("%s/secrets/%s/%s", vaultBaseFromHeader(r), name, guidFromVersion(version))
	enabled := true
	attrs := &gen.SecretAttributes{Enabled: &enabled}
	if !created.IsZero() {
		c := int(created.Unix())
		attrs.Created = &c
		attrs.Updated = &c
	}
	writeJSON(w, status, gen.SecretBundle{
		Id:         &id,
		Value:      &val,
		Attributes: attrs,
		Tags:       tagsWithDescriptionPtr(tags, description),
	})
}

func tagsWithDescriptionPtr(tags map[string]string, description string) *map[string]string {
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
	return &out
}

func attributesFromSecret(s domain.Secret) *gen.SecretAttributes {
	a := &gen.SecretAttributes{Enabled: &s.Enabled}
	if !s.CreatedAt.IsZero() {
		c := int(s.CreatedAt.Unix())
		a.Created = &c
	}
	if !s.UpdatedAt.IsZero() {
		u := int(s.UpdatedAt.Unix())
		a.Updated = &u
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

var _ = strconv.Itoa
var _ = rand.Reader

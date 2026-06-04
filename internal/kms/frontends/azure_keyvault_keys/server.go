// Package azure_keyvault_keys is the Azure Key Vault keys data-plane
// frontend for shimanism's key-management service (Phase 19). It speaks
// the Key Vault keys REST protocol that azure-sdk-for-go/.../azkeys and
// `az keyvault key` drive, translating onto the neutral domain.KMS
// interface. Bearer auth is wired by the harness (azurebearer).
//
// A Key Vault key maps to a domain key: the user-chosen key name is the
// domain key ID. Standard Key Vault keys are asymmetric (RSA/EC); the
// shim's encrypt/decrypt intersection treats the ciphertext as opaque
// bytes, so the round-trip is backend-agnostic (the inmem backend uses
// AES-GCM; a real Key Vault backend uses the key's RSA-OAEP). Byte
// values ride the wire as base64url, handled by the azkeys types' serde.
package azure_keyvault_keys

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"

	"github.com/e6qu/shimanism/internal/kms/domain"
)

// Server is a Key-Vault-keys-shaped HTTP frontend.
type Server struct {
	k domain.KMS
}

// New returns a frontend bound to the given backend.
func New(k domain.KMS) *Server { return &Server{k: k} }

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if !strings.HasPrefix(path, "keys") {
		writeAzErr(w, http.StatusNotFound, "NotFound", "path not matched: "+r.URL.Path)
		return
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(path, "keys"), "/")

	// /keys (collection)
	if rest == "" {
		if r.Method == http.MethodGet {
			srv.listKeys(w, r)
			return
		}
		writeAzErr(w, http.StatusMethodNotAllowed, "MethodNotAllowed", r.Method)
		return
	}

	// Colon-free Azure uses path suffixes /create, /{ver}/encrypt, etc.
	segs := strings.Split(rest, "/")
	name := segs[0]
	switch {
	case len(segs) == 2 && segs[1] == "create" && r.Method == http.MethodPost:
		srv.createKey(w, r, name)
	case len(segs) >= 2 && segs[len(segs)-1] == "encrypt" && r.Method == http.MethodPost:
		srv.encrypt(w, r, name)
	case len(segs) >= 2 && segs[len(segs)-1] == "decrypt" && r.Method == http.MethodPost:
		srv.decrypt(w, r, name)
	case (len(segs) == 1 || len(segs) == 2) && r.Method == http.MethodGet:
		srv.getKey(w, r, name)
	case len(segs) == 1 && r.Method == http.MethodDelete:
		srv.deleteKey(w, r, name)
	default:
		writeAzErr(w, http.StatusNotFound, "NotFound", "key operation not matched: "+r.URL.Path)
	}
}

func (srv *Server) kid(r *http.Request, name string) *azkeys.ID {
	id := azkeys.ID(fmt.Sprintf("https://%s/keys/%s/1", r.Host, name))
	return &id
}

func (srv *Server) createKey(w http.ResponseWriter, r *http.Request, name string) {
	var req azkeys.CreateKeyParameters
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzErr(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}
	opts := domain.CreateKeyOptions{KeyID: name, Usage: domain.KeyUsageEncryptDecrypt}
	if req.Kty != nil {
		opts.KeySpec = string(*req.Kty)
	}
	key, err := srv.k.CreateKey(r.Context(), opts)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv.keyBundle(r, key))
}

func (srv *Server) getKey(w http.ResponseWriter, r *http.Request, name string) {
	key, err := srv.k.DescribeKey(r.Context(), name)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, srv.keyBundle(r, key))
}

func (srv *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	res, err := srv.k.ListKeys(r.Context())
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	type keyListResult struct {
		Value []map[string]any `json:"value"`
	}
	out := keyListResult{Value: []map[string]any{}}
	for _, k := range res.Keys {
		out.Value = append(out.Value, map[string]any{
			"kid":        fmt.Sprintf("https://%s/keys/%s", r.Host, k.ID),
			"attributes": map[string]any{"enabled": k.State == domain.KeyStateEnabled},
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (srv *Server) deleteKey(w http.ResponseWriter, r *http.Request, name string) {
	key, err := srv.k.ScheduleKeyDeletion(r.Context(), name, 30)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	// DeletedKey bundle is a superset of KeyBundle; the SDK reads `key`.
	writeJSON(w, http.StatusOK, srv.keyBundle(r, key))
}

func (srv *Server) encrypt(w http.ResponseWriter, r *http.Request, name string) {
	var req azkeys.KeyOperationParameters
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzErr(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}
	res, err := srv.k.Encrypt(r.Context(), name, req.Value)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, azkeys.KeyOperationResult{KID: srv.kid(r, name), Result: res.Ciphertext})
}

func (srv *Server) decrypt(w http.ResponseWriter, r *http.Request, name string) {
	var req azkeys.KeyOperationParameters
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAzErr(w, http.StatusBadRequest, "BadParameter", err.Error())
		return
	}
	res, err := srv.k.Decrypt(r.Context(), name, req.Value)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, azkeys.KeyOperationResult{KID: srv.kid(r, name), Result: res.Plaintext})
}

func (srv *Server) keyBundle(r *http.Request, key domain.Key) azkeys.KeyBundle {
	kty := azkeys.KeyType(key.KeySpec)
	if key.KeySpec == "" {
		kty = azkeys.KeyTypeRSA
	}
	enabled := key.State == domain.KeyStateEnabled
	return azkeys.KeyBundle{
		Key: &azkeys.JSONWebKey{
			KID: srv.kid(r, key.ID),
			Kty: &kty,
		},
		Attributes: &azkeys.KeyAttributes{Enabled: &enabled},
		Tags:       toStrPtrMap(key.Tags),
	}
}

func toStrPtrMap(m map[string]string) map[string]*string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]*string, len(m))
	for k, v := range m {
		out[k] = to.Ptr(v)
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type azErr struct {
	Error azErrBody `json:"error"`
}
type azErrBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAzErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, azErr{Error: azErrBody{Code: code, Message: msg}})
}

func writeKMSErr(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeAzErr(w, http.StatusNotFound, "KeyNotFound", err.Error())
	case domain.IsAlreadyExists(err):
		writeAzErr(w, http.StatusConflict, "Conflict", err.Error())
	case domain.IsNotSupported(err):
		writeAzErr(w, http.StatusBadRequest, "NotSupported", err.Error())
	case domain.IsInvalidInput(err), domain.IsKeyDisabled(err):
		writeAzErr(w, http.StatusBadRequest, "BadParameter", err.Error())
	default:
		writeAzErr(w, http.StatusInternalServerError, "InternalError", err.Error())
	}
}

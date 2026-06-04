// Package gcp_cloudkms is the GCP Cloud KMS frontend for shimanism's
// key-management service (Phase 19). It speaks the Cloud KMS v1 REST
// protocol that google.golang.org/api/cloudkms/v1 and `gcloud kms`
// drive, and translates onto the neutral domain.KMS interface.
//
// Cloud KMS has a project/location/keyRing/cryptoKey/cryptoKeyVersion
// hierarchy the flat domain doesn't model. keyRings are a GCP-specific
// container with no cross-cloud analog — the frontend accepts
// keyRings.create/get as synthetic success (out of intersection) so GCP
// clients can proceed to create keys. A cryptoKey maps to a domain key:
// the user-chosen cryptoKeyId becomes the domain key ID. Encrypt/Decrypt
// target a cryptoKey (its primary version, implicitly).
//
// Plaintext/ciphertext ride the wire as base64 strings (REST `bytes`).
package gcp_cloudkms

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	kmsraw "google.golang.org/api/cloudkms/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/kms/domain"
)

// Server is a Cloud-KMS-v1-shaped HTTP frontend.
type Server struct {
	k domain.KMS
}

// New returns a frontend bound to the given backend.
func New(k domain.KMS) *Server { return &Server{k: k} }

// Handler wraps Server with the GCP bearer verifier middleware.
func Handler(k domain.KMS) http.Handler {
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://cloudkms.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	return gcpbearer.Middleware(verifier)(New(k))
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/")
	if rest == r.URL.Path {
		rest = strings.TrimPrefix(r.URL.Path, "/")
	}

	// Colon actions first.
	switch {
	case strings.HasSuffix(rest, ":encrypt") && r.Method == http.MethodPost:
		srv.encrypt(w, r, gcpKeyName(strings.TrimSuffix(rest, ":encrypt")))
		return
	case strings.HasSuffix(rest, ":decrypt") && r.Method == http.MethodPost:
		srv.decrypt(w, r, gcpKeyName(strings.TrimSuffix(rest, ":decrypt")))
		return
	}

	// keyRings collection / item.
	if strings.HasSuffix(rest, "/keyRings") && r.Method == http.MethodPost {
		srv.createKeyRing(w, r)
		return
	}
	if i := strings.Index(rest, "/keyRings/"); i >= 0 {
		tail := rest[i+len("/keyRings/"):]
		// tail = "{ring}" | "{ring}/cryptoKeys" | "{ring}/cryptoKeys/{key}"
		if ck := strings.Index(tail, "/cryptoKeys"); ck >= 0 {
			ckTail := strings.TrimPrefix(tail[ck+len("/cryptoKeys"):], "/")
			switch {
			case ckTail == "" && r.Method == http.MethodPost:
				srv.createCryptoKey(w, r)
			case ckTail == "" && r.Method == http.MethodGet:
				srv.listCryptoKeys(w, r, rest)
			case ckTail != "" && r.Method == http.MethodGet:
				srv.getCryptoKey(w, r, ckTail, rest)
			case ckTail != "" && (r.Method == http.MethodPatch || r.Method == http.MethodPut):
				srv.patchCryptoKey(w, r, ckTail, rest)
			default:
				writeErr(w, http.StatusMethodNotAllowed, r.Method+" not allowed")
			}
			return
		}
		// keyRings.get
		if r.Method == http.MethodGet {
			srv.getKeyRing(w, r, rest)
			return
		}
	}
	writeErr(w, http.StatusNotFound, "Resource not found: "+r.URL.Path)
}

// gcpKeyName returns the cryptoKey short name from a resource path
// `.../cryptoKeys/{key}`.
func gcpKeyName(path string) string {
	if i := strings.LastIndex(path, "/cryptoKeys/"); i >= 0 {
		return path[i+len("/cryptoKeys/"):]
	}
	return path
}

// ─── keyRings (synthetic — out of intersection) ──────────────────────

func (srv *Server) createKeyRing(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("keyRingId")
	parent := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "/keyRings")
	name := parent + "/keyRings/" + id
	writeJSON(w, http.StatusOK, &kmsraw.KeyRing{Name: name, CreateTime: nowRFC3339()})
}

func (srv *Server) getKeyRing(w http.ResponseWriter, _ *http.Request, rest string) {
	writeJSON(w, http.StatusOK, &kmsraw.KeyRing{Name: rest, CreateTime: nowRFC3339()})
}

// ─── cryptoKeys ──────────────────────────────────────────────────────

func (srv *Server) createCryptoKey(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("cryptoKeyId")
	var req kmsraw.CryptoKey
	_ = json.NewDecoder(r.Body).Decode(&req)
	opts := domain.CreateKeyOptions{
		KeyID:   id,
		Usage:   domain.KeyUsageEncryptDecrypt,
		KeySpec: "google-symmetric-encryption",
		Tags:    req.Labels,
	}
	key, err := srv.k.CreateKey(r.Context(), opts)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	parent := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "/cryptoKeys")
	writeJSON(w, http.StatusOK, domainKeyToGCP(key, parent+"/cryptoKeys/"+key.ID))
}

func (srv *Server) getCryptoKey(w http.ResponseWriter, r *http.Request, keyID, rest string) {
	key, err := srv.k.DescribeKey(r.Context(), keyID)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, domainKeyToGCP(key, rest))
}

func (srv *Server) listCryptoKeys(w http.ResponseWriter, r *http.Request, rest string) {
	res, err := srv.k.ListKeys(r.Context())
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	parent := strings.TrimSuffix(rest, "/cryptoKeys")
	out := &kmsraw.ListCryptoKeysResponse{TotalSize: int64(len(res.Keys))}
	for _, k := range res.Keys {
		out.CryptoKeys = append(out.CryptoKeys, domainKeyToGCP(k, parent+"/cryptoKeys/"+k.ID))
	}
	writeJSON(w, http.StatusOK, out)
}

// patchCryptoKey handles rotation enable/disable via rotationPeriod.
func (srv *Server) patchCryptoKey(w http.ResponseWriter, r *http.Request, keyID, rest string) {
	var req kmsraw.CryptoKey
	_ = json.NewDecoder(r.Body).Decode(&req)
	var err error
	if req.RotationPeriod != "" || req.NextRotationTime != "" {
		err = srv.k.EnableKeyRotation(r.Context(), keyID)
	} else {
		err = srv.k.DisableKeyRotation(r.Context(), keyID)
	}
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	key, err := srv.k.DescribeKey(r.Context(), keyID)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	g := domainKeyToGCP(key, rest)
	if req.RotationPeriod != "" {
		g.RotationPeriod = req.RotationPeriod
	}
	writeJSON(w, http.StatusOK, g)
}

// ─── encrypt / decrypt ───────────────────────────────────────────────

func (srv *Server) encrypt(w http.ResponseWriter, r *http.Request, keyID string) {
	var req kmsraw.EncryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	plain, err := base64.StdEncoding.DecodeString(req.Plaintext)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plaintext not base64: "+err.Error())
		return
	}
	res, err := srv.k.Encrypt(r.Context(), keyID, plain)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &kmsraw.EncryptResponse{
		Name:       gcpKeyResourceName(r, keyID),
		Ciphertext: base64.StdEncoding.EncodeToString(res.Ciphertext),
	})
}

func (srv *Server) decrypt(w http.ResponseWriter, r *http.Request, keyID string) {
	var req kmsraw.DecryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ct, err := base64.StdEncoding.DecodeString(req.Ciphertext)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ciphertext not base64: "+err.Error())
		return
	}
	res, err := srv.k.Decrypt(r.Context(), keyID, ct)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &kmsraw.DecryptResponse{
		Plaintext:   base64.StdEncoding.EncodeToString(res.Plaintext),
		UsedPrimary: true,
	})
}

func gcpKeyResourceName(r *http.Request, keyID string) string {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/")
	for _, sfx := range []string{":encrypt", ":decrypt"} {
		rest = strings.TrimSuffix(rest, sfx)
	}
	return rest
}

// ─── converters / helpers ────────────────────────────────────────────

func domainKeyToGCP(k domain.Key, name string) *kmsraw.CryptoKey {
	ck := &kmsraw.CryptoKey{
		Name:       name,
		Purpose:    "ENCRYPT_DECRYPT",
		CreateTime: k.CreatedAt.UTC().Format(time.RFC3339),
		Labels:     k.Tags,
	}
	return ck
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

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

func writeErr(w http.ResponseWriter, status int, msg string) {
	st := "INVALID_ARGUMENT"
	switch status {
	case http.StatusNotFound:
		st = "NOT_FOUND"
	case http.StatusConflict:
		st = "ALREADY_EXISTS"
	case http.StatusMethodNotAllowed:
		st = "FAILED_PRECONDITION"
	case http.StatusInternalServerError:
		st = "INTERNAL"
	}
	writeJSON(w, status, gcpError{Error: gcpErrorBody{Code: status, Message: msg, Status: st}})
}

func writeKMSErr(w http.ResponseWriter, err error) {
	switch {
	case domain.IsNotFound(err):
		writeErr(w, http.StatusNotFound, err.Error())
	case domain.IsAlreadyExists(err):
		writeErr(w, http.StatusConflict, err.Error())
	case domain.IsNotSupported(err):
		writeErr(w, http.StatusBadRequest, err.Error())
	case domain.IsInvalidInput(err), domain.IsKeyDisabled(err):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

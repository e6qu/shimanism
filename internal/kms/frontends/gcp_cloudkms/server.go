// Package gcp_cloudkms is the GCP Cloud KMS frontend for shimanism's
// key-management service (Phase 19). It speaks the Cloud KMS v1 REST
// protocol that google.golang.org/api/cloudkms/v1 and `gcloud kms`
// drive, and translates onto the neutral domain.KMS interface.
//
// Cloud KMS has a project/location/keyRing/cryptoKey/cryptoKeyVersion
// hierarchy the flat domain doesn't model. The keyRing is a GCP-specific
// container with no cross-cloud data-plane analog (see INTERSECTION.md);
// it is tracked honestly via domain.KMS CreateKeyRing/GetKeyRing — real on
// backends that can hold state (inmem, native GCP), NotSupported on those
// that can't (AWS/Azure) — never synthesized. A cryptoKey maps to a domain
// key: the user-chosen cryptoKeyId becomes the domain key ID.
// Encrypt/Decrypt target a cryptoKey (its primary version, implicitly).
//
// Plaintext/ciphertext ride the wire as base64 strings (REST `bytes`).
package gcp_cloudkms

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"net/http"
	"strings"
	"time"

	kmsraw "google.golang.org/api/cloudkms/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/kms/domain"
)

// crc32cTable is the Castagnoli polynomial table Cloud KMS uses for its
// data-integrity checksums. crc32c reduces a byte slice to the int64
// shape the Cloud KMS wire types carry (`,string`-encoded).
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

func crc32c(b []byte) int64 { return int64(crc32.Checksum(b, crc32cTable)) }

// decodeBytes decodes a proto3-JSON `bytes` field. The spec accepts both
// standard and URL-safe base64, with or without padding — `gcloud`
// encodes request bytes URL-safe while the Go SDK uses standard — so the
// frontend must accept all four variants.
func decodeBytes(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding,
		base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("not valid base64 (standard or URL-safe)")
}

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
	// hashicorp/google builds some sub-resource URLs (e.g. the
	// cryptoKeyVersions list) by appending another `v1/` onto a
	// custom endpoint that already carries the version segment. Tolerate
	// the doubled prefix so those reads route correctly.
	rest = strings.TrimPrefix(rest, "v1/")

	// Colon actions first.
	switch {
	case strings.HasSuffix(rest, ":encrypt") && r.Method == http.MethodPost:
		srv.encrypt(w, r, gcpKeyName(strings.TrimSuffix(rest, ":encrypt")))
		return
	case strings.HasSuffix(rest, ":decrypt") && r.Method == http.MethodPost:
		srv.decrypt(w, r, gcpKeyName(strings.TrimSuffix(rest, ":decrypt")))
		return
	case strings.HasSuffix(rest, ":destroy") && r.Method == http.MethodPost:
		srv.destroyCryptoKeyVersion(w, r, strings.TrimSuffix(rest, ":destroy"))
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
			// cryptoKeyVersions sub-resource: ckTail is
			// "{key}/cryptoKeyVersions[/{n}]".
			if kv := strings.Index(ckTail, "/cryptoKeyVersions"); kv >= 0 && r.Method == http.MethodGet {
				key := ckTail[:kv]
				ver := strings.TrimPrefix(ckTail[kv+len("/cryptoKeyVersions"):], "/")
				srv.getCryptoKeyVersions(w, r, key, ver, rest)
				return
			}
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

// ─── keyRings (GCP-only container; tracked honestly via the backend) ──

func (srv *Server) createKeyRing(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("keyRingId")
	parent := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "/keyRings")
	name := parent + "/keyRings/" + id
	kr, err := srv.k.CreateKeyRing(r.Context(), name)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, domainKeyRingToGCP(kr, name))
}

func (srv *Server) getKeyRing(w http.ResponseWriter, r *http.Request, rest string) {
	kr, err := srv.k.GetKeyRing(r.Context(), rest)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, domainKeyRingToGCP(kr, rest))
}

func domainKeyRingToGCP(kr domain.KeyRing, name string) *kmsraw.KeyRing {
	out := &kmsraw.KeyRing{Name: name}
	if kr.Name != "" {
		out.Name = kr.Name
	}
	if !kr.CreatedAt.IsZero() {
		out.CreateTime = kr.CreatedAt.UTC().Format(time.RFC3339)
	}
	return out
}

// ─── cryptoKeys ──────────────────────────────────────────────────────

func (srv *Server) createCryptoKey(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("cryptoKeyId")
	parent := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/"), "/cryptoKeys")
	// Cloud KMS requires the parent keyRing to exist. Enforce it on
	// backends that track rings; backends with no keyRing concept return
	// ErrNotSupported, in which case the ring is just path decoration and
	// the check is skipped.
	if _, err := srv.k.GetKeyRing(r.Context(), parent); err != nil && !domain.IsNotSupported(err) {
		writeKMSErr(w, err)
		return
	}
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

// getCryptoKeyVersions serves the cryptoKeyVersions list (ver == "") and
// single-version get. The flat domain models one implicit primary
// version per symmetric key, so the frontend synthesizes version "1".
// hashicorp/google lists versions during google_kms_crypto_key reads.
func (srv *Server) getCryptoKeyVersions(w http.ResponseWriter, r *http.Request, keyID, ver, rest string) {
	key, err := srv.k.DescribeKey(r.Context(), keyID)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	keyName := strings.SplitN(rest, "/cryptoKeyVersions", 2)[0]
	if ver == "" {
		writeJSON(w, http.StatusOK, &kmsraw.ListCryptoKeyVersionsResponse{
			CryptoKeyVersions: []*kmsraw.CryptoKeyVersion{primaryVersion(keyName, key)},
			TotalSize:         1,
		})
		return
	}
	writeJSON(w, http.StatusOK, primaryVersion(keyName, key))
}

// destroyCryptoKeyVersion handles cryptoKeyVersions.destroy. Per the
// published normalization it maps onto domain.ScheduleKeyDeletion (the
// key's single primary version is scheduled for destruction). The
// response carries the version with the backend's real DESTROY_SCHEDULED
// state and destroyTime. versionPath is the full version resource name
// (`.../cryptoKeys/{key}/cryptoKeyVersions/{n}`).
func (srv *Server) destroyCryptoKeyVersion(w http.ResponseWriter, r *http.Request, versionPath string) {
	keyID := strings.SplitN(gcpKeyName(versionPath), "/cryptoKeyVersions", 2)[0]
	key, err := srv.k.ScheduleKeyDeletion(r.Context(), keyID, 0)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	v := primaryVersion(strings.SplitN(versionPath, "/cryptoKeyVersions", 2)[0], key)
	v.Name = versionPath
	if !key.DeletionDate.IsZero() {
		v.DestroyTime = key.DeletionDate.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, v)
}

// primaryVersion maps the domain key's single primary version onto the
// Cloud KMS CryptoKeyVersion shape. Timestamps and state come from the
// backend's real key metadata, not synthesized values; the flat domain
// models exactly one (the primary) symmetric version per key.
func primaryVersion(keyName string, k domain.Key) *kmsraw.CryptoKeyVersion {
	created := k.CreatedAt.UTC().Format(time.RFC3339)
	return &kmsraw.CryptoKeyVersion{
		Name:            keyName + "/cryptoKeyVersions/1",
		State:           gcpVersionState(k.State),
		ProtectionLevel: "SOFTWARE",
		Algorithm:       "GOOGLE_SYMMETRIC_ENCRYPTION",
		CreateTime:      created,
		GenerateTime:    created,
	}
}

// gcpVersionState maps a domain key state onto the Cloud KMS
// CryptoKeyVersion state vocabulary.
func gcpVersionState(s domain.KeyState) string {
	switch s {
	case domain.KeyStateDisabled:
		return "DISABLED"
	case domain.KeyStatePendingDeletion:
		return "DESTROY_SCHEDULED"
	default:
		return "ENABLED"
	}
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
	plain, err := decodeBytes(req.Plaintext)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "plaintext not base64: "+err.Error())
		return
	}
	// Additional authenticated data is out of intersection (the domain
	// has no AAD channel); honoring it would silently change the crypto,
	// so a non-empty AAD is rejected rather than ignored.
	aad, err := decodeBytes(req.AdditionalAuthenticatedData)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "additional_authenticated_data not base64: "+err.Error())
		return
	}
	if len(aad) > 0 {
		writeErr(w, http.StatusBadRequest, "additional_authenticated_data is not supported")
		return
	}
	// Cloud KMS data-integrity: when the client sends the *_crc32c fields,
	// the server verifies them and reports the result via the
	// verified_*_crc32c flags. A mismatch is INVALID_ARGUMENT. `gcloud`
	// always sends both plaintext and (empty-)AAD checksums and requires
	// both verified flags true.
	if req.PlaintextCrc32c != 0 && req.PlaintextCrc32c != crc32c(plain) {
		writeErr(w, http.StatusBadRequest,
			"plaintext_crc32c mismatch: request corrupted in-transit")
		return
	}
	if req.AdditionalAuthenticatedDataCrc32c != 0 && req.AdditionalAuthenticatedDataCrc32c != crc32c(aad) {
		writeErr(w, http.StatusBadRequest,
			"additional_authenticated_data_crc32c mismatch: request corrupted in-transit")
		return
	}
	res, err := srv.k.Encrypt(r.Context(), keyID, plain)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &kmsraw.EncryptResponse{
		Name:                    gcpKeyResourceName(r, keyID),
		Ciphertext:              base64.StdEncoding.EncodeToString(res.Ciphertext),
		CiphertextCrc32c:        crc32c(res.Ciphertext),
		VerifiedPlaintextCrc32c: true,
		VerifiedAdditionalAuthenticatedDataCrc32c: true,
	})
}

func (srv *Server) decrypt(w http.ResponseWriter, r *http.Request, keyID string) {
	var req kmsraw.DecryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	ct, err := decodeBytes(req.Ciphertext)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ciphertext not base64: "+err.Error())
		return
	}
	// Cloud KMS data-integrity: verify the client-supplied
	// ciphertext_crc32c before doing any work. A mismatch is
	// INVALID_ARGUMENT.
	if req.CiphertextCrc32c != 0 && req.CiphertextCrc32c != crc32c(ct) {
		writeErr(w, http.StatusBadRequest,
			"ciphertext_crc32c mismatch: request corrupted in-transit")
		return
	}
	res, err := srv.k.Decrypt(r.Context(), keyID, ct)
	if err != nil {
		writeKMSErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &kmsraw.DecryptResponse{
		Plaintext:       base64.StdEncoding.EncodeToString(res.Plaintext),
		PlaintextCrc32c: crc32c(res.Plaintext),
		UsedPrimary:     true,
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

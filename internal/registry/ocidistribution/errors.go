package ocidistribution

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

// Local sentinel errors specific to the /v2/ protocol layer (the domain
// sentinels cover backend-level conditions).
var (
	// ErrInvalidDigest is a malformed "<alg>:<hex>" digest.
	ErrInvalidDigest = errors.New("invalid digest")
	// ErrDigestMismatch re-exports the domain mismatch sentinel so this
	// package's callers can match on either.
	ErrDigestMismatch = domain.ErrDigestMismatch
)

// OCI error codes (subset in the registry intersection). See the OCI
// Distribution Spec § error-codes.
const (
	codeBlobUnknown     = "BLOB_UNKNOWN"
	codeBlobUploadInval = "BLOB_UPLOAD_INVALID"
	codeDigestInvalid   = "DIGEST_INVALID"
	codeManifestUnknown = "MANIFEST_UNKNOWN"
	codeNameUnknown     = "NAME_UNKNOWN"
	codeNameInvalid     = "NAME_INVALID"
	codeUnsupported     = "UNSUPPORTED"
)

// ociError is one entry in the OCI error envelope.
type ociError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ociErrors is the `{"errors":[...]}` envelope OCI clients expect.
type ociErrors struct {
	Errors []ociError `json:"errors"`
}

// writeOCIError emits the OCI error envelope with the given HTTP status.
func writeOCIError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ociErrors{Errors: []ociError{{Code: code, Message: msg}}})
}

// writeDomainError maps a domain/protocol error onto the OCI envelope with
// the appropriate status and code. notFoundCode lets the caller pick the
// resource-specific not-found code (BLOB_UNKNOWN vs MANIFEST_UNKNOWN).
func writeDomainError(w http.ResponseWriter, err error, notFoundCode string) {
	switch {
	case domain.IsNotFound(err):
		writeOCIError(w, http.StatusNotFound, notFoundCode, err.Error())
	case domain.IsDigestMismatch(err), errors.Is(err, ErrInvalidDigest):
		writeOCIError(w, http.StatusBadRequest, codeDigestInvalid, err.Error())
	case domain.IsInvalidInput(err):
		writeOCIError(w, http.StatusBadRequest, codeNameInvalid, err.Error())
	case domain.IsNotSupported(err):
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, err.Error())
	case domain.IsAlreadyExists(err):
		writeOCIError(w, http.StatusConflict, codeBlobUploadInval, err.Error())
	default:
		writeOCIError(w, http.StatusInternalServerError, codeUnsupported, err.Error())
	}
}

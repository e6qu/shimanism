package gcp_secretmanager

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// gcpErrorResponse is the JSON envelope GCP services return on
// failure. The SDK matches on `error.status` (the canonical gRPC
// status enum encoded as a string — `NOT_FOUND`, `INVALID_ARGUMENT`,
// `ALREADY_EXISTS`, etc.) and the HTTP status code.
type gcpErrorResponse struct {
	Error gcpError `json:"error"`
}

type gcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status,omitempty"`
}

func writeError(w http.ResponseWriter, status int, gcpStatus, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(gcpErrorResponse{Error: gcpError{
		Code:    status,
		Message: message,
		Status:  gcpStatus,
	}})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchSecret, domain.KindNoSuchVersion:
		writeError(w, http.StatusNotFound, "NOT_FOUND", de.Error())
	case domain.KindSecretAlreadyExists:
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", de.Error())
	case domain.KindSecretBeingDeleted:
		writeError(w, http.StatusFailedDependency, "FAILED_PRECONDITION", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", de.Error())
	}
}

package azure_keyvault

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// kvErrorResponse mirrors the JSON shape Azure Key Vault returns on
// failure. The SDK matches on `error.code` (e.g. SecretNotFound,
// ObjectIsBeingDeleted, BadParameter) and the HTTP status.
type kvErrorResponse struct {
	Error kvError `json:"error"`
}

type kvError struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	InnerError *kvError `json:"innererror,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(kvErrorResponse{Error: kvError{
		Code:    code,
		Message: message,
	}})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchSecret, domain.KindNoSuchVersion:
		writeError(w, http.StatusNotFound, "SecretNotFound", de.Error())
	case domain.KindSecretAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Error())
	case domain.KindSecretBeingDeleted:
		writeError(w, http.StatusConflict, "ObjectIsBeingDeleted", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "BadParameter", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", de.Error())
	}
}

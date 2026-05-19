package azure_servicebus

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

type sbErrorResponse struct {
	Error sbError `json:"error"`
}

type sbError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(sbErrorResponse{Error: sbError{
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
	case domain.KindNoSuchQueue:
		writeError(w, http.StatusNotFound, "ResourceNotFound", de.Error())
	case domain.KindQueueAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Error())
	case domain.KindInvalidReceiptHandle:
		writeError(w, http.StatusGone, "MessageLockLost", de.Error())
	case domain.KindMessageTooLarge:
		writeError(w, http.StatusRequestEntityTooLarge, "MessageTooLarge", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "BadRequest", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", de.Error())
	}
}

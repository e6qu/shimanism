package gcp_pubsub

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

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
	case domain.KindNoSuchQueue:
		writeError(w, http.StatusNotFound, "NOT_FOUND", de.Error())
	case domain.KindQueueAlreadyExists:
		writeError(w, http.StatusConflict, "ALREADY_EXISTS", de.Error())
	case domain.KindInvalidReceiptHandle:
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", de.Error())
	case domain.KindMessageTooLarge:
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL", de.Error())
	}
}

package aws_lambda

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

type lambdaError struct {
	Type    string `json:"Type,omitempty"`
	Message string `json:"Message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Amzn-Errortype", code)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(lambdaError{Type: code, Message: message})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "ServiceException", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchFunction:
		writeError(w, http.StatusNotFound, "ResourceNotFoundException", de.Error())
	case domain.KindFunctionAlreadyExists:
		writeError(w, http.StatusConflict, "ResourceConflictException", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidParameterValueException", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "ServiceException", de.Error())
	}
}

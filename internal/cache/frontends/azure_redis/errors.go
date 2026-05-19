package azure_redis

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/cache/domain"
)

type armErrorResponse struct {
	Error armError `json:"error"`
}

type armError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(armErrorResponse{Error: armError{
		Code: code, Message: message,
	}})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchInstance:
		writeError(w, http.StatusNotFound, "ResourceNotFound", de.Error())
	case domain.KindInstanceAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Error())
	default:
		writeError(w, http.StatusBadRequest, "BadRequest", de.Error())
	}
}

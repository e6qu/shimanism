package azure_containerapps

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/e6qu/shimanism/internal/functions/domain"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "read body: "+err.Error())
		return false
	}
	if len(body) == 0 {
		return true
	}
	if err := json.Unmarshal(body, target); err != nil {
		writeError(w, http.StatusBadRequest, "BadRequest", "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

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
	case domain.KindNoSuchFunction:
		writeError(w, http.StatusNotFound, "ResourceNotFound", de.Error())
	case domain.KindFunctionAlreadyExists:
		writeError(w, http.StatusConflict, "Conflict", de.Error())
	default:
		writeError(w, http.StatusBadRequest, "BadRequest", de.Error())
	}
}

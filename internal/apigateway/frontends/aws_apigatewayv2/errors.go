package aws_apigatewayv2

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
)

type apigwError struct {
	Message string `json:"Message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Amzn-Errortype", code)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apigwError{Message: message})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServerErrorException", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchGateway:
		writeError(w, http.StatusNotFound, "NotFoundException", de.Error())
	case domain.KindGatewayAlreadyExists:
		writeError(w, http.StatusConflict, "ConflictException", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "BadRequestException", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServerErrorException", de.Error())
	}
}

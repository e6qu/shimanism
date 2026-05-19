package aws_secretsmanager

import (
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/secrets/domain"
)

// awsJsonError is the JSON shape every AWS Secrets Manager error
// response carries. The SDK matches the `__type` field against the
// per-operation error union declared in the Smithy spec.
type awsJsonError struct {
	Type    string `json:"__type"`
	Message string `json:"Message"`
}

// writeError serialises an AWS-shaped error. `code` is the AWS
// exception short name (e.g. "ResourceNotFoundException"). HTTP
// status reflects the error severity (400 for client, 500 for
// server).
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	// AWS surfaces the error code via the response's x-amzn-ErrorType
	// header in addition to the JSON body's __type field. SDK clients
	// use either; we set both so the choice doesn't matter.
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	_ = encodeJSON(w, awsJsonError{Type: code, Message: message})
}

// mapDomainError translates a domain.Error into the AWS Secrets
// Manager error vocabulary. Status codes match the documented
// per-error HTTP codes in the Smithy spec.
func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServiceError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchSecret, domain.KindNoSuchVersion:
		writeError(w, http.StatusBadRequest, "ResourceNotFoundException", de.Error())
	case domain.KindSecretAlreadyExists:
		writeError(w, http.StatusBadRequest, "ResourceExistsException", de.Error())
	case domain.KindSecretBeingDeleted:
		writeError(w, http.StatusBadRequest, "InvalidRequestException", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidParameterException", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServiceError", de.Error())
	}
}

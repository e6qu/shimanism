package aws_sqs_receive

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
)

type awsJsonError struct {
	Type    string `json:"__type"`
	Message string `json:"message,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(awsJsonError{
		Type:    "com.amazonaws.sqs#" + code,
		Message: message,
	})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServiceError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchSubscription:
		writeError(w, http.StatusBadRequest, "QueueDoesNotExist", de.Error())
	case domain.KindInvalidReceiptHandle:
		writeError(w, http.StatusBadRequest, "ReceiptHandleIsInvalid", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidAttributeValue", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServiceError", de.Error())
	}
}

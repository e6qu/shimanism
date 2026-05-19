package aws_sqs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

type awsJsonError struct {
	Type    string `json:"__type"`
	Message string `json:"Message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-ErrorType", code)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(awsJsonError{Type: code, Message: message})
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalServiceError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchQueue:
		// SQS uses the AmazonSQS-prefixed exception type for
		// queue-not-found; SDK clients match on the suffix.
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.sqs#QueueDoesNotExist", de.Error())
	case domain.KindQueueAlreadyExists:
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.sqs#QueueNameExists", de.Error())
	case domain.KindInvalidReceiptHandle:
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.sqs#ReceiptHandleIsInvalid", de.Error())
	case domain.KindMessageTooLarge:
		writeError(w, http.StatusBadRequest,
			"com.amazonaws.sqs#InvalidMessageContents", de.Error())
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidParameterValue", de.Error())
	default:
		writeError(w, http.StatusInternalServerError, "InternalServiceError", de.Error())
	}
}

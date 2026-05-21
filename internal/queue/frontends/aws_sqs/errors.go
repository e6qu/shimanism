package aws_sqs

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/queue/domain"
)

type awsJsonError struct {
	Type    string `json:"__type"`
	Message string `json:"Message"`
}

// legacyQueryErrorCode maps the canonical SQS Smithy error names to
// their legacy Query-XML codes. SQS is awsQueryCompatible — clients
// (including hashicorp/aws's wait functions) match on these legacy
// codes via the `x-amzn-Query-Error` response header. Without it the
// SDK still extracts the JSON `__type`, but per-error-code waiters
// in awsQueryCompatible code paths can miss the match.
var legacyQueryErrorCode = map[string]string{
	"QueueDoesNotExist":        "AWS.SimpleQueueService.NonExistentQueue",
	"QueueNameExists":          "QueueAlreadyExists",
	"ReceiptHandleIsInvalid":   "ReceiptHandleIsInvalid",
	"InvalidMessageContents":   "InvalidMessageContents",
	"OverLimit":                "OverLimit",
	"UnsupportedOperation":     "AWS.SimpleQueueService.UnsupportedOperation",
	"BatchEntryIdsNotDistinct": "AWS.SimpleQueueService.BatchEntryIdsNotDistinct",
	"InvalidIdFormat":          "InvalidIdFormat",
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	w.Header().Set("x-amzn-ErrorType", code)
	// Strip the Smithy namespace prefix for the legacy lookup.
	bare := code
	if i := strings.LastIndexByte(bare, '#'); i >= 0 {
		bare = bare[i+1:]
	}
	if legacy, ok := legacyQueryErrorCode[bare]; ok {
		w.Header().Set("x-amzn-query-error", legacy+";Sender")
	}
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

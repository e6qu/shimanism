// Package gcs is the GCS REST API frontend for shimanism's storage
// service. It speaks the JSON-over-HTTPS wire protocol that
// `cloud.google.com/go/storage`, `gcloud storage`, and the
// `hashicorp/google` Terraform provider all use, and translates
// each request into a call on the neutral `domain.Storage`
// interface — same backend set the AWS S3 frontend uses.
//
// Per AGENTS.md "Reuse over reinvention", the wire-type structs
// come from `google.golang.org/api/storage/v1` (the raw types the
// official SDK is generated from). The shim only emits the
// routing + dispatch + error-envelope layer.
package gcs

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// apiError mirrors the JSON shape `google-api-go-client` deserialises
// via `googleapi.Error`. Every non-2xx GCS response has this shape.
type apiError struct {
	Error apiErrorBody `json:"error"`
}

type apiErrorBody struct {
	Code    int                `json:"code"`
	Message string             `json:"message"`
	Errors  []apiErrorDetail   `json:"errors,omitempty"`
	Status  string             `json:"status,omitempty"`
}

type apiErrorDetail struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Domain  string `json:"domain,omitempty"`
}

// writeError serializes a GCS-shaped error response. `reason` is the
// short GCS reason code (e.g. "notFound", "conflict", "required") —
// SDK clients match on it via `googleapi.Error.Errors[i].Reason`.
func writeError(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	body := apiError{Error: apiErrorBody{
		Code:    status,
		Message: message,
		Errors:  []apiErrorDetail{{Reason: reason, Message: message, Domain: "global"}},
	}}
	_ = json.NewEncoder(w).Encode(body)
}

// mapDomainError translates a domain.Error into a GCS error response.
// Status codes and reason strings match what Google's services return
// for each condition.
func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "internalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchBucket, domain.KindNoSuchKey:
		writeError(w, http.StatusNotFound, "notFound", de.Message)
	case domain.KindBucketAlreadyExists:
		writeError(w, http.StatusConflict, "conflict", de.Message)
	case domain.KindBucketNotEmpty:
		writeError(w, http.StatusConflict, "conflict", de.Message)
	case domain.KindNoSuchUpload:
		writeError(w, http.StatusNotFound, "notFound", de.Message)
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "required", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "internalError", de.Message)
	}
}

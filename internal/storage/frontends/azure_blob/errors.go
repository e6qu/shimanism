// Package azure_blob is the Azure Blob Storage REST API frontend.
// It speaks the XML-over-HTTPS wire protocol that
// `azure-sdk-for-go/sdk/storage/azblob`, `az storage blob`, and the
// `hashicorp/azurerm` Terraform provider all use, and translates
// each request into a call on the neutral `domain.Storage`
// interface — same backend set the AWS S3 and GCS frontends use.
//
// Per AGENTS.md "Reuse over reinvention", the wire-type structs
// match the shapes the Azure SDK's internal `generated/` package
// uses; the shim emits the routing + dispatch + error-envelope
// layer hand-in-glove with the official OpenAPI v3 spec.
package azure_blob

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Azure error envelope. Returned as XML with content-type
// application/xml and a corresponding x-ms-error-code header. SDK
// clients match on the header for response parsing.
type apiError struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

// writeError serialises an Azure-shaped error response. `code` is
// the short Azure ErrorCode (e.g. "BlobNotFound", "ContainerAlready
// Exists") — SDK clients match on it via azcore.ResponseError.ErrorCode.
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-ms-error-code", code)
	w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(status)
	body := apiError{Code: code, Message: message}
	enc := xml.NewEncoder(w)
	_, _ = w.Write([]byte(xml.Header))
	_ = enc.Encode(body)
}

func mapDomainError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeError(w, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	switch de.Kind {
	case domain.KindNoSuchBucket:
		writeError(w, http.StatusNotFound, "ContainerNotFound", de.Message)
	case domain.KindNoSuchKey:
		writeError(w, http.StatusNotFound, "BlobNotFound", de.Message)
	case domain.KindBucketAlreadyExists:
		writeError(w, http.StatusConflict, "ContainerAlreadyExists", de.Message)
	case domain.KindBucketNotEmpty:
		writeError(w, http.StatusConflict, "ContainerNotEmpty", de.Message)
	case domain.KindNoSuchUpload:
		writeError(w, http.StatusNotFound, "BlobNotFound", de.Message)
	case domain.KindInvalidArgument:
		writeError(w, http.StatusBadRequest, "InvalidInput", de.Message)
	default:
		writeError(w, http.StatusInternalServerError, "InternalError", de.Message)
	}
}

// ensure fmt is used (helps when partial files compile during
// iterative builds).
var _ = fmt.Sprintf

package restxml

import (
	"encoding/xml"
	"errors"
	"net/http"
)

// Error is the on-the-wire shape AWS REST-XML error responses take.
// The wire form is:
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<Error>
//	  <Code>NoSuchBucket</Code>
//	  <Message>The specified bucket does not exist</Message>
//	  <BucketName>my-bucket</BucketName>
//	  <RequestId>abcd1234</RequestId>
//	</Error>
type Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

// WriteError emits the canonical AWS REST-XML error envelope at the
// given HTTP status with the given Code/Message. Backend errors should
// be mapped to a Code that matches one of the operation's declared
// error shapes in the spec; arbitrary errors get "InternalError".
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(Error{Code: code, Message: message})
	_ = enc.Flush()
}

// ShimError is the typed error backends return so generated handlers
// can map to the right S3-shaped HTTP status + error code. Backends
// that return a non-ShimError get HTTP 500 InternalError.
type ShimError struct {
	HTTPStatus int
	Code       string // S3 error code (NoSuchBucket, NoSuchKey, …)
	Message    string
	Resource   string
	RequestID  string
}

func (e *ShimError) Error() string {
	return e.Code + ": " + e.Message
}

// WriteBackendError centralises the backend-error → HTTP-response
// mapping. Every generated handler funnels backend errors through
// here so error fidelity stays in one place.
func WriteBackendError(w http.ResponseWriter, err error) {
	var se *ShimError
	if errors.As(err, &se) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(se.HTTPStatus)
		_, _ = w.Write([]byte(xml.Header))
		enc := xml.NewEncoder(w)
		_ = enc.Encode(Error{
			Code:      se.Code,
			Message:   se.Message,
			Resource:  se.Resource,
			RequestID: se.RequestID,
		})
		_ = enc.Flush()
		return
	}
	WriteError(w, http.StatusInternalServerError, "InternalError", err.Error())
}

// Common S3 error helpers — backends construct these instead of
// fmt.Errorf strings so generated handlers can route to the right
// status code.
func NoSuchBucket(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchBucket",
		Message: "The specified bucket does not exist", Resource: bucket}
}

func NoSuchKey(bucket, key string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchKey",
		Message: "The specified key does not exist", Resource: bucket + "/" + key}
}

func BucketAlreadyOwnedByYou(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusConflict, Code: "BucketAlreadyOwnedByYou",
		Message: "Your previous request to create the named bucket succeeded and you already own it",
		Resource: bucket}
}

func BucketNotEmpty(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusConflict, Code: "BucketNotEmpty",
		Message: "The bucket you tried to delete is not empty", Resource: bucket}
}

func NoSuchUpload(uploadID string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchUpload",
		Message: "The specified multipart upload does not exist", Resource: uploadID}
}

func InvalidArgument(message string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusBadRequest, Code: "InvalidArgument",
		Message: message}
}

// Per-feature "not configured" 404 errors. The TF AWS provider's
// resource Read step issues GetBucket* probes for each S3 feature
// (policy, tagging, lifecycle, …) and treats the corresponding
// NoSuchX 404 as "feature is at its default empty state." Backends
// without native equivalents return the appropriate one of these;
// that is universally correct because every cloud's freshly-created
// bucket has these features in their default-empty state.

func NoSuchBucketPolicy(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchBucketPolicy",
		Message: "The bucket policy does not exist", Resource: bucket}
}

func NoSuchTagSet(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchTagSet",
		Message: "There is no tag set associated with the bucket", Resource: bucket}
}

func NoSuchCORSConfiguration(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchCORSConfiguration",
		Message: "The CORS configuration does not exist", Resource: bucket}
}

func NoSuchLifecycleConfiguration(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchLifecycleConfiguration",
		Message: "The lifecycle configuration does not exist", Resource: bucket}
}

func ReplicationConfigurationNotFound(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "ReplicationConfigurationNotFoundError",
		Message: "The replication configuration was not found", Resource: bucket}
}

func NoSuchWebsiteConfiguration(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchWebsiteConfiguration",
		Message: "The website configuration does not exist", Resource: bucket}
}

func ServerSideEncryptionConfigurationNotFound(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "ServerSideEncryptionConfigurationNotFoundError",
		Message: "The server side encryption configuration was not found", Resource: bucket}
}

func ObjectLockConfigurationNotFound(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "ObjectLockConfigurationNotFoundError",
		Message: "Object Lock configuration does not exist for this bucket", Resource: bucket}
}

func OwnershipControlsNotFound(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "OwnershipControlsNotFoundError",
		Message: "The bucket ownership controls were not found", Resource: bucket}
}

func NoSuchPublicAccessBlockConfiguration(bucket string) *ShimError {
	return &ShimError{HTTPStatus: http.StatusNotFound, Code: "NoSuchPublicAccessBlockConfiguration",
		Message: "The public access block configuration was not found", Resource: bucket}
}

// Decoder is a marker type referenced by generated code so the
// restxml import never goes unused.
type Decoder struct{}

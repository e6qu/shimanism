package awsjson

import (
	"encoding/json"
	"net/http"
)

// errEnvelope is the on-the-wire shape awsJson1_x errors take:
//
//	HTTP/1.1 400 Bad Request
//	x-amzn-errortype: <ErrorTypeShortName>
//	Content-Type: application/x-amz-json-1.1
//
//	{
//	  "__type": "<ErrorTypeShortName>",
//	  "message": "<human-readable>"
//	}
//
// The `__type` field is the Smithy error shape's short name (or fully
// qualified shape ID, but AWS services in practice emit the short name).
// The `x-amzn-errortype` header carries the same value — some clients
// dispatch on the header without parsing the body. The SDK + the
// awsQueryCompatible legacy SDKs both consume this shape.
type errEnvelope struct {
	Type    string `json:"__type"`
	Message string `json:"message,omitempty"`
}

// WriteError emits the awsJson1_x error envelope at the given HTTP
// status with the given Smithy error short name + human message. The
// caller picks the status (400 for client-side faults, 5xx for the
// shim's own failures, 404 / 409 / 403 etc. for the operation's
// declared error shapes).
func WriteError(w http.ResponseWriter, status int, errType, message string) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.Header().Set("X-Amzn-Errortype", errType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errEnvelope{Type: errType, Message: message})
}

// WriteJSON emits the awsJson1_x success body for the given Go value.
// The protocol requires no XML preamble, no enveloping; the value
// serialises into the response body verbatim. Empty Go values
// (`struct{}{}`) serialise to `{}`, which is the protocol's "void"
// response.
func WriteJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON reads the request body into target. On malformed JSON it
// writes the protocol's `SerializationException` envelope and returns
// false; on success it returns true. Generated handlers use the
// boolean as a short-circuit (the response has already been sent on
// failure).
func DecodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) bool {
	if r.ContentLength == 0 {
		return true
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		WriteError(w, http.StatusBadRequest, "SerializationException", err.Error())
		return false
	}
	return true
}

// MissingRequiredField writes the awsJson1_x equivalent of "you didn't
// include a field the spec requires." The Smithy default error type
// is `ValidationException`, but many AWS services use service-specific
// types like `InvalidParameterException` — the generated handler picks
// based on the spec's declared errors. This helper is the generic
// fallback.
func MissingRequiredField(w http.ResponseWriter, fieldName string) {
	WriteError(w, http.StatusBadRequest, "ValidationException",
		"missing required field: "+fieldName)
}

// InvalidParameter writes the awsJson1_x InvalidParameterException —
// the conventional name for "a field's value doesn't match the spec's
// constraints" (bad enum, length out of range, regex mismatch).
func InvalidParameter(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusBadRequest, "InvalidParameterException", message)
}

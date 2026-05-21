package awsquery

import (
	"encoding/xml"
	"errors"
	"net/http"
)

// errEnvelope is the on-the-wire shape awsQuery error responses take:
//
//	HTTP/1.1 400 Bad Request
//	Content-Type: text/xml
//
//	<?xml version="1.0"?>
//	<ErrorResponse xmlns="...">
//	  <Error>
//	    <Type>Sender</Type>
//	    <Code>NotFoundException</Code>
//	    <Message>...</Message>
//	  </Error>
//	  <RequestId>00000000-0000-0000-0000-000000000000</RequestId>
//	</ErrorResponse>
//
// The Type field is "Sender" for client-side faults, "Receiver" for
// server-side faults. The Code is the Smithy error short name.
type errEnvelope struct {
	XMLName   xml.Name `xml:"ErrorResponse"`
	Error     errBody  `xml:"Error"`
	RequestID string   `xml:"RequestId"`
}

type errBody struct {
	Type    string `xml:"Type"`
	Code    string `xml:"Code"`
	Message string `xml:"Message,omitempty"`
}

// WriteError emits the awsQuery error envelope at the given HTTP
// status. `errType` is "Sender" or "Receiver"; `code` is the Smithy
// error short name.
func WriteError(w http.ResponseWriter, status int, errType, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	env := errEnvelope{
		Error:     errBody{Type: errType, Code: code, Message: message},
		RequestID: "00000000-0000-0000-0000-000000000000",
	}
	_ = xml.NewEncoder(w).Encode(env)
}

// BackendError is returned by generated handlers when the backend
// surfaces a typed error that maps cleanly to one of the operation's
// declared Smithy error shapes. The HTTP status, error type, and
// Smithy-canonical code get written via WriteBackendError.
type BackendError struct {
	HTTPStatus int
	Type       string // "Sender" or "Receiver".
	Code       string // Smithy error short name.
	Message    string
}

func (e *BackendError) Error() string { return e.Code + ": " + e.Message }

// WriteBackendError centralises the backend-error → HTTP-response
// mapping for awsQuery. Unknown errors fall through to a 500
// InternalFailure envelope.
func WriteBackendError(w http.ResponseWriter, err error) {
	var be *BackendError
	if errors.As(err, &be) {
		t := be.Type
		if t == "" {
			t = "Sender"
		}
		WriteError(w, be.HTTPStatus, t, be.Code, be.Message)
		return
	}
	WriteError(w, http.StatusInternalServerError, "Receiver", "InternalFailure", err.Error())
}

// WriteResult writes a successful awsQuery operation response. The
// envelope wraps the per-op result in `<OpResponse><OpResult>...
// </OpResult><ResponseMetadata><RequestId>...</RequestId>
// </ResponseMetadata></OpResponse>`. `opName` is the operation's
// short name; the OpResponse wrapper uses `<OpName>Response`, the
// OpResult uses `<OpName>Result`.
func WriteResult(w http.ResponseWriter, opName string, result interface{}) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	// Open the OpResponse wrapper element.
	_, _ = w.Write([]byte("<" + opName + "Response>"))
	if result != nil {
		// Open OpResult, encode the user value's fields inside, close OpResult.
		_, _ = w.Write([]byte("<" + opName + "Result>"))
		_ = xml.NewEncoder(w).Encode(result)
		_, _ = w.Write([]byte("</" + opName + "Result>"))
	} else {
		_, _ = w.Write([]byte("<" + opName + "Result/>"))
	}
	_, _ = w.Write([]byte("<ResponseMetadata><RequestId>00000000-0000-0000-0000-000000000000</RequestId></ResponseMetadata>"))
	_, _ = w.Write([]byte("</" + opName + "Response>"))
}

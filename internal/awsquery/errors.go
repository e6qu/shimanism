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
//
// The result's fields are inlined into `<OpName>Result>` — the
// helper strips the result struct's own outer element (Go's default
// xml.Marshal wraps in a `<StructTypeName>` element) so the output
// matches what AWS clients expect for the awsQuery wire shape.
func WriteResult(w http.ResponseWriter, opName string, result interface{}) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write([]byte("<" + opName + "Response>"))
	if result != nil {
		inner, _ := marshalInner(result)
		if len(inner) > 0 {
			_, _ = w.Write([]byte("<" + opName + "Result>"))
			_, _ = w.Write(inner)
			_, _ = w.Write([]byte("</" + opName + "Result>"))
		} else {
			_, _ = w.Write([]byte("<" + opName + "Result/>"))
		}
	} else {
		_, _ = w.Write([]byte("<" + opName + "Result/>"))
	}
	_, _ = w.Write([]byte("<ResponseMetadata><RequestId>00000000-0000-0000-0000-000000000000</RequestId></ResponseMetadata>"))
	_, _ = w.Write([]byte("</" + opName + "Response>"))
}

// marshalInner serialises a value via xml.Marshal and strips its
// outer element so the fields are inline-emittable inside the
// awsQuery OpResult wrapper.
func marshalInner(v interface{}) ([]byte, error) {
	data, err := xml.Marshal(v)
	if err != nil {
		return nil, err
	}
	// Find the end of the opening tag and the start of the closing tag.
	first := indexOf(data, '>')
	if first < 0 {
		return nil, nil
	}
	last := lastIndexOf(data, '<')
	if last < 0 || last <= first {
		return nil, nil
	}
	return data[first+1 : last], nil
}

func indexOf(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func lastIndexOf(b []byte, c byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == c {
			return i
		}
	}
	return -1
}

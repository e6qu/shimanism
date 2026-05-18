package restxml

import (
	"encoding/xml"
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

// Decoder is a marker type referenced by generated code so the
// restxml import never goes unused.
type Decoder struct{}

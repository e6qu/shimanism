// Package gen contains the generated Go server stubs for the storage
// service. Files ending in `.gen.go` are produced by `make codegen` and
// must not be hand-edited; non-`.gen.go` files (like this one) are the
// hand-written runtime helpers the generated code calls into.
package gen

import (
	"encoding/xml"
	"net/http"
)

// awsErrorXML is the on-the-wire shape S3 errors take. Same response
// envelope across the AWS REST-XML protocol family; we keep it minimal
// here and expand as conformance reveals what each backend needs.
type awsErrorXML struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource,omitempty"`
	RequestID string   `xml:"RequestId,omitempty"`
}

// writeAWSError emits an S3-shaped error response. Used by every
// generated handler when the request can't be honored.
//
// Phase 1.3 pilot: the helper exists so the generated code compiles and
// has a single, consistent error path. Phase 1.4 (conformance harness)
// will validate the on-the-wire shape against `aws-sdk-go-v2`'s
// expectations.
func writeAWSError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(awsErrorXML{Code: code, Message: message})
	_ = enc.Flush()
}

// Package ec2query is the runtime helper for shimanism handlers
// generated against AWS Smithy services that use the ec2Query
// protocol — one HTTP endpoint (POST /) with an
// `application/x-www-form-urlencoded` request body whose `Action`
// field carries the operation name, and XML responses wrapped in
// `<OpNameResponse xmlns="..."><requestId>...</requestId>...fields...
// </OpNameResponse>`.
//
// The ec2Query protocol is a variant of awsQuery with two key
// differences:
//
//  1. List serialization is flattened — `Field.N` rather than the
//     `Field.member.N` shape awsQuery uses.
//  2. The error envelope is `<Response><Errors><Error><Code>...
//     </Code></Error></Errors><RequestID>...</RequestID></Response>`
//     rather than awsQuery's `<ErrorResponse><Error><Type>...`.
//
// Protocol reference:
//
//   - https://smithy.io/2.0/aws/protocols/aws-ec2-query-protocol.html
//
// AWS services on ec2Query: EC2 (networking + instances), VPC
// primitives, and related control-plane surfaces.
package ec2query

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"sync"
)

// Router dispatches ec2Query requests by the `Action` form parameter.
// The dispatch model is identical to awsquery.Router; only the error
// and success envelopes differ.
type Router struct {
	service string
	mu      sync.RWMutex
	routes  map[string]http.Handler
}

// NewRouter returns a Router for the given Smithy service short name
// (e.g. "ec2"). Used only in diagnostic messages.
func NewRouter(service string) *Router {
	return &Router{service: service, routes: map[string]http.Handler{}}
}

// Register binds an operation short-name (e.g. "DescribeInstances")
// to a handler.
func (rt *Router) Register(operation string, h http.Handler) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.routes[operation] = h
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "InvalidAction",
			"ec2Query requires POST")
		return
	}
	if err := r.ParseForm(); err != nil {
		WriteError(w, http.StatusBadRequest, "MalformedQueryString",
			"could not parse form body: "+err.Error())
		return
	}
	action := r.Form.Get("Action")
	if action == "" {
		WriteError(w, http.StatusBadRequest, "MissingAction",
			"request body is missing the Action parameter")
		return
	}
	rt.mu.RLock()
	h := rt.routes[action]
	rt.mu.RUnlock()
	if h == nil {
		WriteError(w, http.StatusBadRequest, "InvalidAction",
			"unknown operation: "+action)
		return
	}
	h.ServeHTTP(w, r)
}

// Decoder is a marker type referenced by generated code so the
// ec2query import never goes unused even when an emitted file has no
// handlers.
type Decoder struct{}

// ec2ErrEnvelope is the on-the-wire shape ec2Query error responses take:
//
//	HTTP/1.1 400 Bad Request
//	Content-Type: text/xml
//
//	<?xml version="1.0" encoding="UTF-8"?>
//	<Response>
//	  <Errors>
//	    <Error>
//	      <Code>InvalidInstanceID.NotFound</Code>
//	      <Message>The instance ID 'i-xxx' does not exist</Message>
//	    </Error>
//	  </Errors>
//	  <RequestID>00000000-0000-0000-0000-000000000000</RequestID>
//	</Response>
//
// Unlike awsQuery there is no `<Type>` field in the Error element.
type ec2ErrEnvelope struct {
	XMLName   xml.Name   `xml:"Response"`
	Errors    ec2ErrList `xml:"Errors"`
	RequestID string     `xml:"RequestID"`
}

type ec2ErrList struct {
	Error ec2ErrBody `xml:"Error"`
}

type ec2ErrBody struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message,omitempty"`
}

// WriteError emits the ec2Query error envelope at the given HTTP
// status. `code` is the AWS EC2 error code (e.g.
// "InvalidInstanceID.NotFound").
func WriteError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	env := ec2ErrEnvelope{
		Errors:    ec2ErrList{Error: ec2ErrBody{Code: code, Message: message}},
		RequestID: "00000000-0000-0000-0000-000000000000",
	}
	_ = xml.NewEncoder(w).Encode(env)
}

// BackendError is returned by generated handlers when the backend
// surfaces a typed error that maps to one of the operation's declared
// Smithy error shapes.
type BackendError struct {
	HTTPStatus int
	Code       string // EC2-canonical error code, e.g. "InvalidVpcID.NotFound".
	Message    string
}

func (e *BackendError) Error() string { return e.Code + ": " + e.Message }

// WriteBackendError maps a backend error to the ec2Query HTTP
// response. Unknown errors fall through to a 500 InternalError
// envelope.
func WriteBackendError(w http.ResponseWriter, err error) {
	var be *BackendError
	if errors.As(err, &be) {
		WriteError(w, be.HTTPStatus, be.Code, be.Message)
		return
	}
	WriteError(w, http.StatusInternalServerError, "InternalError", err.Error())
}

// EmitVerifierError adapts the 3-arg sigv4verifier.EmitError
// signature to ec2Query's WriteError. For sigv4 verifier failures
// the code maps directly from the verifier's emitted type. Per-
// adapter New() constructors use this:
//
//	sigv4verifier.Middleware(verifier, ec2query.EmitVerifierError)
func EmitVerifierError(w http.ResponseWriter, status int, errorType, message string) {
	WriteError(w, status, errorType, message)
}

// ec2Namespace is the XML namespace on all EC2 success responses.
const ec2Namespace = "http://ec2.amazonaws.com/doc/2016-11-15/"

// WriteResult writes a successful ec2Query operation response. The EC2
// protocol wraps the result in `<OpNameResponse xmlns="..."><requestId>
// ...</requestId>FIELDS</OpNameResponse>` — no separate `<OpNameResult>`
// wrapper (unlike awsQuery's `<OpNameResponse><OpNameResult>...</
// OpNameResult></OpNameResponse>` double-wrapping).
func WriteResult(w http.ResponseWriter, opName string, result interface{}) {
	w.Header().Set("Content-Type", "text/xml;charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write([]byte(`<` + opName + `Response xmlns="` + ec2Namespace + `">`))
	_, _ = w.Write([]byte(`<requestId>00000000-0000-0000-0000-000000000000</requestId>`))
	if result != nil {
		inner, _ := marshalInner(result)
		_, _ = w.Write(inner)
	}
	_, _ = w.Write([]byte(`</` + opName + `Response>`))
}

// marshalInner serialises a value via xml.Marshal and strips its
// outer element so the fields are inline-emittable inside the ec2Query
// response wrapper.
func marshalInner(v interface{}) ([]byte, error) {
	data, err := xml.Marshal(v)
	if err != nil {
		return nil, err
	}
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

// contextKey is unexported; the only way to get a Form out of context
// is via FormFromContext.
type contextKey struct{}

// WithForm stores the parsed form values on the context. Generated
// handlers call this before dispatching so adapters needing fine-
// grained access to fields the emitter doesn't decode (e.g. nested
// Filter structs) can retrieve the raw form.
func WithForm(ctx context.Context, form url.Values) context.Context {
	return context.WithValue(ctx, contextKey{}, form)
}

// FormFromContext returns the form values stored by WithForm, or nil
// if the context wasn't set up by a generated ec2Query handler.
func FormFromContext(ctx context.Context) url.Values {
	if v, ok := ctx.Value(contextKey{}).(url.Values); ok {
		return v
	}
	return nil
}

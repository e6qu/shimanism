// Package awsjson is the runtime helper for shimanism handlers
// generated against AWS Smithy services that use the awsJson1_0 or
// awsJson1_1 protocol — one HTTP endpoint (`POST /`), dispatch by the
// `X-Amz-Target: <Service>.<Operation>` header, JSON request and
// response bodies, JSON error envelopes with the `__type` field.
//
// Protocol references:
//
//   - https://smithy.io/2.0/aws/protocols/aws-json-1_0-protocol.html
//   - https://smithy.io/2.0/aws/protocols/aws-json-1_1-protocol.html
//
// The router is the awsJson sibling of internal/restxml's Router. It
// does the minimum the protocol requires; everything else (decode,
// validation, dispatch) is in generated handler code or the per-op
// translate.go.
package awsjson

import (
	"errors"
	"net/http"
	"strings"
	"sync"
)

// Router dispatches awsJson1_x requests by the `X-Amz-Target` header.
// Routes register a (service-short-name, operation-short-name) pair
// against an http.Handler; the router validates the header is well-
// formed, looks up the handler, and delegates.
type Router struct {
	service string
	mu      sync.RWMutex
	routes  map[string]http.Handler // operation short-name → handler
}

// NewRouter returns a Router for the given Smithy service short name
// (e.g. "SecretsManager"). The service name is matched against the
// `X-Amz-Target` header's left-hand side.
func NewRouter(service string) *Router {
	return &Router{service: service, routes: map[string]http.Handler{}}
}

// Register binds an operation short-name (e.g. "DescribeSecret") to a
// handler. Duplicate registration overwrites — codegen output should
// never register the same op twice; the silent overwrite is a guardrail
// against test-harness re-init, not a feature.
func (rt *Router) Register(operation string, h http.Handler) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.routes[operation] = h
}

// ServeHTTP implements http.Handler. Per the awsJson1_x specs:
//   - the request method is always POST,
//   - the path is always "/",
//   - the operation is identified solely by the X-Amz-Target header.
//
// Non-POST, non-root, missing-header, or unknown-target requests fail
// with the protocol's standard envelope, not a generic 404 / 405.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "UnknownOperationException",
			"awsJson1_x requires POST")
		return
	}
	target := r.Header.Get("X-Amz-Target")
	if target == "" {
		WriteError(w, http.StatusBadRequest, "InvalidSignatureException",
			"missing X-Amz-Target header")
		return
	}
	svc, op, ok := strings.Cut(target, ".")
	if !ok || svc == "" || op == "" {
		WriteError(w, http.StatusBadRequest, "UnknownOperationException",
			"malformed X-Amz-Target: "+target)
		return
	}
	if svc != rt.service {
		WriteError(w, http.StatusBadRequest, "UnknownOperationException",
			"X-Amz-Target service mismatch: got "+svc+", want "+rt.service)
		return
	}
	rt.mu.RLock()
	h := rt.routes[op]
	rt.mu.RUnlock()
	if h == nil {
		WriteError(w, http.StatusBadRequest, "UnknownOperationException",
			"unknown operation: "+op)
		return
	}
	h.ServeHTTP(w, r)
}

// BackendError is returned by generated handlers when the backend
// surfaces a typed error that maps cleanly to one of the operation's
// declared Smithy error shapes. The HTTP status and error type get
// written via WriteBackendError; the message lands in the JSON body.
//
// QueryCompatibleCode is set when the service is `awsQueryCompatible`
// (currently: SQS). The SDK's awsQuery-compatibility shim matches on
// the `x-amzn-query-error` header for the legacy code; the adapter
// fills this so the SDK's pre-Smithy waiters and error mappers see
// the expected code.
type BackendError struct {
	HTTPStatus          int
	Type                string // Smithy error short name (e.g. "ResourceNotFoundException").
	Message             string
	QueryCompatibleCode string // Legacy awsQuery code (e.g. "AWS.SimpleQueueService.NonExistentQueue"); blank for non-awsQueryCompatible services.
}

func (e *BackendError) Error() string { return e.Type + ": " + e.Message }

// WriteBackendError centralises the backend-error → HTTP-response
// mapping for awsJson1_x. Unknown errors fall through to a 500
// InternalFailure envelope (the awsJson conventional name for "we
// don't know what happened").
func WriteBackendError(w http.ResponseWriter, err error) {
	var be *BackendError
	if errors.As(err, &be) {
		if be.QueryCompatibleCode != "" {
			w.Header().Set("x-amzn-query-error", be.QueryCompatibleCode+";Sender")
		}
		WriteError(w, be.HTTPStatus, be.Type, be.Message)
		return
	}
	WriteError(w, http.StatusInternalServerError, "InternalFailure", err.Error())
}

// Marker referenced by generated code so the awsjson import never goes
// unused even when an emitted file has no handlers (e.g. types-only
// emission).
type Decoder struct{}

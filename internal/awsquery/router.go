// Package awsquery is the runtime helper for shimanism handlers
// generated against AWS Smithy services that use the awsQuery
// protocol — one HTTP endpoint (POST /) with a
// `application/x-www-form-urlencoded` request body whose `Action`
// field carries the operation name, and XML responses wrapped in
// `<OpResponse><OpResult>...</OpResult><ResponseMetadata><RequestId>...
// </RequestId></ResponseMetadata></OpResponse>`.
//
// Protocol reference:
//
//   - https://smithy.io/2.0/aws/protocols/aws-query-protocol.html
//
// AWS services on awsQuery include SNS, RDS, ElastiCache, EC2 (most
// of the pre-2018 surfaces). hashicorp/aws's Terraform provider drives
// these via the legacy code paths.
package awsquery

import (
	"net/http"
	"sync"
)

// Router dispatches awsQuery requests by the `Action` form
// parameter. Routes register an action name against an http.Handler;
// the router parses the form once, validates the Action, and delegates.
type Router struct {
	service string
	mu      sync.RWMutex
	routes  map[string]http.Handler
}

// NewRouter returns a Router for the given Smithy service short name
// (e.g. "sns"). Used only in diagnostic messages — awsQuery doesn't
// carry the service name on the wire.
func NewRouter(service string) *Router {
	return &Router{service: service, routes: map[string]http.Handler{}}
}

// Register binds an operation short-name (e.g. "Publish") to a
// handler.
func (rt *Router) Register(operation string, h http.Handler) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.routes[operation] = h
}

// ServeHTTP implements http.Handler.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Sender", "InvalidAction",
			"awsQuery requires POST")
		return
	}
	if err := r.ParseForm(); err != nil {
		WriteError(w, http.StatusBadRequest, "Sender", "MalformedQueryString",
			"could not parse form body: "+err.Error())
		return
	}
	action := r.Form.Get("Action")
	if action == "" {
		WriteError(w, http.StatusBadRequest, "Sender", "MissingAction",
			"request body is missing the Action parameter")
		return
	}
	rt.mu.RLock()
	h := rt.routes[action]
	rt.mu.RUnlock()
	if h == nil {
		WriteError(w, http.StatusBadRequest, "Sender", "InvalidAction",
			"unknown operation: "+action)
		return
	}
	h.ServeHTTP(w, r)
}

// Decoder is a marker type referenced by generated code so the
// awsquery import never goes unused even when an emitted file has
// no handlers.
type Decoder struct{}

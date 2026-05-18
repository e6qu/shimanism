// Package harness provides test-only fixtures for spinning up a real
// shimanism shim instance with a chosen backend, addressable over HTTP
// so external clients (aws-sdk-go-v2, aws-cli, Terraform AWS provider,
// etc.) can drive it through their standard endpoint-override paths.
//
// The harness is not for production use. It produces a real shim,
// but configured for short-lived test runs (random port, no
// signature validation, in-memory state via the chosen backend).
package harness

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/e6qu/shimanism/internal/restxml"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

// StorageServer is a started shim instance with its addressable URL.
type StorageServer struct {
	// URL points at the shim's root; pass it to clients via
	// `--endpoint-url` / `BaseEndpoint` / Terraform `endpoints { s3 = ... }`.
	URL string
	// Close shuts down the test server. Registered with t.Cleanup so
	// callers rarely need to invoke it directly.
	Close func()
}

// StartStorageServer starts a shim instance backed by the given
// implementation of the storage intersection. Registers cleanup on t.
// Every request is logged to t.Log so conformance failures show the
// exact sequence of operations the client drove.
func StartStorageServer(t *testing.T, backend storagegen.AmazonS3Backend) *StorageServer {
	t.Helper()
	router := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(router, backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: router})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// logRoundTrip logs each request through the harness. Lightweight —
// no body capture, just method + path + query + response status.
type logRoundTrip struct {
	t   *testing.T
	mux http.Handler
}

func (l *logRoundTrip) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w, status: 200}
	l.mux.ServeHTTP(sw, r)
	suffix := ""
	if r.URL.RawQuery != "" {
		suffix = "?" + r.URL.RawQuery
	}
	l.t.Logf("[harness] %s %s%s -> %d", r.Method, r.URL.Path, suffix, sw.status)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

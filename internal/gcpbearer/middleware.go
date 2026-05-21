package gcpbearer

import (
	"encoding/json"
	"net/http"
	"os"
)

// Middleware returns an http.Handler that wraps `next`, verifying
// the incoming request's Bearer token before delegating. Failed
// verification short-circuits with GCP's JSON error envelope.
//
// Bypass gated on SHIMANISM_TEST_UNAUTHENTICATED=1 (cached on first
// read). Conformance lanes that haven't yet adopted real signing
// set the env var; production deployments must not.
func Middleware(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bypass() {
				next.ServeHTTP(w, r)
				return
			}
			if err := v.Verify(r); err != nil {
				ve, ok := err.(*Error)
				if !ok {
					writeGCPError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
					return
				}
				writeGCPError(w, ve.HTTPStatus, ve.Status, ve.Message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// writeGCPError writes the GCP standard JSON error envelope:
//
//	{"error":{"code":<httpStatus>,"message":"...","status":"<STATUS>"}}
func writeGCPError(w http.ResponseWriter, code int, status, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    code,
			"message": message,
			"status":  status,
		},
	})
}

// bypass reads SHIMANISM_TEST_UNAUTHENTICATED on every call so
// per-test t.Setenv overrides take effect even after the harness
// init() set the var process-wide.
func bypass() bool {
	return os.Getenv("SHIMANISM_TEST_UNAUTHENTICATED") == "1"
}

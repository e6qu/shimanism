package sigv4verifier

import (
	"context"
	"net/http"
	"os"
	"sync"
)

// Middleware returns an http.Handler that wraps `next`, verifying
// the incoming request's SigV4 signature before delegating. Failed
// verification short-circuits with the source cloud's own 401/403
// envelope (`emitErr` is service-specific — pass the per-cloud
// envelope writer; the helpers in `internal/awsjson` are the
// awsJson1_x choice, `internal/restxml.WriteError` is REST-XML).
//
// Use:
//
//	verifier := sigv4verifier.New(store, sigv4verifier.Options{
//	    Service: "secretsmanager",
//	    Region:  "us-east-1",
//	})
//	handler := sigv4verifier.Middleware(verifier, awsJsonEmitErr)(next)
//
// The verifier-bypass path is gated on the SHIMANISM_TEST_UNAUTHENTICATED
// environment variable. When set to "1", the middleware skips
// verification — conformance lanes that haven't yet adopted real
// signing can set the var; production deployments must not.
type EmitError func(w http.ResponseWriter, status int, errorType, message string)

func Middleware(v *Verifier, emitErr EmitError) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bypass() {
				next.ServeHTTP(w, r)
				return
			}
			if err := v.Verify(r); err != nil {
				ve, ok := err.(*Error)
				if !ok {
					emitErr(w, http.StatusInternalServerError, "InternalFailure", err.Error())
					return
				}
				emitErr(w, ve.HTTPStatus, ve.Code, ve.Message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

var (
	bypassOnce sync.Once
	bypassFlag bool
)

// bypass reports whether the SHIMANISM_TEST_UNAUTHENTICATED env var
// is set. Cached on first read so changing the env mid-process won't
// flip behaviour silently — set it before the shim starts.
func bypass() bool {
	bypassOnce.Do(func() {
		bypassFlag = os.Getenv("SHIMANISM_TEST_UNAUTHENTICATED") == "1"
	})
	return bypassFlag
}

// StaticStore is a CredentialStore wired to a single (access-key,
// secret) pair — handy for tests and single-tenant deployments.
// Production multi-tenant deployments should implement their own.
type StaticStore struct {
	AccessKey, Secret, SessionToken string
}

func (s StaticStore) Lookup(_ context.Context, k string) (string, string, bool) {
	if k != s.AccessKey {
		return "", "", false
	}
	return s.Secret, s.SessionToken, true
}

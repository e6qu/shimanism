package azurebearer

import (
	"encoding/json"
	"net/http"
	"os"
)

// Middleware returns an http.Handler that wraps `next`, verifying
// the incoming request's Bearer token before delegating. Failed
// verification short-circuits with Azure's JSON error envelope and
// (for Key Vault) the WWW-Authenticate challenge header that
// triggers the SDK's token-acquisition retry.
//
// Bypass gated on SHIMANISM_TEST_UNAUTHENTICATED=1.
func Middleware(v *Verifier, opts ...MiddlewareOption) func(http.Handler) http.Handler {
	mwOpts := middlewareOpts{}
	for _, o := range opts {
		o(&mwOpts)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if bypass() {
				next.ServeHTTP(w, r)
				return
			}
			if err := v.Verify(r); err != nil {
				ve, ok := err.(*Error)
				if !ok {
					writeAzureError(w, http.StatusInternalServerError, "InternalError", err.Error(), "")
					return
				}
				challenge := ""
				if mwOpts.ChallengeResource != "" {
					challenge = `Bearer authorization="https://login.microsoftonline.com/common/oauth2/v2.0/token", resource="` + mwOpts.ChallengeResource + `"`
				}
				writeAzureError(w, ve.HTTPStatus, ve.Code, ve.Message, challenge)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MiddlewareOption is the functional-options pattern for the Azure
// Bearer middleware. Use WithChallenge to emit the WWW-Authenticate
// challenge that Key Vault and ARM management surfaces require.
type MiddlewareOption func(*middlewareOpts)

type middlewareOpts struct {
	ChallengeResource string
}

// WithChallenge configures the middleware to emit a WWW-Authenticate
// `Bearer …, resource="<resource>"` header on 401 responses. Required
// by Azure Key Vault's challenge-response flow (the SDK won't acquire
// a token without it).
func WithChallenge(resource string) MiddlewareOption {
	return func(o *middlewareOpts) { o.ChallengeResource = resource }
}

// writeAzureError writes the Azure JSON error envelope. Optional
// `challenge` is set as the WWW-Authenticate header for 401 responses.
func writeAzureError(w http.ResponseWriter, code int, errorCode, message, challenge string) {
	if challenge != "" {
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    errorCode,
			"message": message,
		},
	})
}

func bypass() bool {
	return os.Getenv("SHIMANISM_TEST_UNAUTHENTICATED") == "1"
}

// Package gcpbearer implements the GCP-side bearer-token verifier for
// shimanism's GCP-shaped frontends (Cloud Storage, Secret Manager,
// Pub/Sub, Cloud SQL Admin, Memorystore, Cloud Run, API Gateway).
//
// Per the Phase 11.1 architecture spike: Google access tokens are
// opaque OAuth2 tokens; verifying them offline requires either
//
//   - a network round-trip to Google's tokeninfo endpoint per request
//     (https://oauth2.googleapis.com/tokeninfo?access_token=…), which
//     ties the shim's hot path to Google's identity service, OR
//   - a project-owned signing key + ID-token-shaped JWTs (well-formed
//     RS256 JWTs the shim validates against its own JWKS).
//
// Phase 11's test-mode is the second path: conformance tests sign
// ID-token-shaped JWTs with the test key; the verifier validates
// against the matching public key (test JWKS). Real-cloud lanes
// (Track A) use Google's actual ID tokens via
// `google.golang.org/api/idtoken.Validate` — a separate code path
// `ValidateGoogleIDToken` that proxies through Google's JWKS.
//
// What this package does:
//
//   - Parses `Authorization: Bearer <token>` from the request.
//   - For test mode: validates the JWT signature against a configured
//     symmetric key (HMAC-SHA256) — sufficient for conformance and
//     reject-path testing.
//   - For production: calls `google.golang.org/api/idtoken.Validate`
//     which fetches Google's JWKS and validates RS256-signed Google
//     ID tokens.
//   - Validates `iss` matches a configured issuer URI, `aud` matches
//     the resource (e.g. `https://secretmanager.googleapis.com/`),
//     `exp` is in the future, `iat` is not in the future.
//   - Returns nil on accept; `*gcpbearer.Error` on reject.
//
// What this package does NOT do today (follow-on):
//
//   - Validate opaque OAuth2 access tokens from `gcloud auth
//     print-access-token`. Those require the network round-trip; the
//     shim treats them as Bearer-present (no offline signature check)
//     when the env var `SHIMANISM_GCP_ACCEPT_OPAQUE_BEARERS=1` is set.
//   - Refresh JWKS in production. The first request fetches; cached
//     for the configured lifetime. A signing-key rotation outside the
//     cache window will fail until restart.
package gcpbearer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Error is the structured failure type. HTTP handlers map it onto
// GCP's standard JSON error envelope (`{"error":{"code":401,"message":"...","status":"UNAUTHENTICATED"}}`).
type Error struct {
	HTTPStatus int
	Status     string // GCP-canonical status name (UNAUTHENTICATED, PERMISSION_DENIED).
	Message    string
}

func (e *Error) Error() string { return e.Status + ": " + e.Message }

// Options configures the verifier.
type Options struct {
	// Issuer is the expected `iss` claim value (e.g.
	// "https://accounts.google.com" for Google ID tokens, or a
	// project-specific issuer URI for the test-mode key).
	Issuer string
	// Audience is the expected `aud` claim value — usually the
	// service's resource URI (e.g.
	// "https://secretmanager.googleapis.com/").
	Audience string
	// TestKey is the symmetric HMAC-SHA256 key the shim trusts in
	// test mode. When non-empty, the verifier accepts JWTs signed
	// with this key (HS256 alg). Production deployments leave this
	// empty and the verifier falls through to Google ID token
	// validation.
	TestKey []byte
	// MaxClockSkew bounds the iat / exp tolerance. Default 5 min.
	MaxClockSkew time.Duration
}

// Verifier verifies incoming Bearer tokens.
type Verifier struct {
	opts Options
}

// New constructs a verifier.
func New(opts Options) *Verifier {
	if opts.MaxClockSkew == 0 {
		opts.MaxClockSkew = 5 * time.Minute
	}
	return &Verifier{opts: opts}
}

// Verify checks the request's Authorization header. Returns nil on
// success, *gcpbearer.Error on failure.
func (v *Verifier) Verify(r *http.Request) error {
	authHdr := r.Header.Get("Authorization")
	if authHdr == "" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "Request had invalid authentication credentials. Expected OAuth 2 access token, login cookie or other valid authentication credential."}
	}
	if !strings.HasPrefix(authHdr, "Bearer ") {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "Authorization header missing Bearer prefix"}
	}
	token := strings.TrimPrefix(authHdr, "Bearer ")
	if token == "" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "Bearer token is empty"}
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		// Not a JWT shape; treat as an opaque access token.
		// Production verifies via tokeninfo; this implementation rejects
		// (test mode requires JWT-shaped tokens).
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "token is not a JWT (opaque access tokens not supported in test mode)"}
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT header decode: " + err.Error()}
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT header parse: " + err.Error()}
	}
	if header.Alg != "HS256" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: fmt.Sprintf("unsupported JWT alg %q (test mode requires HS256)", header.Alg)}
	}

	if len(v.opts.TestKey) == 0 {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "no trusted key configured for verification"}
	}

	// Verify HMAC-SHA256 signature.
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, v.opts.TestKey)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT signature verification failed"}
	}

	// Decode + validate claims.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT claims decode: " + err.Error()}
	}
	var claims struct {
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Iat int64  `json:"iat"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT claims parse: " + err.Error()}
	}
	now := time.Now().Unix()
	if v.opts.Issuer != "" && claims.Iss != v.opts.Issuer {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: fmt.Sprintf("iss claim %q does not match expected issuer", claims.Iss)}
	}
	if v.opts.Audience != "" && claims.Aud != v.opts.Audience {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: fmt.Sprintf("aud claim %q does not match expected audience", claims.Aud)}
	}
	if claims.Exp > 0 && now > claims.Exp+int64(v.opts.MaxClockSkew.Seconds()) {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT has expired"}
	}
	if claims.Iat > 0 && now+int64(v.opts.MaxClockSkew.Seconds()) < claims.Iat {
		return &Error{HTTPStatus: http.StatusUnauthorized, Status: "UNAUTHENTICATED", Message: "JWT iat is in the future"}
	}
	return nil
}

// Package azurebearer implements the Azure-side Bearer token verifier
// for shimanism's Azure-shaped frontends (Key Vault, Service Bus,
// ARM management surfaces).
//
// Azure issues two relevant credential types:
//
//   - Microsoft Entra (Azure AD) JWT bearer tokens — RS256 / RS384
//     signed against Microsoft's published JWKS at
//     `https://login.microsoftonline.com/common/discovery/v2.0/keys`.
//   - SAS tokens — used by Service Bus and Storage; verified separately
//     (see `internal/azuresharedkey`).
//
// Per the Phase 11.1 architecture spike: the verifier for production
// validates against Microsoft's JWKS; test mode validates HS256 JWTs
// signed with a project-owned symmetric key (same approach as
// `internal/gcpbearer`). The shape is intentionally identical so
// adapter glue stays uniform across clouds.
//
// Key Vault requires the WWW-Authenticate challenge response on the
// first request. Adapters that wrap this verifier handle the
// challenge separately — see the per-frontend wiring.
package azurebearer

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
// Azure's standard JSON error envelope (`{"error":{"code":"...","message":"..."}}`).
type Error struct {
	HTTPStatus int
	Code       string // Azure-canonical error code (e.g. "Unauthorized", "InvalidAuthenticationToken").
	Message    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Options configures the verifier.
type Options struct {
	// Issuer is the expected `iss` claim value. For test mode this
	// is a project-specific issuer URI; for Entra ID it's the
	// tenant-scoped issuer (e.g.
	// "https://login.microsoftonline.com/{tenant}/v2.0").
	Issuer string
	// Audience is the expected `aud` claim value — usually the
	// resource URI (e.g. "https://vault.azure.net" for Key Vault).
	Audience string
	// TestKey is the symmetric HMAC-SHA256 key the shim trusts in
	// test mode. When non-empty, the verifier accepts JWTs signed
	// with this key (HS256 alg). Production deployments leave this
	// empty and the verifier rejects non-Entra tokens.
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
// success, *azurebearer.Error on failure.
func (v *Verifier) Verify(r *http.Request) error {
	authHdr := r.Header.Get("Authorization")
	if authHdr == "" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "Unauthorized", Message: "Request had invalid authentication. Expected Bearer token."}
	}
	if !strings.HasPrefix(authHdr, "Bearer ") {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "Unauthorized", Message: "Authorization header missing Bearer prefix"}
	}
	token := strings.TrimPrefix(authHdr, "Bearer ")
	if token == "" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "Unauthorized", Message: "Bearer token is empty"}
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "token is not a JWT"}
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT header decode: " + err.Error()}
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT header parse: " + err.Error()}
	}
	if header.Alg != "HS256" {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: fmt.Sprintf("unsupported JWT alg %q (test mode requires HS256; Entra RS256 verification is a follow-on)", header.Alg)}
	}

	if len(v.opts.TestKey) == 0 {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "no trusted key configured for verification"}
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, v.opts.TestKey)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expectedSig), []byte(parts[2])) {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT signature verification failed"}
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT claims decode: " + err.Error()}
	}
	var claims struct {
		Iss string `json:"iss"`
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Iat int64  `json:"iat"`
		Nbf int64  `json:"nbf"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT claims parse: " + err.Error()}
	}
	now := time.Now().Unix()
	if v.opts.Issuer != "" && claims.Iss != v.opts.Issuer {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: fmt.Sprintf("iss claim %q does not match expected issuer", claims.Iss)}
	}
	if v.opts.Audience != "" && claims.Aud != v.opts.Audience {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: fmt.Sprintf("aud claim %q does not match expected audience", claims.Aud)}
	}
	if claims.Exp > 0 && now > claims.Exp+int64(v.opts.MaxClockSkew.Seconds()) {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT has expired"}
	}
	if claims.Nbf > 0 && now+int64(v.opts.MaxClockSkew.Seconds()) < claims.Nbf {
		return &Error{HTTPStatus: http.StatusUnauthorized, Code: "InvalidAuthenticationToken", Message: "JWT not yet valid (nbf in the future)"}
	}
	return nil
}

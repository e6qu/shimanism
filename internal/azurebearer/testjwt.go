package azurebearer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"
)

// TestJWT builds a well-formed HS256 JWT signed with the verifier's
// trusted test key. Conformance tests use this to assemble bearer
// tokens the azurebearer middleware accepts end-to-end. Not for
// production use; the verifier's TestKey path is gated on a static
// shared secret and Entra ID verification (RS256 against Microsoft's
// JWKS) is a separate code path.
func TestJWT(key []byte, issuer, audience string, ttl time.Duration) string {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	hb, _ := json.Marshal(header)
	now := time.Now().Unix()
	claims := map[string]interface{}{
		"iss": issuer,
		"aud": audience,
		"iat": now,
		"exp": now + int64(ttl.Seconds()),
	}
	cb, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

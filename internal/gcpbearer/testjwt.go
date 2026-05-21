package gcpbearer

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"time"
)

// TestJWT builds a well-formed HS256 JWT signed with the given key
// and carrying the iss / aud claims the verifier validates against.
// Used by conformance tests + the shim's test fixtures to construct
// bearer tokens the verifier accepts.
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

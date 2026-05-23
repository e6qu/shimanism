package azurebearer

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"
)

// TestRSAKey generates a fresh RSA key pair for tests.
func TestRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// JWKSFromRSAPublic builds a one-entry JWKS containing the public
// key under the given kid.
func JWKSFromRSAPublic(pub *rsa.PublicKey, kid string) *JWKS {
	return &JWKS{Keys: []JWK{rsaToJWK(pub, kid)}}
}

func rsaToJWK(pub *rsa.PublicKey, kid string) JWK {
	return JWK{
		Kid: kid,
		Kty: "RSA",
		Use: "sig",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// TestRSAJWT signs a JWT with the given RSA private key + kid, with
// iss / aud / iat / exp claims. Mirror of gcpbearer.TestRSAJWT.
func TestRSAJWT(priv *rsa.PrivateKey, kid, issuer, audience string, ttl time.Duration) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": kid}
	hb, _ := json.Marshal(header)
	now := time.Now().Unix()
	claims := map[string]any{
		"iss": issuer,
		"aud": audience,
		"iat": now,
		"exp": now + int64(ttl.Seconds()),
	}
	cb, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	hashed := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

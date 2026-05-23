package gcpbearer

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
//
// The 2048-bit key is generated per-call; cache it in a sync.Once or
// at package init in test code if multiple tests need the same key.
// Real-cloud lanes (Track A) don't use this — they accept Google's
// or Microsoft Entra's real RS256-signed tokens via Options.JWKSURL.
func TestRSAKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
}

// JWKSFromRSAPublic builds a one-entry JWKS containing the given RSA
// public key under the supplied kid. Used by tests to wire a remote
// JWKS endpoint or an in-process JWKS into Options.
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

// TestRSAJWT signs a JWT with the given RSA private key and kid,
// carrying the standard iss / aud / iat / exp claims. The kid in
// the header lets the verifier look up the matching public key in
// the JWKS.
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

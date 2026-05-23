package gcpbearer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
)

// TestRS256_InProcessJWKS exercises the production RS256 path with
// an Options.JWKS literal — sufficient for unit testing the
// signature-verify + claims-validate code paths without standing up
// an HTTP server.
func TestRS256_InProcessJWKS(t *testing.T) {
	priv, err := gcpbearer.TestRSAKey()
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwks := gcpbearer.JWKSFromRSAPublic(&priv.PublicKey, "test-kid-1")
	v := gcpbearer.New(gcpbearer.Options{
		Issuer:   testIssuer,
		Audience: testAudience,
		JWKS:     jwks,
	})

	t.Run("accept_valid_RS256", func(t *testing.T) {
		token, err := gcpbearer.TestRSAJWT(priv, "test-kid-1", testIssuer, testAudience, time.Hour)
		if err != nil {
			t.Fatalf("sign JWT: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/v1/projects/p/secrets/s", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := v.Verify(req); err != nil {
			t.Errorf("Verify rejected a valid RS256 token: %v", err)
		}
	})

	t.Run("reject_unknown_kid", func(t *testing.T) {
		token, err := gcpbearer.TestRSAJWT(priv, "unknown-kid", testIssuer, testAudience, time.Hour)
		if err != nil {
			t.Fatalf("sign JWT: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		err = v.Verify(req)
		var bErr *gcpbearer.Error
		if err == nil || !asGcpErr(err, &bErr) || !strings.Contains(bErr.Message, "JWKS lookup") {
			t.Errorf("Verify with unknown kid: got %v; want JWKS-lookup error", err)
		}
	})

	t.Run("reject_tampered_signature", func(t *testing.T) {
		token, _ := gcpbearer.TestRSAJWT(priv, "test-kid-1", testIssuer, testAudience, time.Hour)
		parts := strings.Split(token, ".")
		// Flip a character in the middle of the signature. Pick a
		// character that's definitely different (toggle case if it's
		// a letter, otherwise toggle to 'a').
		sig := []byte(parts[2])
		i := len(sig) / 2
		if sig[i] == 'a' {
			sig[i] = 'b'
		} else {
			sig[i] = 'a'
		}
		parts[2] = string(sig)
		tampered := strings.Join(parts, ".")
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+tampered)
		err := v.Verify(req)
		var bErr *gcpbearer.Error
		if err == nil || !asGcpErr(err, &bErr) || !strings.Contains(bErr.Message, "RS256 signature") {
			t.Errorf("Verify tampered: got %v; want RS256-signature error", err)
		}
	})

	t.Run("reject_wrong_issuer", func(t *testing.T) {
		token, _ := gcpbearer.TestRSAJWT(priv, "test-kid-1", "https://attacker.example/", testAudience, time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		err := v.Verify(req)
		var bErr *gcpbearer.Error
		if err == nil || !asGcpErr(err, &bErr) || !strings.Contains(bErr.Message, "iss claim") {
			t.Errorf("Verify wrong-issuer: got %v; want iss-claim error", err)
		}
	})

	t.Run("reject_expired", func(t *testing.T) {
		token, _ := gcpbearer.TestRSAJWT(priv, "test-kid-1", testIssuer, testAudience, -1*time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		err := v.Verify(req)
		var bErr *gcpbearer.Error
		if err == nil || !asGcpErr(err, &bErr) || !strings.Contains(bErr.Message, "expired") {
			t.Errorf("Verify expired: got %v; want expiry error", err)
		}
	})
}

// TestRS256_RemoteJWKS exercises the URL-fetched + cached JWKS path
// against a httptest server that publishes a real JWKS. Same shape
// as production usage with Google's `oauth2/v3/certs` endpoint
// (substitute the real URL via Options.JWKSURL).
func TestRS256_RemoteJWKS(t *testing.T) {
	priv, err := gcpbearer.TestRSAKey()
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwks := gcpbearer.JWKSFromRSAPublic(&priv.PublicKey, "remote-kid")

	jwksHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwksHits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	v := gcpbearer.New(gcpbearer.Options{
		Issuer:       testIssuer,
		Audience:     testAudience,
		JWKSURL:      srv.URL,
		JWKSCacheTTL: 30 * time.Minute,
	})

	// First request fetches the JWKS.
	token, _ := gcpbearer.TestRSAJWT(priv, "remote-kid", testIssuer, testAudience, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if err := v.Verify(req); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if jwksHits != 1 {
		t.Errorf("expected 1 JWKS fetch on first verify; got %d", jwksHits)
	}

	// Second request with the same kid should hit the cache (no
	// extra fetch).
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	if err := v.Verify(req2); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if jwksHits != 1 {
		t.Errorf("expected JWKS cache hit on second verify; got %d fetches", jwksHits)
	}

	// Token with unknown kid forces a re-fetch (kid-rotation path).
	otherPriv, _ := gcpbearer.TestRSAKey()
	otherTok, _ := gcpbearer.TestRSAJWT(otherPriv, "different-kid", testIssuer, testAudience, time.Hour)
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Authorization", "Bearer "+otherTok)
	err = v.Verify(req3)
	var bErr *gcpbearer.Error
	if err == nil || !asGcpErr(err, &bErr) {
		t.Fatalf("Verify with unknown kid: want gcpbearer.Error, got %v", err)
	}
	if jwksHits != 2 {
		t.Errorf("expected JWKS re-fetch on unknown-kid path; got %d fetches", jwksHits)
	}
}

// asGcpErr unwraps a gcpbearer.Error from an error value.
func asGcpErr(err error, target **gcpbearer.Error) bool {
	if e, ok := err.(*gcpbearer.Error); ok {
		*target = e
		return true
	}
	return false
}

package azurebearer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/azurebearer"
)

// TestRS256_InProcessJWKS exercises the production RS256 path with
// an Options.JWKS literal. Mirror of gcpbearer's RS256 tests; the
// shape is intentionally identical so adapter glue stays uniform.
func TestRS256_InProcessJWKS(t *testing.T) {
	priv, err := azurebearer.TestRSAKey()
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	jwks := azurebearer.JWKSFromRSAPublic(&priv.PublicKey, "test-kid-1")
	v := azurebearer.New(azurebearer.Options{
		Issuer:   testIssuer,
		Audience: testAudience,
		JWKS:     jwks,
	})

	t.Run("accept_valid_RS256", func(t *testing.T) {
		token, err := azurebearer.TestRSAJWT(priv, "test-kid-1", testIssuer, testAudience, time.Hour)
		if err != nil {
			t.Fatalf("sign JWT: %v", err)
		}
		req := httptest.NewRequest(http.MethodGet, "/secrets/api-token", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if err := v.Verify(req); err != nil {
			t.Errorf("Verify rejected a valid RS256 token: %v", err)
		}
	})

	t.Run("reject_unknown_kid", func(t *testing.T) {
		token, _ := azurebearer.TestRSAJWT(priv, "unknown-kid", testIssuer, testAudience, time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		err := v.Verify(req)
		var bErr *azurebearer.Error
		if err == nil || !asAzErr(err, &bErr) || !strings.Contains(bErr.Message, "JWKS lookup") {
			t.Errorf("Verify with unknown kid: got %v; want JWKS-lookup error", err)
		}
	})

	t.Run("reject_tampered_signature", func(t *testing.T) {
		token, _ := azurebearer.TestRSAJWT(priv, "test-kid-1", testIssuer, testAudience, time.Hour)
		parts := strings.Split(token, ".")
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
		var bErr *azurebearer.Error
		if err == nil || !asAzErr(err, &bErr) || !strings.Contains(bErr.Message, "RS256 signature") {
			t.Errorf("Verify tampered: got %v; want RS256-signature error", err)
		}
	})

	t.Run("reject_wrong_audience", func(t *testing.T) {
		token, _ := azurebearer.TestRSAJWT(priv, "test-kid-1", testIssuer, "https://other.azure.net", time.Hour)
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		err := v.Verify(req)
		var bErr *azurebearer.Error
		if err == nil || !asAzErr(err, &bErr) || !strings.Contains(bErr.Message, "aud claim") {
			t.Errorf("Verify wrong-audience: got %v; want aud-claim error", err)
		}
	})
}

// TestRS256_RemoteJWKS exercises the URL-fetched + cached JWKS path
// against a httptest server. Same shape as production usage with
// Microsoft Entra's `login.microsoftonline.com/.../discovery/v2.0/keys`.
func TestRS256_RemoteJWKS(t *testing.T) {
	priv, _ := azurebearer.TestRSAKey()
	jwks := azurebearer.JWKSFromRSAPublic(&priv.PublicKey, "remote-kid")

	jwksHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwksHits++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	v := azurebearer.New(azurebearer.Options{
		Issuer:       testIssuer,
		Audience:     testAudience,
		JWKSURL:      srv.URL,
		JWKSCacheTTL: 30 * time.Minute,
	})

	token, _ := azurebearer.TestRSAJWT(priv, "remote-kid", testIssuer, testAudience, time.Hour)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if err := v.Verify(req); err != nil {
		t.Fatalf("first Verify: %v", err)
	}
	if jwksHits != 1 {
		t.Errorf("expected 1 JWKS fetch; got %d", jwksHits)
	}

	// Cache hit on second call with same kid.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	if err := v.Verify(req2); err != nil {
		t.Fatalf("second Verify: %v", err)
	}
	if jwksHits != 1 {
		t.Errorf("expected JWKS cache hit; got %d fetches", jwksHits)
	}

	// Unknown kid forces re-fetch.
	otherPriv, _ := azurebearer.TestRSAKey()
	otherTok, _ := azurebearer.TestRSAJWT(otherPriv, "rotated-kid", testIssuer, testAudience, time.Hour)
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Authorization", "Bearer "+otherTok)
	if err := v.Verify(req3); err == nil {
		t.Errorf("Verify with unknown kid: want error, got nil")
	}
	if jwksHits != 2 {
		t.Errorf("expected JWKS re-fetch on unknown-kid; got %d fetches", jwksHits)
	}
}

func asAzErr(err error, target **azurebearer.Error) bool {
	if e, ok := err.(*azurebearer.Error); ok {
		*target = e
		return true
	}
	return false
}

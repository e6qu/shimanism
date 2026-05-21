package gcpbearer_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
)

const (
	testIssuer   = "https://shim.test/"
	testAudience = "https://secretmanager.googleapis.com/"
)

var testKey = []byte("test-key-do-not-use-in-prod")

// signJWT builds a well-formed HS256 JWT with the given claims.
func signJWT(t *testing.T, claims map[string]interface{}) string {
	t.Helper()
	header := map[string]interface{}{"alg": "HS256", "typ": "JWT"}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	h := base64.RawURLEncoding.EncodeToString(hb)
	c := base64.RawURLEncoding.EncodeToString(cb)
	signingInput := h + "." + c
	mac := hmac.New(sha256.New, testKey)
	mac.Write([]byte(signingInput))
	s := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + s
}

func makeReq(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestGCPBearer_AcceptsValidToken(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{
		Issuer:   testIssuer,
		Audience: testAudience,
		TestKey:  testKey,
	})
	token := signJWT(t, map[string]interface{}{
		"iss": testIssuer,
		"aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	})
	if err := v.Verify(makeReq(token)); err != nil {
		t.Errorf("Verify on valid token: %v", err)
	}
}

func TestGCPBearer_RejectsMissingAuth(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{TestKey: testKey})
	err := v.Verify(makeReq(""))
	if err == nil {
		t.Fatal("expected error for missing Authorization")
	}
	if e := err.(*gcpbearer.Error); e.Status != "UNAUTHENTICATED" {
		t.Errorf("Status = %q, want UNAUTHENTICATED", e.Status)
	}
}

func TestGCPBearer_RejectsTamperedSignature(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{Audience: testAudience, TestKey: testKey})
	token := signJWT(t, map[string]interface{}{
		"aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	// Flip the last char of the signature.
	tampered := []byte(token)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	err := v.Verify(makeReq(string(tampered)))
	if err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

func TestGCPBearer_RejectsWrongAudience(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{Audience: testAudience, TestKey: testKey})
	token := signJWT(t, map[string]interface{}{
		"aud": "https://wrong.audience/",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	err := v.Verify(makeReq(token))
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
	if !strings.Contains(err.Error(), "aud") {
		t.Errorf("expected aud-mismatch error, got: %v", err)
	}
}

func TestGCPBearer_RejectsExpiredToken(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{Audience: testAudience, TestKey: testKey})
	token := signJWT(t, map[string]interface{}{
		"aud": testAudience,
		"exp": time.Now().Add(-10 * time.Minute).Unix(),
	})
	err := v.Verify(makeReq(token))
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("expected expired error, got: %v", err)
	}
}

func TestGCPBearer_RejectsOpaqueAccessToken(t *testing.T) {
	v := gcpbearer.New(gcpbearer.Options{TestKey: testKey})
	// Opaque tokens don't have 3 dot-separated segments.
	err := v.Verify(makeReq("ya29.opaque-access-token-not-a-jwt"))
	if err == nil {
		t.Fatal("expected error for opaque token")
	}
}

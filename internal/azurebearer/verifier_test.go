package azurebearer_test

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

	"github.com/e6qu/shimanism/internal/azurebearer"
)

const (
	testIssuer   = "https://shim.test/"
	testAudience = "https://vault.azure.net"
)

var testKey = []byte("test-key-do-not-use-in-prod")

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

func TestAzureBearer_AcceptsValidToken(t *testing.T) {
	v := azurebearer.New(azurebearer.Options{
		Issuer: testIssuer, Audience: testAudience, TestKey: testKey,
	})
	token := signJWT(t, map[string]interface{}{
		"iss": testIssuer, "aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
	if err := v.Verify(makeReq(token)); err != nil {
		t.Errorf("Verify on valid token: %v", err)
	}
}

func TestAzureBearer_RejectsMissingAuth(t *testing.T) {
	v := azurebearer.New(azurebearer.Options{TestKey: testKey})
	err := v.Verify(makeReq(""))
	if err == nil {
		t.Fatal("expected error for missing Authorization")
	}
	if e := err.(*azurebearer.Error); e.Code != "Unauthorized" {
		t.Errorf("Code = %q, want Unauthorized", e.Code)
	}
}

func TestAzureBearer_RejectsTamperedSignature(t *testing.T) {
	v := azurebearer.New(azurebearer.Options{Audience: testAudience, TestKey: testKey})
	token := signJWT(t, map[string]interface{}{
		"aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
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

func TestAzureBearer_RejectsExpiredToken(t *testing.T) {
	v := azurebearer.New(azurebearer.Options{Audience: testAudience, TestKey: testKey})
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

func TestAzureBearer_RejectsNotYetValid(t *testing.T) {
	v := azurebearer.New(azurebearer.Options{Audience: testAudience, TestKey: testKey, MaxClockSkew: 1 * time.Minute})
	token := signJWT(t, map[string]interface{}{
		"aud": testAudience,
		"exp": time.Now().Add(20 * time.Minute).Unix(),
		"nbf": time.Now().Add(10 * time.Minute).Unix(),
	})
	err := v.Verify(makeReq(token))
	if err == nil {
		t.Fatal("expected error for nbf in the future")
	}
}

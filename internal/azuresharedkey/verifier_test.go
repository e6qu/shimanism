package azuresharedkey_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/azuresharedkey"
)

const testAccount = "shimstorage"

var testKey = base64ed("test-key-do-not-use-in-prod-this-is-32-bytes-of-junk")

// base64ed wraps a string into a base64-decoded byte slice — Azure
// account keys are typically base64-encoded; we decode to raw bytes
// to match how azure-sdk-for-go consumes them.
func base64ed(s string) []byte {
	b := make([]byte, len(s))
	copy(b, s)
	return b
}

// sign computes the SharedKey signature the verifier expects for a
// request — used in tests to construct valid auth headers.
func sign(t *testing.T, r *http.Request) string {
	t.Helper()
	canonical := buildCanonicalForTest(r, testAccount)
	mac := hmac.New(sha256.New, testKey)
	mac.Write([]byte(canonical))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// buildCanonicalForTest mirrors the verifier's canonicalisation —
// kept package-local so the test doesn't have to import internal
// helpers.
func buildCanonicalForTest(r *http.Request, account string) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Encoding"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Language"))
	b.WriteString("\n")
	cl := r.Header.Get("Content-Length")
	if cl == "0" {
		cl = ""
	}
	b.WriteString(cl)
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-MD5"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Content-Type"))
	b.WriteString("\n")
	if r.Header.Get("x-ms-date") != "" {
		b.WriteString("")
	} else {
		b.WriteString(r.Header.Get("Date"))
	}
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Modified-Since"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Match"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-None-Match"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("If-Unmodified-Since"))
	b.WriteString("\n")
	b.WriteString(r.Header.Get("Range"))
	b.WriteString("\n")
	type kv struct{ k, v string }
	var hs []kv
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, "x-ms-") {
			continue
		}
		v := strings.Join(vs, ",")
		v = strings.ReplaceAll(v, "\n", " ")
		v = strings.TrimSpace(v)
		hs = append(hs, kv{lk, v})
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].k < hs[j].k })
	for _, h := range hs {
		b.WriteString(h.k)
		b.WriteString(":")
		b.WriteString(h.v)
		b.WriteString("\n")
	}
	b.WriteString("/")
	b.WriteString(account)
	b.WriteString(r.URL.Path)
	if len(r.URL.Query()) > 0 {
		var qs []kv
		for k, vs := range r.URL.Query() {
			lk := strings.ToLower(k)
			sort.Strings(vs)
			qs = append(qs, kv{lk, strings.Join(vs, ",")})
		}
		sort.Slice(qs, func(i, j int) bool { return qs[i].k < qs[j].k })
		for _, q := range qs {
			b.WriteString("\n")
			b.WriteString(q.k)
			b.WriteString(":")
			b.WriteString(q.v)
		}
	}
	return b.String()
}

func TestSharedKey_AcceptsValid(t *testing.T) {
	v := azuresharedkey.New(azuresharedkey.StaticStore{Account: testAccount, Key: testKey})
	req := httptest.NewRequest(http.MethodGet, "/container/blob", nil)
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-ms-version", "2024-11-04")
	signature := sign(t, req)
	req.Header.Set("Authorization", "SharedKey "+testAccount+":"+signature)

	if err := v.Verify(req); err != nil {
		t.Errorf("Verify on valid signature: %v", err)
	}
}

func TestSharedKey_RejectsMissingAuth(t *testing.T) {
	v := azuresharedkey.New(azuresharedkey.StaticStore{Account: testAccount, Key: testKey})
	req := httptest.NewRequest(http.MethodGet, "/container/blob", nil)

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for missing Authorization")
	}
	if e := err.(*azuresharedkey.Error); e.Code != "AuthenticationFailed" {
		t.Errorf("Code = %q, want AuthenticationFailed", e.Code)
	}
}

func TestSharedKey_RejectsUnknownAccount(t *testing.T) {
	v := azuresharedkey.New(azuresharedkey.StaticStore{Account: testAccount, Key: testKey})
	req := httptest.NewRequest(http.MethodGet, "/container/blob", nil)
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	signature := sign(t, req)
	req.Header.Set("Authorization", "SharedKey otherAccount:"+signature)

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for unknown account")
	}
}

func TestSharedKey_RejectsTampered(t *testing.T) {
	v := azuresharedkey.New(azuresharedkey.StaticStore{Account: testAccount, Key: testKey})
	req := httptest.NewRequest(http.MethodGet, "/container/blob", nil)
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	signature := sign(t, req)
	// Flip the last char of the signature.
	tampered := []byte(signature)
	if tampered[len(tampered)-1] == 'A' {
		tampered[len(tampered)-1] = 'B'
	} else {
		tampered[len(tampered)-1] = 'A'
	}
	req.Header.Set("Authorization", "SharedKey "+testAccount+":"+string(tampered))

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for tampered signature")
	}
}

func TestSharedKey_RejectsWrongScheme(t *testing.T) {
	v := azuresharedkey.New(azuresharedkey.StaticStore{Account: testAccount, Key: testKey})
	req := httptest.NewRequest(http.MethodGet, "/container/blob", nil)
	req.Header.Set("Authorization", "Bearer some-token")

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for non-SharedKey scheme")
	}
	if e := err.(*azuresharedkey.Error); e.Code != "InvalidAuthenticationInfo" {
		t.Errorf("Code = %q, want InvalidAuthenticationInfo", e.Code)
	}
}

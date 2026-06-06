package sigv4verifier_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/e6qu/shimanism/internal/sigv4verifier"
)

// testStore is the in-memory CredentialStore used by these tests.
// Production deployments wire their own.
type testStore struct {
	key, secret, session string
}

func (s testStore) Lookup(_ context.Context, k string) (string, string, bool) {
	if k != s.key {
		return "", "", false
	}
	return s.secret, s.session, true
}

func signedRequest(t *testing.T, method, url, body string, creds aws.Credentials, service, region string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	// Compute payload hash before signing — that's what the signer
	// expects when SignHTTP is called with a non-empty body.
	signer := v4.NewSigner()
	payloadHash := "UNSIGNED-PAYLOAD"
	if body != "" {
		payloadHash = sha256Hex(body)
	}
	if err := signer.SignHTTP(context.Background(), creds, req, payloadHash, service, region, time.Now().UTC()); err != nil {
		t.Fatalf("SignHTTP: %v", err)
	}
	return req
}

func TestVerifier_AcceptsValidSignature(t *testing.T) {
	store := testStore{key: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	v := sigv4verifier.New(store, sigv4verifier.Options{
		Service: "secretsmanager",
		Region:  "us-east-1",
	})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	req := signedRequest(t, http.MethodPost, "http://example/", `{}`, creds, "secretsmanager", "us-east-1")

	if err := v.Verify(req); err != nil {
		t.Errorf("Verify on valid signature returned error: %v", err)
	}
}

func TestVerifier_AcceptsEscapedARNPathLabel(t *testing.T) {
	store := testStore{key: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	v := sigv4verifier.New(store, sigv4verifier.Options{
		Service: "kafka",
		Region:  "us-east-1",
	})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	arn := "arn:aws:kafka:us-east-1:000000000000:cluster/cluster-a/uuid-1"
	req, err := http.NewRequest(http.MethodGet, "http://example/v1/clusters/"+arn, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.URL.RawPath = "/v1/clusters/" + url.PathEscape(arn)
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	signer := v4.NewSigner()
	if err := signer.SignHTTP(context.Background(), creds, req, sha256Hex(""), "kafka", "us-east-1", time.Now().UTC()); err != nil {
		t.Fatalf("SignHTTP: %v", err)
	}

	if err := v.Verify(req); err != nil {
		t.Errorf("Verify on escaped ARN path label returned error: %v", err)
	}
}

func TestVerifier_RejectsMissingAuth(t *testing.T) {
	v := sigv4verifier.New(testStore{}, sigv4verifier.Options{Service: "secretsmanager", Region: "us-east-1"})
	req, _ := http.NewRequest(http.MethodPost, "http://example/", strings.NewReader(`{}`))

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for missing Authorization")
	}
	ve, ok := err.(*sigv4verifier.Error)
	if !ok {
		t.Fatalf("error type = %T, want *sigv4verifier.Error", err)
	}
	if ve.Code != "MissingAuthenticationToken" {
		t.Errorf("Code = %q, want MissingAuthenticationToken", ve.Code)
	}
}

func TestVerifier_RejectsUnknownAccessKey(t *testing.T) {
	store := testStore{key: "AKIAIOSFODNN7EXAMPLE", secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"}
	v := sigv4verifier.New(store, sigv4verifier.Options{Service: "secretsmanager", Region: "us-east-1"})
	// Sign with a different access key (not in the store).
	otherCreds := aws.Credentials{AccessKeyID: "AKIAOTHER", SecretAccessKey: store.secret}
	req := signedRequest(t, http.MethodPost, "http://example/", `{}`, otherCreds, "secretsmanager", "us-east-1")

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for unknown access key")
	}
	if e := err.(*sigv4verifier.Error); e.Code != "InvalidAccessKeyId" {
		t.Errorf("Code = %q, want InvalidAccessKeyId", e.Code)
	}
}

func TestVerifier_RejectsWrongRegionOrService(t *testing.T) {
	store := testStore{key: "AKID", secret: "secret"}
	v := sigv4verifier.New(store, sigv4verifier.Options{Service: "secretsmanager", Region: "us-east-1"})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	// Sign with the right key but the wrong service.
	req := signedRequest(t, http.MethodPost, "http://example/", `{}`, creds, "lambda", "us-east-1")

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for wrong service credential scope")
	}
	if e := err.(*sigv4verifier.Error); e.Code != "SignatureDoesNotMatch" {
		t.Errorf("Code = %q, want SignatureDoesNotMatch", e.Code)
	}
}

func TestVerifier_RejectsTamperedSignature(t *testing.T) {
	store := testStore{key: "AKID", secret: "secret"}
	v := sigv4verifier.New(store, sigv4verifier.Options{Service: "secretsmanager", Region: "us-east-1"})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	req := signedRequest(t, http.MethodPost, "http://example/", `{}`, creds, "secretsmanager", "us-east-1")

	// Flip a bit in the Authorization Signature= field.
	auth := req.Header.Get("Authorization")
	i := strings.Index(auth, "Signature=")
	if i < 0 {
		t.Fatal("test setup: Signature= not in Authorization")
	}
	tampered := []byte(auth)
	// Flip the last hex char.
	if tampered[len(tampered)-1] == 'a' {
		tampered[len(tampered)-1] = 'b'
	} else {
		tampered[len(tampered)-1] = 'a'
	}
	req.Header.Set("Authorization", string(tampered))

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for tampered signature")
	}
	if e := err.(*sigv4verifier.Error); e.Code != "SignatureDoesNotMatch" {
		t.Errorf("Code = %q, want SignatureDoesNotMatch", e.Code)
	}
}

func TestVerifier_RejectsStaleTimestamp(t *testing.T) {
	store := testStore{key: "AKID", secret: "secret"}
	v := sigv4verifier.New(store, sigv4verifier.Options{
		Service:      "secretsmanager",
		Region:       "us-east-1",
		MaxClockSkew: 1 * time.Minute,
	})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	req := signedRequest(t, http.MethodPost, "http://example/", `{}`, creds, "secretsmanager", "us-east-1")
	// Rewind the signed time by 30 minutes (outside the 1-minute skew).
	req.Header.Set("X-Amz-Date", time.Now().UTC().Add(-30*time.Minute).Format("20060102T150405Z"))

	err := v.Verify(req)
	if err == nil {
		t.Fatal("expected error for stale timestamp")
	}
	if e := err.(*sigv4verifier.Error); e.Code != "RequestTimeTooSkewed" {
		t.Errorf("Code = %q, want RequestTimeTooSkewed", e.Code)
	}
}

func TestVerifier_RestoresBodyForDownstream(t *testing.T) {
	store := testStore{key: "AKID", secret: "secret"}
	v := sigv4verifier.New(store, sigv4verifier.Options{Service: "secretsmanager", Region: "us-east-1"})
	creds := aws.Credentials{AccessKeyID: store.key, SecretAccessKey: store.secret}
	req := signedRequest(t, http.MethodPost, "http://example/", `{"hello":"world"}`, creds, "secretsmanager", "us-east-1")

	if err := v.Verify(req); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// The downstream handler should still be able to read the body.
	body := make([]byte, 64)
	n, _ := req.Body.Read(body)
	if string(body[:n]) != `{"hello":"world"}` {
		t.Errorf("downstream body = %q, want the original JSON", body[:n])
	}
}

// sha256Hex mirrors the unexported helper in the verifier package
// so tests can compute payload hashes the same way the verifier does.
func sha256Hex(s string) string {
	return sigv4SHA256Hex([]byte(s))
}

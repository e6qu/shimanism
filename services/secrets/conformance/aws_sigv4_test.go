// Test the SigV4 verifier end-to-end on the secrets frontend: real
// SigV4-signed requests must succeed; unsigned / wrong-key / tampered
// requests must fail with the source cloud's own 401/403 envelope.
//
// The harness's init() sets SHIMANISM_TEST_UNAUTHENTICATED=1, which
// makes the verifier short-circuit. We unset it for this test (and
// only this test — it runs serially via t.Setenv so the rest of the
// suite keeps the bypass) so the verifier actually runs.
package conformance_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	awssmfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

// Test-mode credentials wired by aws_secretsmanager.New. Mirror them
// here so signing produces a signature the verifier accepts.
const (
	testAccessKey = "AKIAIOSFODNN7EXAMPLE"
	testSecret    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	testRegion    = "us-east-1"
	testService   = "secretsmanager"
)

// TestAWSSigV4_AcceptsSignedRequestViaSDK exercises the positive
// path with the real AWS Secrets Manager SDK pointed at the shim
// and configured with the trusted test credentials. The SDK signs
// the request via its own pipeline; the verifier must accept.
//
// Verifier fix that unblocked this case: restrict the verifier's
// re-sign-clone headers to ONLY the original Authorization's
// SignedHeaders set. Go's http.Transport auto-adds
// `Accept-Encoding: gzip` AFTER the SDK signs, so the inbound
// request to the server has it but the SDK's signature didn't
// cover it; without filtering, the verifier's re-sign would
// include Accept-Encoding in the SignedHeaders, expanding the
// canonical request and producing a spurious mismatch.
func TestAWSSigV4_AcceptsSignedRequestViaSDK(t *testing.T) {
	t.Setenv("SHIMANISM_TEST_UNAUTHENTICATED", "")
	srv := startSignedSecretsServer(t)

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(testRegion),
		awsconfig.WithCredentialsProvider(awsapi.CredentialsProviderFunc(func(ctx context.Context) (awsapi.Credentials, error) {
			return awsapi.Credentials{AccessKeyID: testAccessKey, SecretAccessKey: testSecret}, nil
		})),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	client := secretsmanager.NewFromConfig(cfg, func(o *secretsmanager.Options) {
		o.BaseEndpoint = awsapi.String(srv.URL)
	})

	// DescribeSecret on a non-existent secret. The verifier accepting
	// the signature lets the request reach the adapter, which surfaces
	// ResourceNotFoundException — proving the signed request made it
	// past the verifier.
	_, err = client.DescribeSecret(context.Background(), &secretsmanager.DescribeSecretInput{
		SecretId: awsapi.String("nonexistent"),
	})
	if err == nil {
		t.Fatal("expected ResourceNotFoundException; got success")
	}
	if !strings.Contains(err.Error(), "ResourceNotFoundException") {
		t.Errorf("expected ResourceNotFoundException (verifier accepted, op ran), got: %v", err)
	}
}

func TestAWSSigV4_RejectsUnsignedRequest(t *testing.T) {
	t.Setenv("SHIMANISM_TEST_UNAUTHENTICATED", "")
	srv := startSignedSecretsServer(t)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(`{"SecretId":"any"}`))
	req.Header.Set("X-Amz-Target", "secretsmanager.DescribeSecret")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Amzn-Errortype"); got != "MissingAuthenticationToken" {
		t.Errorf("X-Amzn-Errortype = %q, want MissingAuthenticationToken", got)
	}
}

func TestAWSSigV4_RejectsWrongKey(t *testing.T) {
	t.Setenv("SHIMANISM_TEST_UNAUTHENTICATED", "")
	srv := startSignedSecretsServer(t)

	req := buildDescribeSecretReq(t, srv.URL, "any-secret",
		awsapi.Credentials{AccessKeyID: "AKIAOTHER", SecretAccessKey: testSecret})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Amzn-Errortype"); got != "InvalidAccessKeyId" {
		t.Errorf("X-Amzn-Errortype = %q, want InvalidAccessKeyId", got)
	}
}

func TestAWSSigV4_RejectsTamperedSignature(t *testing.T) {
	t.Setenv("SHIMANISM_TEST_UNAUTHENTICATED", "")
	srv := startSignedSecretsServer(t)

	req := buildDescribeSecretReq(t, srv.URL, "any-secret",
		awsapi.Credentials{AccessKeyID: testAccessKey, SecretAccessKey: testSecret})
	// Flip the last char of the Authorization header's Signature= field.
	auth := req.Header.Get("Authorization")
	tampered := []byte(auth)
	if tampered[len(tampered)-1] == 'a' {
		tampered[len(tampered)-1] = 'b'
	} else {
		tampered[len(tampered)-1] = 'a'
	}
	req.Header.Set("Authorization", string(tampered))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("X-Amzn-Errortype"); got != "SignatureDoesNotMatch" {
		t.Errorf("X-Amzn-Errortype = %q, want SignatureDoesNotMatch", got)
	}
}

func startSignedSecretsServer(t *testing.T) *httptest.Server {
	t.Helper()
	handler := awssmfront.New(inmem.New())
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func buildDescribeSecretReq(t *testing.T, baseURL, secretID string, creds awsapi.Credentials) *http.Request {
	t.Helper()
	body := `{"SecretId":"` + secretID + `"}`
	req, err := http.NewRequest(http.MethodPost, baseURL+"/", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("X-Amz-Target", "secretsmanager.DescribeSecret")
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	signer := v4.NewSigner()
	if err := signer.SignHTTP(context.Background(), creds, req, sha256BodyHex(body), testService, testRegion, time.Now().UTC()); err != nil {
		t.Fatalf("SignHTTP: %v", err)
	}
	return req
}

// sha256BodyHex returns lowercase hex of SHA-256(body); the SigV4
// signer expects the payload hash up front for header-signed requests.
func sha256BodyHex(s string) string {
	return computeSha256(s)
}

// Package sigv4verifier implements the server-side SigV4 verifier
// shimanism needs to reject unsigned / tampered-signature requests
// with the source cloud's own 401/403 envelope.
//
// AWS's `aws-sdk-go-v2/aws/signer/v4` package exposes a *signer* —
// it builds the canonical request, derives the signing key, and
// writes the Authorization header. Verification re-uses that exact
// algorithm: take the incoming request, strip the presented
// Authorization, re-sign with the same credentials extracted from
// the request, constant-time compare. Same code paths as the signer
// produce the same string when the request is honest; anything else
// reveals tampering.
//
// What this package does NOT do today:
//
//   - Presigned URL verification (signature carried in query
//     parameters, separate canonical-request rules).
//   - Temporary-credential (X-Amz-Security-Token) lookup against a
//     real STS-issued session-token store. The test-mode store
//     trusts any session token; production deployments wire their
//     own.
//   - Streaming-body integrity (UNSIGNED-PAYLOAD is accepted and the
//     body is not re-hashed). Phase 11 conformance assumes signed
//     payloads or explicit UNSIGNED-PAYLOAD; chunked SigV4-STREAMING
//     is a Phase 11+ follow-on.
//
// Use:
//
//	v := sigv4verifier.New(creds, sigv4verifier.Options{
//	    Service:      "secretsmanager",
//	    Region:       "us-east-1",
//	    MaxClockSkew: 15 * time.Minute,
//	})
//	if err := v.Verify(r); err != nil {
//	    // err is a *sigv4verifier.Error — write its HTTPStatus +
//	    // ErrorCode + Message in the source cloud's envelope.
//	}
package sigv4verifier

import (
	"bytes"
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// CredentialStore looks up the AWS access-key-id's secret + optional
// session token. Returns ok=false for unknown access keys (the
// verifier responds with InvalidAccessKeyId / SignatureDoesNotMatch).
//
// In tests the store carries a single deterministic test credential
// the shim trusts iff SHIMANISM_TEST_TRUSTED_KEY=1 is set in the
// environment at startup. In production, wire this against the
// deployment's identity-management surface.
type CredentialStore interface {
	Lookup(ctx context.Context, accessKeyID string) (secret string, sessionToken string, ok bool)
}

// Options configures the verifier. Service + Region come from the
// frontend's identity in the AWS SDK's service catalogue — e.g.
// Service="secretsmanager", Region="us-east-1" — and must match the
// values the SDK client signed with.
type Options struct {
	Service      string
	Region       string
	MaxClockSkew time.Duration
}

// Verifier verifies incoming AWS-shaped requests against the
// configured credential store.
type Verifier struct {
	store CredentialStore
	opts  Options
}

// New constructs a verifier with sensible defaults.
func New(store CredentialStore, opts Options) *Verifier {
	if opts.MaxClockSkew == 0 {
		opts.MaxClockSkew = 15 * time.Minute
	}
	return &Verifier{store: store, opts: opts}
}

// Error is the structured failure type. HTTP handlers map it onto
// the source cloud's error envelope.
type Error struct {
	HTTPStatus int
	Code       string // AWS-canonical short error name.
	Message    string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Verify checks the request's Authorization header and, if signed
// with a trusted credential, returns nil. Returns a *sigv4verifier.Error
// for any failure. On success the request body is buffered + restored
// for downstream handlers (we have to read it to compute the payload
// hash; we can't tell the SDK side "don't re-read").
//
// The verifier does NOT modify the request other than to restore the
// (already-buffered) body — the Authorization header / X-Amz-Date /
// X-Amz-Security-Token stay intact for downstream observation.
func (v *Verifier) Verify(r *http.Request) error {
	authHdr := r.Header.Get("Authorization")
	if authHdr == "" {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "MissingAuthenticationToken",
			Message:    "Request is missing Authentication Token",
		}
	}
	scheme, parsed, err := parseAuthHeader(authHdr)
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "InvalidSignatureException",
			Message:    err.Error(),
		}
	}
	if scheme != "AWS4-HMAC-SHA256" {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "InvalidSignatureException",
			Message:    "unsupported signing scheme: " + scheme,
		}
	}

	signedTime, err := parseAmzDate(r.Header.Get("X-Amz-Date"))
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "InvalidSignatureException",
			Message:    "X-Amz-Date header: " + err.Error(),
		}
	}
	if skew := time.Since(signedTime); skew > v.opts.MaxClockSkew || -skew > v.opts.MaxClockSkew {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "RequestTimeTooSkewed",
			Message:    "Signed time is outside ±" + v.opts.MaxClockSkew.String() + " of server time",
		}
	}

	// Credential scope: <accessKey>/<yyyymmdd>/<region>/<service>/aws4_request.
	credParts := strings.Split(parsed.Credential, "/")
	if len(credParts) != 5 || credParts[4] != "aws4_request" {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "InvalidSignatureException",
			Message:    "malformed Credential scope: " + parsed.Credential,
		}
	}
	accessKey, region, service := credParts[0], credParts[2], credParts[3]
	if region != v.opts.Region || service != v.opts.Service {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "SignatureDoesNotMatch",
			Message: fmt.Sprintf("credential scope does not match this frontend; got %s/%s, want %s/%s",
				region, service, v.opts.Region, v.opts.Service),
		}
	}

	secret, sessionToken, ok := v.store.Lookup(r.Context(), accessKey)
	if !ok {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "InvalidAccessKeyId",
			Message:    "access key id is not recognised",
		}
	}

	// Buffer the body once; restore it for downstream handlers.
	body, err := readAndRestoreBody(r)
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       "InvalidRequest",
			Message:    "could not read request body: " + err.Error(),
		}
	}
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = sha256Hex(body)
	}

	// Build a fresh request that mirrors the incoming one minus the
	// Authorization (and X-Amz-Date which the signer will set itself).
	// The signer rewrites the Authorization header on the cloned
	// request; we then compare.
	clone := r.Clone(r.Context())
	clone.Header.Del("Authorization")
	clone.Header.Del("X-Amz-Date")
	clone.Body = io.NopCloser(bytes.NewReader(body))

	creds := aws.Credentials{
		AccessKeyID:     accessKey,
		SecretAccessKey: secret,
		SessionToken:    sessionToken,
	}
	signer := v4.NewSigner()
	if err := signer.SignHTTP(r.Context(), creds, clone, payloadHash, service, region, signedTime); err != nil {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "SignatureDoesNotMatch",
			Message:    "could not re-sign for verification: " + err.Error(),
		}
	}
	expected, _, err := parseAuthHeaderShort(clone.Header.Get("Authorization"))
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "SignatureDoesNotMatch",
			Message:    "could not re-parse verified signature",
		}
	}
	if subtle.ConstantTimeCompare([]byte(parsed.Signature), []byte(expected)) != 1 {
		return &Error{
			HTTPStatus: http.StatusForbidden,
			Code:       "SignatureDoesNotMatch",
			Message:    "request signature is not valid for the credential and request",
		}
	}
	return nil
}

// readAndRestoreBody reads the request body once and replaces it on
// the request with a non-closed io.ReadCloser holding the same bytes.
// Returns the buffered bytes so the verifier can compute the payload
// hash.
func readAndRestoreBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return []byte{}, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(b))
	if r.GetBody == nil {
		clone := make([]byte, len(b))
		copy(clone, b)
		r.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(clone)), nil
		}
	}
	return b, nil
}

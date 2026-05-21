// Phase 10 sub-phase 10.2-C: invalid-input fidelity for storage.
//
// For each known-bad input the AWS S3 client might send, assert the
// shim returns the source-cloud's *real* error envelope, never a
// fabricated success and never a generic 500.
//
// The matrix of bad inputs is bounded — we test the cases the AWS
// provider's plan/apply pipeline actually generates when fed
// misconfigured HCL or when the user runs apply against state that
// has drifted out of band. Other classes of malformed input are out
// of scope for this phase (the conformance harness asserts the
// happy + adjacent-sad paths, not exhaustive fuzzing).
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

func TestInvalidInput_AWSS3_NoSuchBucket(t *testing.T) {
	t.Parallel()
	backend := inmem.New()
	srv := harness.StartStorageServer(t, backend)

	client := s3.NewFromConfig(awsTestConfig(srv.URL))
	_, err := client.HeadBucket(t.Context(), &s3.HeadBucketInput{
		Bucket: aws.String("does-not-exist"),
	})
	if err == nil {
		t.Fatal("expected HeadBucket on missing bucket to fail")
	}
	// Real S3 returns 404 with NotFound (NoSuchBucket on object-level
	// paths). Both shapes acceptable; the shim must not return
	// silent success or a generic 500.
	var nf *s3types.NotFound
	var nsb *s3types.NoSuchBucket
	if !errors.As(err, &nf) && !errors.As(err, &nsb) {
		t.Errorf("expected typed AWS not-found error, got %T: %v", err, err)
	}
}

func TestInvalidInput_AWSS3_GetMissingObject(t *testing.T) {
	t.Parallel()
	backend := inmem.New()
	srv := harness.StartStorageServer(t, backend)

	if err := backend.CreateBucket(t.Context(), "exists", "us-east-1"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}

	client := s3.NewFromConfig(awsTestConfig(srv.URL))
	_, err := client.GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String("exists"),
		Key:    aws.String("does-not-exist.txt"),
	})
	if err == nil {
		t.Fatal("expected GetObject on missing key to fail")
	}
	var nsk *s3types.NoSuchKey
	if !errors.As(err, &nsk) {
		t.Errorf("expected NoSuchKey, got %T: %v", err, err)
	}
}

func TestInvalidInput_AWSS3_PutObjectToMissingBucket(t *testing.T) {
	t.Parallel()
	backend := inmem.New()
	srv := harness.StartStorageServer(t, backend)

	client := s3.NewFromConfig(awsTestConfig(srv.URL))
	_, err := client.PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String("does-not-exist"),
		Key:    aws.String("key"),
		Body:   bytes.NewReader([]byte("data")),
	})
	if err == nil {
		t.Fatal("expected PutObject to missing bucket to fail")
	}
	var nsb *s3types.NoSuchBucket
	if !errors.As(err, &nsb) {
		// Some SDK versions surface this as a generic API error with
		// the NoSuchBucket code; verify the code at least matches.
		if !strings.Contains(err.Error(), "NoSuchBucket") {
			t.Errorf("expected NoSuchBucket error envelope, got %T: %v", err, err)
		}
	}
}

func awsTestConfig(endpoint string) aws.Config {
	return aws.Config{
		Region: "us-east-1",
		Credentials: aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
			// Verifier's trusted test credentials.
			return aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			}, nil
		}),
		BaseEndpoint: aws.String(endpoint),
	}
}

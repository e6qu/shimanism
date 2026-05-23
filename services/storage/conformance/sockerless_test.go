// Sockerless lane for the storage service.
//
// `github.com/e6qu/sockerless` is a multi-cloud simulator suite.
// These tests point the shim's cloud backends at a running sockerless
// simulator instance so the AWS / GCP / Azure backend translation
// layers can be exercised without standing up real cloud accounts.
//
// Sockerless coverage of the storage service (May 2026, Phase 14.A):
//
//	AWS S3      — full bucket + object round-trip. Sockerless#173 +
//	              #174 closed; routes at canonical root and the
//	              aws-chunked envelope decoder lands the SDK's
//	              streaming PutObject correctly.
//	GCS         — full bucket + object round-trip.
//	Azure Blob  — data-plane lane added in Phase 14.B (sockerless#178).
//
// Each sub-test skips when its driver env var isn't set, so the
// default `go test ./...` lane is unaffected.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"testing"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/storage/domain"
	awsbackend "github.com/e6qu/shimanism/services/storage/backends/aws"
	gcsbackend "github.com/e6qu/shimanism/services/storage/backends/gcs"
)

// TestSockerless_AWS_S3RoundTrip drives the shim's AWS-shaped
// frontend → AWS S3 backend → sockerless AWS simulator end-to-end:
// CreateBucket, ListBuckets, PutObject, GetObject, HeadObject,
// DeleteObject, DeleteBucket. Set SOCKERLESS_AWS_ENDPOINT (the
// sim's HTTPS listener; no path prefix — sockerless#173 closed)
// to opt in.
func TestSockerless_AWS_S3RoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	client := newSockerlessAWSClient(t, endpoint)
	backend := awsbackend.New(client)
	ctx := context.Background()

	bucket := randomNamespace("shim-sockerless")
	if err := backend.CreateBucket(ctx, bucket, "us-east-1"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteBucket(ctx, bucket) })

	if _, err := backend.HeadBucket(ctx, bucket); err != nil {
		t.Fatalf("HeadBucket: %v", err)
	}

	list, err := backend.ListBuckets(ctx, domain.ListBucketsOptions{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	found := false
	for _, b := range list.Buckets {
		if b.Name == bucket {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListBuckets did not contain %q (got %d buckets)", bucket, len(list.Buckets))
	}

	// Object round-trip. The body is a non-seekable reader —
	// exercises the aws-chunked path the SDK uses for streaming
	// uploads (sockerless#174 closed in upstream PR #179).
	key := "rt/" + randomNamespace("obj") + ".bin"
	body := []byte("phase-14.A sockerless AWS S3 round-trip")
	t.Cleanup(func() { _ = backend.DeleteObject(ctx, bucket, key) })

	if _, err := backend.PutObject(ctx, domain.PutObjectOptions{
		Bucket: bucket, Key: key,
		Body: bytes.NewReader(body),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	head, err := backend.HeadObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if head.Size != int64(len(body)) {
		t.Errorf("HeadObject.Size = %d, want %d (sockerless#174 may have regressed)", head.Size, len(body))
	}

	got, err := backend.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer got.Body.Close()
	data, _ := io.ReadAll(got.Body)
	if !bytes.Equal(data, body) {
		t.Errorf("GetObject body = %q, want %q (sockerless#174 may have regressed)", string(data), string(body))
	}
}

// newSockerlessAWSClient builds an *awss3.Client tuned for sockerless:
// path-style URLs, request-checksum WhenRequired (sockerless serves
// HTTP-only by default and the SDK refuses streaming-signed payloads
// over plain HTTP), and InsecureSkipVerify if AWS_S3_CONFORMANCE_INSECURE_TLS=1.
func newSockerlessAWSClient(t *testing.T, endpoint string) *awss3.Client {
	t.Helper()
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	cfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	return awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
}

// TestSockerless_GCS_RoundTrip drives the shim's GCS backend against
// a running sockerless GCP simulator. Set SOCKERLESS_GCP_ENDPOINT
// (host:port, e.g. localhost:14567) to opt in.
//
// Sockerless's GCP sim implements the canonical GCS REST routes at
// /storage/v1/b/... and expects the SDK's STORAGE_EMULATOR_HOST env
// driver (not option.WithEndpoint, which doesn't reroute every API
// surface the SDK touches).
func TestSockerless_GCS_RoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	t.Setenv("STORAGE_EMULATOR_HOST", endpoint)
	ctx := context.Background()
	client, err := gcsstorage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("gcs client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcsbackend.New(client, gcsbackend.Config{ProjectID: project})

	bucket := randomNamespace("shim-gcs")
	if err := backend.CreateBucket(ctx, bucket, "us-central1"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteBucket(ctx, bucket) })

	key := "rt/" + randomNamespace("obj") + ".bin"
	body := []byte("sockerless GCS round-trip")
	if _, err := backend.PutObject(ctx, domain.PutObjectOptions{
		Bucket: bucket, Key: key,
		Body: bytes.NewReader(body),
	}); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteObject(ctx, bucket, key) })

	got, err := backend.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer got.Body.Close()
	data, _ := io.ReadAll(got.Body)
	if !bytes.Equal(data, body) {
		t.Errorf("GetObject body = %q, want %q", string(data), string(body))
	}
}

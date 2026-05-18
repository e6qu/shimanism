// Cross-backend conformance: exercises the SDK against every backend
// the conformance factory list publishes. Each backend's factory
// decides whether to skip (typically when its env var is unset). In
// CI, one job per backend sets the env vars its docker container
// exposes — `MINIO_ENDPOINT`, `STORAGE_EMULATOR_HOST` +
// `GCS_PROJECT_ID`, `AZURE_STORAGE_CONNECTION_STRING`, etc.
//
// The matrix is intentionally narrow: this is a smoke test that the
// shim's translation layer works against every real backend. The
// deeper protocol tests (TestSDK_*) run on the always-on `inmem`
// backend and don't need re-running per backend; what changes
// across backends is the bytes-out-the-other-side correctness.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/e6qu/shimanism/internal/harness"
)

// TestConformance_AllBackends sweeps every registered backend
// factory through a Put → Head → Get → Delete loop. Object key
// space is randomised per run so concurrent CI lanes don't trample
// each other against a shared real-cloud backend.
func TestConformance_AllBackends(t *testing.T) {
	for _, bf := range activeBackends() {
		bf := bf
		t.Run(bf.name, func(t *testing.T) {
			backend := bf.fn(t) // may t.Skip if its env-var is unset
			srv := harness.StartStorageServer(t, backend)
			cli := newS3Client(t, srv.URL)
			ctx := context.Background()

			// Random bucket + key so multi-lane CI / repeated runs against
			// a shared real-cloud backend stay isolated.
			bucket := randomNamespace("shim-conform")
			key := "rt/" + randomNamespace("obj") + ".bin"
			body := []byte("conformance payload — " + bf.name)

			t.Cleanup(func() {
				_, _ = cli.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: aws.String(bucket), Key: aws.String(key),
				})
				_, _ = cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
			})

			if _, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
				// Some backends (notably real AWS) may reject names that
				// look randomly generated if the test account is locked
				// down. Surface clearly.
				t.Fatalf("CreateBucket(%s): %v", bucket, err)
			}
			if _, err := cli.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(key),
				Body: bytes.NewReader(body),
			}); err != nil {
				t.Fatalf("PutObject: %v", err)
			}
			head, err := cli.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(key),
			})
			if err != nil {
				t.Fatalf("HeadObject: %v", err)
			}
			if got, want := aws.ToInt64(head.ContentLength), int64(len(body)); got != want {
				t.Errorf("HeadObject ContentLength = %d, want %d", got, want)
			}
			get, err := cli.GetObject(ctx, &s3.GetObjectInput{
				Bucket: aws.String(bucket), Key: aws.String(key),
			})
			if err != nil {
				t.Fatalf("GetObject: %v", err)
			}
			got, err := io.ReadAll(get.Body)
			_ = get.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("GetObject body = %q, want %q", got, body)
			}
		})
	}
}

// randomNamespace returns a short suffix-tagged identifier safe for
// S3 bucket / object naming. 8 hex characters is enough collision
// resistance for a CI run.
func randomNamespace(prefix string) string {
	var buf [4]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		// crypto/rand should never fail; fall through to a deterministic
		// fallback so the test surfaces the *real* error rather than a
		// random-source panic.
		return prefix + "-fallback"
	}
	return fmt.Sprintf("%s-%x", strings.ToLower(prefix), buf[:])
}

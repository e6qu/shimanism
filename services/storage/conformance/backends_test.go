// Cross-frontend × cross-backend conformance: for every frontend in
// {AWS S3, GCS, Azure Blob} and every backend in
// {inmem, minio, aws, gcs, azureblob}, drive a Put → Head → Get →
// Delete round-trip via the matching cloud's official Go SDK. Each
// backend factory decides whether to skip (typically when its
// container / env var is unset). CI lights up one backend per job
// by setting that backend's env var, so each job exercises every
// frontend against one backend.
//
// The intent is to prove the shim's translation layer works for
// every (frontend wire protocol, backend cloud) pair — the
// 3 × 5 = 15 SDK-backend combos that make up the SDK row of the
// PLAN.md exit-criteria matrix.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"testing"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/conformance"
)

// TestConformanceMatrix_AWSFrontend drives the AWS-shaped frontend
// across every backend with the official aws-sdk-go-v2 SDK.
func TestConformanceMatrix_AWSFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartStorageServer(t, backend)
			cli := newS3Client(t, srv.URL)
			ctx := context.Background()

			bucket := randomNamespace("shim-aws")
			key := "rt/" + randomNamespace("obj") + ".bin"
			body := []byte("conformance via AWS frontend → " + bf.Name)

			t.Cleanup(func() {
				_, _ = cli.DeleteObject(ctx, &s3.DeleteObjectInput{
					Bucket: awsapi.String(bucket), Key: awsapi.String(key),
				})
				_, _ = cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: awsapi.String(bucket)})
			})

			if _, err := cli.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: awsapi.String(bucket)}); err != nil {
				t.Fatalf("CreateBucket(%s): %v", bucket, err)
			}
			if _, err := cli.PutObject(ctx, &s3.PutObjectInput{
				Bucket: awsapi.String(bucket), Key: awsapi.String(key),
				Body: bytes.NewReader(body),
			}); err != nil {
				t.Fatalf("PutObject: %v", err)
			}
			head, err := cli.HeadObject(ctx, &s3.HeadObjectInput{
				Bucket: awsapi.String(bucket), Key: awsapi.String(key),
			})
			if err != nil {
				t.Fatalf("HeadObject: %v", err)
			}
			if got, want := awsapi.ToInt64(head.ContentLength), int64(len(body)); got != want {
				t.Errorf("HeadObject ContentLength = %d, want %d", got, want)
			}
			get, err := cli.GetObject(ctx, &s3.GetObjectInput{
				Bucket: awsapi.String(bucket), Key: awsapi.String(key),
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

// TestConformanceMatrix_GCSFrontend drives the GCS-shaped frontend
// across every backend with the official cloud.google.com/go/storage
// SDK.
func TestConformanceMatrix_GCSFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartStorageServerGCS(t, backend)
			cli := newGCSClient(t, srv.URL)
			ctx := context.Background()
			project := "shim-conformance"

			bucket := randomNamespace("shim-gcs")
			key := "rt/" + randomNamespace("obj") + ".bin"
			body := []byte("conformance via GCS frontend → " + bf.Name)

			t.Cleanup(func() {
				_ = cli.Bucket(bucket).Object(key).Delete(ctx)
				_ = cli.Bucket(bucket).Delete(ctx)
			})

			if err := cli.Bucket(bucket).Create(ctx, project, nil); err != nil {
				t.Fatalf("Bucket.Create(%s): %v", bucket, err)
			}
			wr := cli.Bucket(bucket).Object(key).NewWriter(ctx)
			if _, err := wr.Write(body); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if err := wr.Close(); err != nil {
				t.Fatalf("Close writer: %v", err)
			}
			attrs, err := cli.Bucket(bucket).Object(key).Attrs(ctx)
			if err != nil {
				t.Fatalf("Object.Attrs: %v", err)
			}
			if attrs.Size != int64(len(body)) {
				t.Errorf("Attrs.Size = %d, want %d", attrs.Size, len(body))
			}
			rd, err := cli.Bucket(bucket).Object(key).NewReader(ctx)
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			got, err := io.ReadAll(rd)
			_ = rd.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("body = %q, want %q", got, body)
			}
		})
	}
}

// TestConformanceMatrix_AzureBlobFrontend drives the Azure-Blob-shaped
// frontend across every backend with the official
// azure-sdk-for-go/sdk/storage/azblob SDK.
func TestConformanceMatrix_AzureBlobFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartStorageServerAzureBlob(t, backend)
			cli := newAzureBlobClient(t, srv.URL)
			ctx := context.Background()

			container := randomNamespace("shim-az")
			blobName := "rt/" + randomNamespace("obj") + ".bin"
			body := []byte("conformance via Azure Blob frontend → " + bf.Name)

			t.Cleanup(func() {
				_, _ = cli.DeleteBlob(ctx, container, blobName, nil)
				_, _ = cli.DeleteContainer(ctx, container, nil)
			})

			if _, err := cli.CreateContainer(ctx, container, nil); err != nil {
				t.Fatalf("CreateContainer(%s): %v", container, err)
			}
			if _, err := cli.UploadBuffer(ctx, container, blobName, body, &azblob.UploadBufferOptions{}); err != nil {
				t.Fatalf("UploadBuffer: %v", err)
			}
			props, err := cli.ServiceClient().NewContainerClient(container).NewBlobClient(blobName).GetProperties(ctx, nil)
			if err != nil {
				t.Fatalf("GetProperties: %v", err)
			}
			if props.ContentLength != nil && *props.ContentLength != int64(len(body)) {
				t.Errorf("ContentLength = %d, want %d", *props.ContentLength, len(body))
			}
			rd, err := cli.DownloadStream(ctx, container, blobName, &blob.DownloadStreamOptions{})
			if err != nil {
				t.Fatalf("DownloadStream: %v", err)
			}
			got, err := io.ReadAll(rd.Body)
			_ = rd.Body.Close()
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if !bytes.Equal(got, body) {
				t.Errorf("body = %q, want %q", got, body)
			}
		})
	}
}

// randomNamespace returns a short suffix-tagged identifier safe for
// bucket / container / object naming across all three cloud
// conventions. 8 hex characters of crypto/rand is enough collision
// resistance for a CI run.
func randomNamespace(prefix string) string {
	var buf [4]byte
	if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
		return prefix + "-fallback"
	}
	return fmt.Sprintf("%s-%x", strings.ToLower(prefix), buf[:])
}

// Force a dependency on cloud.google.com/go/storage so the import
// survives goimports / unused-import passes in editor environments.
var _ = gcsstorage.NewClient

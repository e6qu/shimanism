// Sockerless lane for the storage service.
//
// Tests in this file point the shim's cloud backends at a running
// sockerless simulator instance so the AWS / GCP / Azure backend
// translation layers can be exercised without real cloud accounts.
// Each sub-test skips when its driver env var isn't set, so the
// default `go test ./...` lane is unaffected.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"testing"

	gcsstorage "cloud.google.com/go/storage"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/storage/domain"
	awsbackend "github.com/e6qu/shimanism/services/storage/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/storage/backends/azureblob"
	gcsbackend "github.com/e6qu/shimanism/services/storage/backends/gcs"
)

// TestSockerless_AWS_S3RoundTrip drives the shim's AWS-shaped
// frontend → AWS S3 backend → sockerless AWS simulator end-to-end:
// CreateBucket, ListBuckets, PutObject, GetObject, HeadObject,
// DeleteObject, DeleteBucket. Set SOCKERLESS_AWS_ENDPOINT (the
// sim's HTTPS listener; no path prefix) to opt in.
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

	// Non-seekable body exercises the SDK's aws-chunked transfer-
	// encoding path; the sim must decode the chunked envelope before
	// persisting or the round-trip fails.
	key := "rt/" + randomNamespace("obj") + ".bin"
	body := []byte("sockerless AWS S3 round-trip")
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
		t.Errorf("HeadObject.Size = %d, want %d", head.Size, len(body))
	}

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

// newSockerlessAWSClient builds an *awss3.Client tuned for sockerless:
// path-style URLs, request-checksum WhenRequired, and
// InsecureSkipVerify if AWS_S3_CONFORMANCE_INSECURE_TLS=1 (to trust
// the sim's self-signed cert).
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

// TestSockerless_GCS_RoundTrip drives the shim's GCS backend
// against a running sockerless GCP simulator. Set
// SOCKERLESS_GCP_ENDPOINT (host:port, e.g. localhost:14567) to
// opt in. The GCS Go SDK's STORAGE_EMULATOR_HOST env var is what
// reroutes the SDK; option.WithEndpoint alone doesn't.
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

// TestSockerless_Azure_Blob_RoundTrip drives the shim's Azure Blob
// storage backend end-to-end against a running sockerless Azure sim
// under TLS: CreateBucket → PutObject → HeadObject → GetObject →
// DeleteObject → DeleteBucket.
//
// Set SOCKERLESS_AZURE_BLOB_ACCOUNT (e.g. `testacct`) and
// SOCKERLESS_AZURE_TLS_PORT (e.g. `14569`) to opt in. The Azure
// SDK is configured with `BlobEndpoint=https://{account}.blob.localhost:{port}`
// so the request hits the sim's host-based dispatcher.
func TestSockerless_Azure_Blob_RoundTrip(t *testing.T) {
	account := os.Getenv("SOCKERLESS_AZURE_BLOB_ACCOUNT")
	if account == "" {
		t.Skip("SOCKERLESS_AZURE_BLOB_ACCOUNT not set")
	}
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	// 32 bytes of fixed pseudo-key material, base64-encoded. Real
	// callers configure a real account key; sockerless validates only
	// the SharedKey signature form, not the key contents.
	keyMaterial := bytes.Repeat([]byte("k"), 32)
	encodedKey := base64.StdEncoding.EncodeToString(keyMaterial)
	cred, err := azblob.NewSharedKeyCredential(account, encodedKey)
	if err != nil {
		t.Fatalf("shared-key credential: %v", err)
	}

	vaultURL := "https://" + account + ".blob.localhost:" + port + "/"
	c, err := azblob.NewClientWithSharedKeyCredential(vaultURL, cred, &azblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &http.Client{Transport: storageLocalhostDial(port)}},
	})
	if err != nil {
		t.Fatalf("blob client: %v", err)
	}
	backend := azurebackend.New(c, "eastus")
	ctx := context.Background()

	bucket := randomNamespace("shim-azblob")
	if err := backend.CreateBucket(ctx, bucket, "eastus"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteBucket(ctx, bucket) })

	key := "rt/" + randomNamespace("obj") + ".bin"
	body := []byte("sockerless Azure Blob round-trip")
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

// TestSockerless_Azure_Blob_Multipart exercises the shim's azureblob
// backend's block-blob staging code path against sockerless's
// block-blob staging support (added in sockerless PR #229).
// CreateMultipartUpload → UploadPart × N → ListParts →
// CompleteMultipartUpload → GetObject, asserts the assembled blob
// content matches the concatenation of all uploaded parts.
//
// On the wire this drives StageBlock (`?comp=block&blockid=…`) for
// each part, CommitBlockList (`?comp=blocklist` with the XML block
// list body) for the finalize, and GetBlockList
// (`?comp=blocklist&blocklisttype=uncommitted`) for ListParts. The
// test never speaks any of those wire shapes directly — it just
// drives the shim's domain interface, which calls the Azure SDK,
// which speaks block-blob HTTP to sockerless.
func TestSockerless_Azure_Blob_Multipart(t *testing.T) {
	account := os.Getenv("SOCKERLESS_AZURE_BLOB_ACCOUNT")
	if account == "" {
		t.Skip("SOCKERLESS_AZURE_BLOB_ACCOUNT not set")
	}
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	keyMaterial := bytes.Repeat([]byte("k"), 32)
	encodedKey := base64.StdEncoding.EncodeToString(keyMaterial)
	cred, err := azblob.NewSharedKeyCredential(account, encodedKey)
	if err != nil {
		t.Fatalf("shared-key credential: %v", err)
	}
	vaultURL := "https://" + account + ".blob.localhost:" + port + "/"
	c, err := azblob.NewClientWithSharedKeyCredential(vaultURL, cred, &azblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &http.Client{Transport: storageLocalhostDial(port)}},
	})
	if err != nil {
		t.Fatalf("blob client: %v", err)
	}
	backend := azurebackend.New(c, "eastus")
	ctx := context.Background()

	bucket := randomNamespace("shim-azblob-mp")
	if err := backend.CreateBucket(ctx, bucket, "eastus"); err != nil {
		t.Fatalf("CreateBucket: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteBucket(ctx, bucket) })

	key := "mp/" + randomNamespace("obj") + ".bin"
	uploadID, err := backend.CreateMultipartUpload(ctx, bucket, key, "application/octet-stream", nil)
	if err != nil {
		t.Fatalf("CreateMultipartUpload: %v", err)
	}
	t.Cleanup(func() { _ = backend.AbortMultipartUpload(ctx, bucket, key, uploadID) })

	parts := [][]byte{
		bytes.Repeat([]byte("a"), 1024),
		bytes.Repeat([]byte("b"), 1024),
		bytes.Repeat([]byte("c"), 512),
	}
	completed := make([]domain.CompletePartRef, len(parts))
	for i, part := range parts {
		partNumber := int32(i + 1)
		etag, err := backend.UploadPart(ctx, bucket, key, uploadID, partNumber, bytes.NewReader(part))
		if err != nil {
			t.Fatalf("UploadPart %d: %v", partNumber, err)
		}
		completed[i] = domain.CompletePartRef{Number: partNumber, ETag: etag}
	}

	listed, err := backend.ListParts(ctx, bucket, key, uploadID)
	if err != nil {
		t.Fatalf("ListParts: %v", err)
	}
	if len(listed) != len(parts) {
		t.Errorf("ListParts returned %d parts, want %d", len(listed), len(parts))
	}

	if _, err := backend.CompleteMultipartUpload(ctx, bucket, key, uploadID, completed); err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteObject(ctx, bucket, key) })

	got, err := backend.GetObject(ctx, bucket, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer got.Body.Close()
	data, _ := io.ReadAll(got.Body)
	want := bytes.Join(parts, nil)
	if !bytes.Equal(data, want) {
		t.Errorf("multipart-assembled blob length = %d, want %d (parts concatenated)", len(data), len(want))
	}
}

// TestSockerless_E2E_AWSFrontendToGCSBackend drives a real AWS S3
// client into shimanism's AWS frontend, through the GCS backend, and
// out to the sockerless GCP simulator. This is the concrete AWS -> GCP
// migration path for storage.
func TestSockerless_E2E_AWSFrontendToGCSBackend(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	t.Setenv("STORAGE_EMULATOR_HOST", endpoint)
	ctx := context.Background()
	gcsClient, err := gcsstorage.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("gcs client: %v", err)
	}
	t.Cleanup(func() { _ = gcsClient.Close() })
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcsbackend.New(gcsClient, gcsbackend.Config{ProjectID: project})
	shim := harness.StartStorageServer(t, backend)
	awsClient := newS3Client(t, shim.URL)

	bucket := randomNamespace("e2e-aws-gcp")
	key := "rt/" + randomNamespace("obj") + ".txt"
	body := []byte("aws frontend to gcs backend through sockerless")

	if _, err := awsClient.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("CreateBucket via AWS frontend: %v", err)
	}
	t.Cleanup(func() {
		_, _ = awsClient.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		_, _ = awsClient.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(bucket)})
	})
	if _, err := awsClient.PutObject(ctx, &awss3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}); err != nil {
		t.Fatalf("PutObject via AWS frontend: %v", err)
	}
	got, err := awsClient.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("GetObject via AWS frontend: %v", err)
	}
	defer got.Body.Close()
	data, _ := io.ReadAll(got.Body)
	if !bytes.Equal(data, body) {
		t.Errorf("GetObject body = %q, want %q", string(data), string(body))
	}
}

// TestSockerless_E2E_GCSFrontendToAzureBlobBackend drives a real GCS
// client into shimanism's GCS frontend, through the Azure Blob backend,
// and out to the sockerless Azure simulator. This is the concrete
// GCP -> Azure migration path for storage.
func TestSockerless_E2E_GCSFrontendToAzureBlobBackend(t *testing.T) {
	account := os.Getenv("SOCKERLESS_AZURE_BLOB_ACCOUNT")
	if account == "" {
		t.Skip("SOCKERLESS_AZURE_BLOB_ACCOUNT not set")
	}
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	keyMaterial := bytes.Repeat([]byte("k"), 32)
	encodedKey := base64.StdEncoding.EncodeToString(keyMaterial)
	cred, err := azblob.NewSharedKeyCredential(account, encodedKey)
	if err != nil {
		t.Fatalf("shared-key credential: %v", err)
	}
	blobURL := "https://" + account + ".blob.localhost:" + port + "/"
	blobClient, err := azblob.NewClientWithSharedKeyCredential(blobURL, cred, &azblob.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: &http.Client{Transport: storageLocalhostDial(port)}},
	})
	if err != nil {
		t.Fatalf("blob client: %v", err)
	}
	backend := azurebackend.New(blobClient, "eastus")
	shim := harness.StartStorageServerGCS(t, backend)
	gcsClient := newGCSClient(t, shim.URL)
	ctx := context.Background()

	bucket := randomNamespace("e2e-gcp-azure")
	key := "rt/" + randomNamespace("obj") + ".txt"
	body := []byte("gcs frontend to azure blob backend through sockerless")

	if err := gcsClient.Bucket(bucket).Create(ctx, "shim-sockerless", nil); err != nil {
		t.Fatalf("Create bucket via GCS frontend: %v", err)
	}
	t.Cleanup(func() {
		_ = gcsClient.Bucket(bucket).Object(key).Delete(ctx)
		_ = gcsClient.Bucket(bucket).Delete(ctx)
	})
	wr := gcsClient.Bucket(bucket).Object(key).NewWriter(ctx)
	if _, err := wr.Write(body); err != nil {
		t.Fatalf("Write via GCS frontend: %v", err)
	}
	if err := wr.Close(); err != nil {
		t.Fatalf("Close writer via GCS frontend: %v", err)
	}
	rd, err := gcsClient.Bucket(bucket).Object(key).NewReader(ctx)
	if err != nil {
		t.Fatalf("Read via GCS frontend: %v", err)
	}
	data, err := io.ReadAll(rd)
	_ = rd.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(data, body) {
		t.Errorf("GCS reader body = %q, want %q", string(data), string(body))
	}
}

// TestSockerless_E2E_AzureBlobFrontendToAWSBackend drives a real Azure
// Blob client into shimanism's Azure Blob frontend, through the AWS S3
// backend, and out to the sockerless AWS simulator. This is the
// concrete Azure -> AWS migration path for storage.
func TestSockerless_E2E_AzureBlobFrontendToAWSBackend(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	backend := awsbackend.New(newSockerlessAWSClient(t, endpoint))
	shim := harness.StartStorageServerAzureBlob(t, backend)
	blobClient := newAzureBlobClient(t, shim.URL)
	ctx := context.Background()

	containerName := randomNamespace("e2e-azure-aws")
	blobName := "rt/" + randomNamespace("obj") + ".txt"
	body := []byte("azure blob frontend to aws backend through sockerless")

	if _, err := blobClient.CreateContainer(ctx, containerName, nil); err != nil {
		t.Fatalf("CreateContainer via Azure frontend: %v", err)
	}
	t.Cleanup(func() {
		_, _ = blobClient.DeleteBlob(ctx, containerName, blobName, nil)
		_, _ = blobClient.DeleteContainer(ctx, containerName, nil)
	})
	if _, err := blobClient.UploadBuffer(ctx, containerName, blobName, body, &azblob.UploadBufferOptions{}); err != nil {
		t.Fatalf("UploadBuffer via Azure frontend: %v", err)
	}
	got, err := blobClient.DownloadStream(ctx, containerName, blobName, nil)
	if err != nil {
		t.Fatalf("DownloadStream via Azure frontend: %v", err)
	}
	data, err := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(data, body) {
		t.Errorf("Azure download body = %q, want %q", string(data), string(body))
	}
}

// storageLocalhostDial returns an *http.Transport that routes every
// outbound TCP connection to 127.0.0.1:port. Preserves the
// SDK-supplied Host header so sockerless's host-based dispatch
// sees the configured account name.
func storageLocalhostDial(port string) *http.Transport {
	d := &net.Dialer{}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return d.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
}

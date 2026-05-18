// Package conformance_test (non-test file): backend factories used
// by parameterised conformance tests. Each factory is named the same
// as the backend it produces; a per-PR conformance lane picks one
// factory at a time (controlled via env vars so CI can light up
// each backend in its own job without modifying the test source).
package conformance_test

import (
	"context"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	gcsstorage "cloud.google.com/go/storage"

	"github.com/e6qu/shimanism/internal/storage/domain"
	awsbackend "github.com/e6qu/shimanism/services/storage/backends/aws"
	azureblobbackend "github.com/e6qu/shimanism/services/storage/backends/azureblob"
	gcsbackend "github.com/e6qu/shimanism/services/storage/backends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	miniobackend "github.com/e6qu/shimanism/services/storage/backends/minio"
)

// backendFactory returns a Storage backend ready for use. Each
// factory may call t.Skip if its required infrastructure
// (Docker container, env var, …) isn't available.
type backendFactory struct {
	name string
	fn   func(t *testing.T) domain.Storage
}

// activeBackends returns the set of backend factories to drive the
// conformance suite against. Lists every backend; each factory
// internally decides whether to skip.
func activeBackends() []backendFactory {
	return []backendFactory{
		{name: "inmem", fn: newInmem},
		{name: "minio", fn: newMinIO},
		{name: "aws", fn: newAWS},
		{name: "gcs", fn: newGCS},
		{name: "azureblob", fn: newAzureBlob},
	}
}

// newInmem is always available — no external dependencies.
func newInmem(t *testing.T) domain.Storage {
	t.Helper()
	return inmem.New()
}

// newAWS connects to real AWS S3 (or an S3-compatible endpoint) when
// AWS_S3_CONFORMANCE_ENDPOINT is set. The endpoint may be the empty
// string "default" to use the SDK's normal regional endpoint; in that
// case credentials come from the standard AWS env chain. When neither
// AWS_S3_CONFORMANCE_ENDPOINT nor AWS_S3_CONFORMANCE=1 is set, the
// factory skips.
func newAWS(t *testing.T) domain.Storage {
	t.Helper()
	endpoint := os.Getenv("AWS_S3_CONFORMANCE_ENDPOINT")
	if endpoint == "" && os.Getenv("AWS_S3_CONFORMANCE") != "1" {
		t.Skip("AWS_S3_CONFORMANCE_ENDPOINT not set and AWS_S3_CONFORMANCE!=1 (AWS backend conformance disabled)")
	}
	// We import the AWS SDK config lazily here to keep this file
	// tolerant of go-tooling minimisation; the import is at the
	// package level via awsbackend's transitive deps.
	client, err := buildAWSS3Client(t, endpoint)
	if err != nil {
		t.Fatalf("build AWS S3 client (endpoint=%q): %v", endpoint, err)
	}
	return awsbackend.New(client)
}

// newGCS connects to GCS via cloud.google.com/go/storage. The standard
// STORAGE_EMULATOR_HOST env var is honoured by the GCS Go SDK to redirect
// at a fake-gcs-server instance. GCS_PROJECT_ID is required (the GCS API
// requires it for CreateBucket / ListBuckets). Skipped if neither
// STORAGE_EMULATOR_HOST nor GCS_CONFORMANCE=1 is set.
func newGCS(t *testing.T) domain.Storage {
	t.Helper()
	if os.Getenv("STORAGE_EMULATOR_HOST") == "" && os.Getenv("GCS_CONFORMANCE") != "1" {
		t.Skip("STORAGE_EMULATOR_HOST not set and GCS_CONFORMANCE!=1 (GCS backend conformance disabled)")
	}
	project := os.Getenv("GCS_PROJECT_ID")
	if project == "" {
		project = "shim-conformance"
	}
	client, err := buildGCSClient(context.Background())
	if err != nil {
		t.Fatalf("build GCS client: %v", err)
	}
	return gcsbackend.New(client, gcsbackend.Config{ProjectID: project})
}

// newAzureBlob connects to Azure Blob Storage. With Azurite (the local
// emulator), AZURE_STORAGE_CONNECTION_STRING is the standard env var
// the Azure SDK consumes. Skipped if neither AZURE_STORAGE_CONNECTION_STRING
// nor AZURE_BLOB_CONFORMANCE=1 is set.
func newAzureBlob(t *testing.T) domain.Storage {
	t.Helper()
	conn := os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	if conn == "" && os.Getenv("AZURE_BLOB_CONFORMANCE") != "1" {
		t.Skip("AZURE_STORAGE_CONNECTION_STRING not set and AZURE_BLOB_CONFORMANCE!=1 (Azure Blob backend conformance disabled)")
	}
	client, err := buildAzureBlobClient(conn)
	if err != nil {
		t.Fatalf("build Azure Blob client: %v", err)
	}
	return azureblobbackend.New(client, os.Getenv("AZURE_BLOB_REGION"))
}

// newMinIO connects to a MinIO server using MINIO_ENDPOINT /
// MINIO_ACCESS_KEY / MINIO_SECRET_KEY env vars. Skipped if
// MINIO_ENDPOINT is not set. CI starts a MinIO container in a
// pre-step and sets the env vars.
func newMinIO(t *testing.T) domain.Storage {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT not set (MinIO backend conformance disabled)")
	}
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	b, err := miniobackend.New(miniobackend.Config{
		Endpoint:  endpoint,
		AccessKey: access,
		SecretKey: secret,
	})
	if err != nil {
		t.Fatalf("connect to MinIO at %s: %v", endpoint, err)
	}
	return b
}

// buildAWSS3Client constructs an *s3.Client using the standard AWS env
// chain. If endpoint is non-empty and not "default", it is used as the
// BaseEndpoint (path-style URLs are enabled in that case, since most
// S3-compatible endpoints accessed via IP/hostname require it).
func buildAWSS3Client(t *testing.T, endpoint string) (*awss3.Client, error) {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, err
	}
	opts := []func(*awss3.Options){}
	if endpoint != "" && endpoint != "default" {
		opts = append(opts, func(o *awss3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}
	return awss3.NewFromConfig(cfg, opts...), nil
}

// buildGCSClient constructs a *storage.Client. The GCS Go SDK honours
// STORAGE_EMULATOR_HOST automatically, so the caller does not need to
// route the endpoint manually.
func buildGCSClient(ctx context.Context) (*gcsstorage.Client, error) {
	return gcsstorage.NewClient(ctx)
}

// buildAzureBlobClient constructs an *azblob.Client from a connection
// string (the form the Azure SDK accepts directly, including the
// Azurite default "DefaultEndpointsProtocol=http;…").
func buildAzureBlobClient(conn string) (*azblob.Client, error) {
	return azblob.NewClientFromConnectionString(conn, nil)
}

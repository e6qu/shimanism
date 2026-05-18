// Package conformance hosts the backend factories used by the
// parameterised conformance tests in this directory. Tests live in
// `package conformance_test` (external tests) and import the
// exported factory set from here. Each factory is named the same
// as the backend it produces; a per-PR conformance lane picks one
// factory at a time (controlled via env vars so CI can light up
// each backend in its own job without modifying the test source).
package conformance

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

// BackendFactory returns a Storage backend ready for use. Each
// factory may call t.Skip if its required infrastructure
// (Docker container, env var, …) isn't available.
type BackendFactory struct {
	Name string
	Fn   func(t *testing.T) domain.Storage
}

// ActiveBackends returns the set of backend factories to drive the
// conformance suite against. Lists every backend; each factory
// internally decides whether to skip.
func ActiveBackends() []BackendFactory {
	return []BackendFactory{
		{Name: "inmem", Fn: NewInmem},
		{Name: "minio", Fn: NewMinIO},
		{Name: "aws", Fn: NewAWS},
		{Name: "gcs", Fn: NewGCS},
		{Name: "azureblob", Fn: NewAzureBlob},
	}
}

// NewInmem is always available — no external dependencies.
func NewInmem(t *testing.T) domain.Storage {
	t.Helper()
	return inmem.New()
}

// NewAWS connects to real AWS S3 (or an S3-compatible endpoint) when
// AWS_S3_CONFORMANCE_ENDPOINT is set. The endpoint may be the empty
// string "default" to use the SDK's normal regional endpoint; in that
// case credentials come from the standard AWS env chain. When neither
// AWS_S3_CONFORMANCE_ENDPOINT nor AWS_S3_CONFORMANCE=1 is set, the
// factory skips.
func NewAWS(t *testing.T) domain.Storage {
	t.Helper()
	endpoint := os.Getenv("AWS_S3_CONFORMANCE_ENDPOINT")
	if endpoint == "" && os.Getenv("AWS_S3_CONFORMANCE") != "1" {
		t.Skip("AWS_S3_CONFORMANCE_ENDPOINT not set and AWS_S3_CONFORMANCE!=1 (AWS backend conformance disabled)")
	}
	client, err := buildAWSS3Client(t, endpoint)
	if err != nil {
		t.Fatalf("build AWS S3 client (endpoint=%q): %v", endpoint, err)
	}
	return awsbackend.New(client)
}

// NewGCS connects to GCS via cloud.google.com/go/storage. The standard
// STORAGE_EMULATOR_HOST env var is honoured by the GCS Go SDK to redirect
// at a fake-gcs-server instance. GCS_PROJECT_ID is required (the GCS API
// requires it for CreateBucket / ListBuckets). Skipped if neither
// STORAGE_EMULATOR_HOST nor GCS_CONFORMANCE=1 is set.
func NewGCS(t *testing.T) domain.Storage {
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

// NewAzureBlob connects to Azure Blob Storage. With Azurite (the local
// emulator), AZURE_STORAGE_CONNECTION_STRING is the standard env var
// the Azure SDK consumes. Skipped if neither AZURE_STORAGE_CONNECTION_STRING
// nor AZURE_BLOB_CONFORMANCE=1 is set.
func NewAzureBlob(t *testing.T) domain.Storage {
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

// NewMinIO connects to a MinIO server using MINIO_ENDPOINT /
// MINIO_ACCESS_KEY / MINIO_SECRET_KEY env vars. Skipped if
// MINIO_ENDPOINT is not set. CI starts a MinIO container in a
// pre-step and sets the env vars.
func NewMinIO(t *testing.T) domain.Storage {
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

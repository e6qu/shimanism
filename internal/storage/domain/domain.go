// Package domain holds shimanism's neutral object-storage interface
// and types. The interface is the lingua franca between three
// frontend protocols (AWS S3, GCS, Azure Blob) and five backend
// implementations (AWS, MinIO, GCS, Azure Blob, K8s peer); each
// frontend translates its wire types into this domain, each backend
// translates this domain into its cloud's native API.
//
// See doc/CROSS_CLOUD_ROUTING.md for the architecture and rationale.
package domain

import (
	"context"
	"io"
	"time"
)

// Bucket is a single storage bucket / container.
type Bucket struct {
	Name      string
	CreatedAt time.Time
	Region    string
}

// Owner identifies the bucket's owning principal.
type Owner struct {
	ID          string
	DisplayName string
}

// Object describes a stored object. Body is non-nil only on Get
// results; the caller must Close it after streaming through.
type Object struct {
	Bucket       string
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	ContentType  string
	Metadata     map[string]string
	// Body streams the object payload. Non-nil on GetObject results;
	// nil on HeadObject results. The caller must Close it to release
	// the underlying network connection back to the SDK's pool.
	Body io.ReadCloser
}

// ObjectMetadata is the Head-level info returned by ListObjects (no body).
type ObjectMetadata struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	StorageClass string
}

// MultipartUpload describes an in-progress multipart upload session.
type MultipartUpload struct {
	UploadID  string
	Bucket    string
	Key       string
	Initiated time.Time
}

// Part describes one part of an in-progress multipart upload.
type Part struct {
	Number       int32
	ETag         string
	Size         int64
	LastModified time.Time
}

// CompletePartRef is the input shape for CompleteMultipartUpload.
type CompletePartRef struct {
	Number int32
	ETag   string
}

// ListBucketsOptions bundles ListBuckets request options.
type ListBucketsOptions struct {
	Prefix     string
	Region     string
	MaxResults int
	NextToken  string
}

// ListBucketsResult is the ListBuckets response.
type ListBucketsResult struct {
	Buckets   []Bucket
	NextToken string
	Owner     Owner
	Prefix    string
}

// ListObjectsOptions bundles ListObjects request options.
type ListObjectsOptions struct {
	Bucket     string
	Prefix     string
	Delimiter  string
	StartAfter string
	NextToken  string
	MaxResults int
}

// ListObjectsResult is the ListObjects response.
type ListObjectsResult struct {
	Bucket         string
	Objects        []ObjectMetadata
	CommonPrefixes []string
	NextToken      string
	IsTruncated    bool
	KeyCount       int
	Prefix         string
	Delimiter      string
}

// PutObjectOptions bundles PutObject request options. Body is the
// streaming reader; the backend must not require Body to satisfy
// io.Closer (the handler keeps ownership of r.Body).
type PutObjectOptions struct {
	Bucket      string
	Key         string
	Body        io.Reader
	ContentType string
	Metadata    map[string]string
}

// PutObjectResult is the PutObject response.
type PutObjectResult struct {
	ETag string
}

// CopyObjectOptions bundles CopyObject request options.
type CopyObjectOptions struct {
	SrcBucket string
	SrcKey    string
	DstBucket string
	DstKey    string
	// MetadataDirective is "COPY" or "REPLACE"; backends that don't
	// distinguish should treat empty as "COPY".
	MetadataDirective string
	Metadata          map[string]string
	ContentType       string
}

// CopyObjectResult is the CopyObject response.
type CopyObjectResult struct {
	ETag         string
	LastModified time.Time
}

// Storage is the interface every storage backend implements. Frontend
// adapters (one per source-cloud wire protocol) translate their wire
// types into calls on this interface; backends translate this
// interface into their cloud's native SDK calls.
type Storage interface {
	// Bucket lifecycle
	ListBuckets(ctx context.Context, opt ListBucketsOptions) (ListBucketsResult, error)
	CreateBucket(ctx context.Context, name, region string) error
	DeleteBucket(ctx context.Context, name string) error
	HeadBucket(ctx context.Context, name string) (Bucket, error)

	// Object lifecycle
	ListObjects(ctx context.Context, opt ListObjectsOptions) (ListObjectsResult, error)
	GetObject(ctx context.Context, bucket, key string) (Object, error)
	PutObject(ctx context.Context, opt PutObjectOptions) (PutObjectResult, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	HeadObject(ctx context.Context, bucket, key string) (Object, error)
	CopyObject(ctx context.Context, opt CopyObjectOptions) (CopyObjectResult, error)

	// Multipart upload
	CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (uploadID string, _ error)
	UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (etag string, _ error)
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePartRef) (etag string, _ error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]MultipartUpload, error)
	ListParts(ctx context.Context, bucket, key, uploadID string) ([]Part, error)
}

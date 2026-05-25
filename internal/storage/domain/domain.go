// Package domain holds shimanism's neutral object-storage interface
// and types. The interface is the lingua franca between three
// frontend protocols (AWS S3, GCS, Azure Blob) and five backend
// implementations (AWS, MinIO, GCS, Azure Blob, K8s peer); each
// frontend translates its wire types into this domain, each backend
// translates this domain into its cloud's native API.
//
// See docs/cross-cloud-routing.md for the architecture and rationale.
package domain

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
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

// MultipartETag computes the S3-style multipart ETag from the
// per-part ETags. S3's convention for a multipart object is:
//
//	"<md5(concat(decode(part_etag_hex)))>-<N>"
//
// where each part's ETag is the md5 of that part's body in hex
// (quoted in transit), and the final ETag concatenates the *raw*
// md5 bytes (not the hex), md5-hashes them, and suffixes the part
// count. Backends whose native multipart finalization returns a
// different shape (GCS composed-object Etag, Azure block-blob ETag,
// the in-mem md5-of-assembled-body) call this helper from
// CompleteMultipartUpload to return the S3-compatible ETag instead,
// so SDK clients verifying multipart ETags don't see drift across
// backends. Real-S3-compatible backends (AWS passthrough, MinIO)
// pass through the native ETag because it already matches.
func MultipartETag(parts []CompletePartRef) string {
	h := md5.New()
	for _, p := range parts {
		raw := strings.Trim(p.ETag, "\"")
		// A per-part ETag may itself be the multipart-suffix shape
		// `<hex>-<n>` if a client somehow uploaded a composed part
		// (not legal in S3 today but kept defensive). Strip any
		// suffix before decoding.
		if i := strings.IndexByte(raw, '-'); i >= 0 {
			raw = raw[:i]
		}
		b, err := hex.DecodeString(raw)
		if err != nil {
			// If a backend handed us a non-hex ETag (e.g. GCS's
			// base64-encoded CRC32C), fall back to hashing the
			// string itself so we still return *some* stable
			// per-multipart identifier instead of swallowing the
			// error silently.
			b = []byte(raw)
		}
		h.Write(b)
	}
	return fmt.Sprintf("\"%s-%d\"", hex.EncodeToString(h.Sum(nil)), len(parts))
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

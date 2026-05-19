// Package minio is the MinIO backend for shimanism's storage domain.
// MinIO speaks the S3 wire protocol natively, so this is the
// passthrough "control case" that proves the routing + adapter layers
// are faithful before we try a cross-shape translation.
//
// The backend uses minio-go (Apache 2.0). It accepts any
// S3-protocol-compatible endpoint, so the same backend code is used
// against MinIO, R2, B2, or any other S3-compatible service.
package minio

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	miniogo "github.com/minio/minio-go/v7"
	miniocreds "github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Config holds backend connection parameters.
type Config struct {
	Endpoint  string // "host:port" (no scheme)
	AccessKey string
	SecretKey string
	UseSSL    bool
	// Region is reported on bucket creation when MinIO supports it.
	Region string
}

// Backend implements domain.Storage by calling a MinIO endpoint.
type Backend struct {
	c      *miniogo.Client
	core   *miniogo.Core
	region string
}

// New connects to MinIO at the given endpoint. The connection is
// lazy (minio-go opens connections per request) so this only
// validates credentials.
func New(cfg Config) (*Backend, error) {
	opts := &miniogo.Options{
		Creds:  miniocreds.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	c, err := miniogo.New(cfg.Endpoint, opts)
	if err != nil {
		return nil, err
	}
	core, err := miniogo.NewCore(cfg.Endpoint, opts)
	if err != nil {
		return nil, err
	}
	r := cfg.Region
	if r == "" {
		r = "us-east-1"
	}
	return &Backend{c: c, core: core, region: r}, nil
}

// Compile-time check.
var _ domain.Storage = (*Backend)(nil)

// translateErr maps minio-go's error responses to domain errors.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	var er miniogo.ErrorResponse
	if errors.As(err, &er) {
		switch er.Code {
		case "NoSuchBucket":
			return domain.NoSuchBucket(er.BucketName)
		case "NoSuchKey":
			return domain.NoSuchKey(er.BucketName, er.Key)
		case "NoSuchUpload":
			return domain.NoSuchUpload(er.Key)
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return domain.BucketAlreadyExists(er.BucketName)
		case "BucketNotEmpty":
			return domain.BucketNotEmpty(er.BucketName)
		case "InvalidArgument":
			return domain.InvalidArgument(er.Message)
		}
	}
	return err
}

// ----------------------------------------------------------------------
// Bucket lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListBuckets(ctx context.Context, opt domain.ListBucketsOptions) (domain.ListBucketsResult, error) {
	buckets, err := b.c.ListBuckets(ctx)
	if err != nil {
		return domain.ListBucketsResult{}, translateErr(err)
	}
	res := domain.ListBucketsResult{Prefix: opt.Prefix}
	for _, mb := range buckets {
		if opt.Prefix != "" && !strings.HasPrefix(mb.Name, opt.Prefix) {
			continue
		}
		res.Buckets = append(res.Buckets, domain.Bucket{
			Name:      mb.Name,
			CreatedAt: mb.CreationDate,
			Region:    b.region,
		})
	}
	return res, nil
}

func (b *Backend) CreateBucket(ctx context.Context, name, region string) error {
	if region == "" {
		region = b.region
	}
	err := b.c.MakeBucket(ctx, name, miniogo.MakeBucketOptions{Region: region})
	if err == nil {
		return nil
	}
	// MinIO returns BucketAlreadyOwnedByYou on idempotent recreate;
	// translate to nil to match the in-mem behavior.
	var er miniogo.ErrorResponse
	if errors.As(err, &er) && (er.Code == "BucketAlreadyOwnedByYou" || er.Code == "BucketAlreadyExists") {
		return nil
	}
	return translateErr(err)
}

func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	return translateErr(b.c.RemoveBucket(ctx, name))
}

func (b *Backend) HeadBucket(ctx context.Context, name string) (domain.Bucket, error) {
	ok, err := b.c.BucketExists(ctx, name)
	if err != nil {
		return domain.Bucket{}, translateErr(err)
	}
	if !ok {
		return domain.Bucket{}, domain.NoSuchBucket(name)
	}
	return domain.Bucket{Name: name, Region: b.region}, nil
}

// ----------------------------------------------------------------------
// Object lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListObjects(ctx context.Context, opt domain.ListObjectsOptions) (domain.ListObjectsResult, error) {
	maxKeys := opt.MaxResults
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	res := domain.ListObjectsResult{
		Bucket:    opt.Bucket,
		Prefix:    opt.Prefix,
		Delimiter: opt.Delimiter,
	}
	listOpts := miniogo.ListObjectsOptions{
		Prefix:     opt.Prefix,
		Recursive:  opt.Delimiter == "",
		StartAfter: opt.StartAfter,
		MaxKeys:    maxKeys,
	}
	if opt.NextToken != "" {
		listOpts.StartAfter = opt.NextToken
	}
	cps := map[string]bool{}
	for info := range b.c.ListObjects(ctx, opt.Bucket, listOpts) {
		if info.Err != nil {
			return domain.ListObjectsResult{}, translateErr(info.Err)
		}
		// MinIO's ListObjects in non-recursive mode returns common
		// prefixes as entries with empty Key but non-empty Prefix;
		// minio-go folds them into ObjectInfo.Key when delim is "/".
		// Detect by Size == 0 + Key ending in delimiter.
		if opt.Delimiter != "" && strings.HasSuffix(info.Key, opt.Delimiter) && info.Size == 0 {
			cps[info.Key] = true
			continue
		}
		res.Objects = append(res.Objects, domain.ObjectMetadata{
			Key:          info.Key,
			Size:         info.Size,
			ETag:         "\"" + strings.Trim(info.ETag, "\"") + "\"",
			LastModified: info.LastModified,
			StorageClass: info.StorageClass,
		})
		if len(res.Objects) >= maxKeys {
			break
		}
	}
	for cp := range cps {
		res.CommonPrefixes = append(res.CommonPrefixes, cp)
	}
	res.KeyCount = len(res.Objects)
	return res, nil
}

func (b *Backend) GetObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	obj, err := b.c.GetObject(ctx, bucket, key, miniogo.GetObjectOptions{})
	if err != nil {
		return domain.Object{}, translateErr(err)
	}
	info, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		return domain.Object{}, translateErr(err)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         info.Size,
		ETag:         "\"" + strings.Trim(info.ETag, "\"") + "\"",
		LastModified: info.LastModified,
		ContentType:  info.ContentType,
		Metadata:     userMetaToMap(info.UserMetadata),
		Body:         obj,
	}, nil
}

func userMetaToMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

func (b *Backend) PutObject(ctx context.Context, opt domain.PutObjectOptions) (domain.PutObjectResult, error) {
	popts := miniogo.PutObjectOptions{
		ContentType:  opt.ContentType,
		UserMetadata: opt.Metadata,
	}
	// minio-go needs an explicit object size or -1 for unknown
	// (multipart-stream upload).
	info, err := b.c.PutObject(ctx, opt.Bucket, opt.Key, opt.Body, -1, popts)
	if err != nil {
		return domain.PutObjectResult{}, translateErr(err)
	}
	return domain.PutObjectResult{ETag: "\"" + strings.Trim(info.ETag, "\"") + "\""}, nil
}

func (b *Backend) DeleteObject(ctx context.Context, bucket, key string) error {
	return translateErr(b.c.RemoveObject(ctx, bucket, key, miniogo.RemoveObjectOptions{}))
}

func (b *Backend) HeadObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	info, err := b.c.StatObject(ctx, bucket, key, miniogo.StatObjectOptions{})
	if err != nil {
		return domain.Object{}, translateErr(err)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         info.Size,
		ETag:         "\"" + strings.Trim(info.ETag, "\"") + "\"",
		LastModified: info.LastModified,
		ContentType:  info.ContentType,
		Metadata:     userMetaToMap(info.UserMetadata),
		// Body intentionally nil.
	}, nil
}

func (b *Backend) CopyObject(ctx context.Context, opt domain.CopyObjectOptions) (domain.CopyObjectResult, error) {
	dst := miniogo.CopyDestOptions{Bucket: opt.DstBucket, Object: opt.DstKey}
	if opt.ContentType != "" {
		dst.UserMetadata = map[string]string{"Content-Type": opt.ContentType}
	}
	if opt.MetadataDirective == "REPLACE" {
		dst.ReplaceMetadata = true
		dst.UserMetadata = opt.Metadata
	}
	src := miniogo.CopySrcOptions{Bucket: opt.SrcBucket, Object: opt.SrcKey}
	info, err := b.c.CopyObject(ctx, dst, src)
	if err != nil {
		return domain.CopyObjectResult{}, translateErr(err)
	}
	return domain.CopyObjectResult{
		ETag:         "\"" + strings.Trim(info.ETag, "\"") + "\"",
		LastModified: info.LastModified,
	}, nil
}

// ----------------------------------------------------------------------
// Multipart
// ----------------------------------------------------------------------

func (b *Backend) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (string, error) {
	id, err := b.core.NewMultipartUpload(ctx, bucket, key, miniogo.PutObjectOptions{
		ContentType:  contentType,
		UserMetadata: metadata,
	})
	return id, translateErr(err)
}

func (b *Backend) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	part, err := b.core.PutObjectPart(ctx, bucket, key, uploadID, int(partNumber),
		body, -1, miniogo.PutObjectPartOptions{})
	if err != nil {
		return "", translateErr(err)
	}
	return "\"" + strings.Trim(part.ETag, "\"") + "\"", nil
}

func (b *Backend) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []domain.CompletePartRef) (string, error) {
	mp := make([]miniogo.CompletePart, len(parts))
	for i, p := range parts {
		mp[i] = miniogo.CompletePart{PartNumber: int(p.Number), ETag: strings.Trim(p.ETag, "\"")}
	}
	info, err := b.core.CompleteMultipartUpload(ctx, bucket, key, uploadID, mp, miniogo.PutObjectOptions{})
	if err != nil {
		return "", translateErr(err)
	}
	return "\"" + strings.Trim(info.ETag, "\"") + "\"", nil
}

func (b *Backend) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	return translateErr(b.core.AbortMultipartUpload(ctx, bucket, key, uploadID))
}

func (b *Backend) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]domain.MultipartUpload, error) {
	r, err := b.core.ListMultipartUploads(ctx, bucket, prefix, "", "", "", 1000)
	if err != nil {
		return nil, translateErr(err)
	}
	out := make([]domain.MultipartUpload, 0, len(r.Uploads))
	for _, u := range r.Uploads {
		out = append(out, domain.MultipartUpload{
			UploadID:  u.UploadID,
			Bucket:    bucket,
			Key:       u.Key,
			Initiated: u.Initiated,
		})
	}
	return out, nil
}

func (b *Backend) ListParts(ctx context.Context, bucket, key, uploadID string) ([]domain.Part, error) {
	r, err := b.core.ListObjectParts(ctx, bucket, key, uploadID, 0, 1000)
	if err != nil {
		return nil, translateErr(err)
	}
	out := make([]domain.Part, 0, len(r.ObjectParts))
	for _, p := range r.ObjectParts {
		out = append(out, domain.Part{
			Number:       int32(p.PartNumber),
			ETag:         "\"" + strings.Trim(p.ETag, "\"") + "\"",
			Size:         p.Size,
			LastModified: p.LastModified,
		})
	}
	return out, nil
}

// SilenceTime is referenced so the import doesn't go unused on
// future package-level helpers.
var _ = time.Time{}

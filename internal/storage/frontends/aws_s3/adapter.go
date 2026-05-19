// Package aws_s3 is the AWS S3 frontend adapter. It implements
// gen.AmazonS3Backend (the union of all 34 per-operation interfaces
// the codegen emits) by translating between AWS-shaped request /
// response types and the neutral domain.Storage interface.
//
// Every translation is field-by-field — no XML re-trip, no double-
// decode. The AWS S3-shape stays on the wire and at the adapter
// boundary; below this point the system is neutral.
//
// The 18 bucket-config probe operations the Terraform AWS provider
// issues during resource Read (GetBucketPolicy, GetBucketTagging,
// etc.) live in probes.go. They never touch the domain — they're
// AWS-specific config concepts; the adapter returns the canonical
// "not configured" 404 or default-state 200 directly. See
// services/storage/OPERATIONS.md.
package aws_s3

import (
	"context"
	"errors"
	"strings"

	"github.com/e6qu/shimanism/internal/restxml"
	"github.com/e6qu/shimanism/internal/storage/domain"
	gen "github.com/e6qu/shimanism/services/storage/gen"
)

// Adapter satisfies gen.AmazonS3Backend by wrapping a domain.Storage.
type Adapter struct {
	s domain.Storage
}

// New returns an Adapter that routes AWS S3-shaped operations to s.
func New(s domain.Storage) *Adapter { return &Adapter{s: s} }

// Compile-time check.
var _ gen.AmazonS3Backend = (*Adapter)(nil)

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// mapError translates a domain.Error into the right S3-vocabulary
// ShimError. Non-domain errors fall through as 500 InternalError.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	var de *domain.Error
	if !errors.As(err, &de) {
		return err
	}
	switch de.Kind {
	case domain.KindNoSuchBucket:
		return restxml.NoSuchBucket(de.Resource)
	case domain.KindNoSuchKey:
		// Resource is "bucket/key"; split for nicer error.
		if i := strings.IndexByte(de.Resource, '/'); i >= 0 {
			return restxml.NoSuchKey(de.Resource[:i], de.Resource[i+1:])
		}
		return restxml.NoSuchKey("", de.Resource)
	case domain.KindNoSuchUpload:
		return restxml.NoSuchUpload(de.Resource)
	case domain.KindBucketAlreadyExists, domain.KindBucketAlreadyOwnedByYou:
		return restxml.BucketAlreadyOwnedByYou(de.Resource)
	case domain.KindBucketNotEmpty:
		return restxml.BucketNotEmpty(de.Resource)
	case domain.KindInvalidArgument:
		return restxml.InvalidArgument(de.Message)
	}
	return err
}

// ----------------------------------------------------------------------
// Core 16: bucket lifecycle
// ----------------------------------------------------------------------

func (a *Adapter) ListBuckets(ctx context.Context, in *gen.ListBucketsRequest) (*gen.ListBucketsOutput, error) {
	res, err := a.s.ListBuckets(ctx, domain.ListBucketsOptions{
		Prefix:     deref(in.Prefix, ""),
		Region:     deref(in.BucketRegion, ""),
		MaxResults: int(deref(in.MaxBuckets, 0)),
		NextToken:  deref(in.ContinuationToken, ""),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.ListBucketsOutput{
		Owner: &gen.Owner{ID: ptr(res.Owner.ID), DisplayName: ptr(res.Owner.DisplayName)},
	}
	if res.NextToken != "" {
		out.ContinuationToken = ptr(res.NextToken)
	}
	if res.Prefix != "" {
		out.Prefix = ptr(res.Prefix)
	}
	for _, b := range res.Buckets {
		b := b
		out.Buckets.Items = append(out.Buckets.Items, gen.Bucket{
			Name:         ptr(b.Name),
			CreationDate: ptr(b.CreatedAt),
			BucketRegion: ptr(b.Region),
		})
	}
	return out, nil
}

func (a *Adapter) CreateBucket(ctx context.Context, in *gen.CreateBucketRequest) (*gen.CreateBucketOutput, error) {
	region := ""
	if in.CreateBucketConfiguration != nil && in.CreateBucketConfiguration.LocationConstraint != nil {
		region = string(*in.CreateBucketConfiguration.LocationConstraint)
	}
	if err := a.s.CreateBucket(ctx, in.Bucket, region); err != nil {
		return nil, mapError(err)
	}
	return &gen.CreateBucketOutput{Location: ptr("/" + in.Bucket)}, nil
}

func (a *Adapter) DeleteBucket(ctx context.Context, in *gen.DeleteBucketRequest) (struct{}, error) {
	if err := a.s.DeleteBucket(ctx, in.Bucket); err != nil {
		return struct{}{}, mapError(err)
	}
	return struct{}{}, nil
}

func (a *Adapter) HeadBucket(ctx context.Context, in *gen.HeadBucketRequest) (*gen.HeadBucketOutput, error) {
	b, err := a.s.HeadBucket(ctx, in.Bucket)
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.HeadBucketOutput{BucketRegion: ptr(b.Region)}, nil
}

// ----------------------------------------------------------------------
// Core 16: object lifecycle
// ----------------------------------------------------------------------

func (a *Adapter) ListObjectsV2(ctx context.Context, in *gen.ListObjectsV2Request) (*gen.ListObjectsV2Output, error) {
	res, err := a.s.ListObjects(ctx, domain.ListObjectsOptions{
		Bucket:     in.Bucket,
		Prefix:     deref(in.Prefix, ""),
		Delimiter:  deref(in.Delimiter, ""),
		StartAfter: deref(in.StartAfter, ""),
		NextToken:  deref(in.ContinuationToken, ""),
		MaxResults: int(deref(in.MaxKeys, 1000)),
	})
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.ListObjectsV2Output{
		Name:              ptr(res.Bucket),
		Prefix:            ptr(res.Prefix),
		Delimiter:         ptr(res.Delimiter),
		MaxKeys:           ptr(int32(deref(in.MaxKeys, 1000))),
		KeyCount:          ptr(int32(res.KeyCount)),
		IsTruncated:       ptr(res.IsTruncated),
		ContinuationToken: in.ContinuationToken,
		StartAfter:        in.StartAfter,
	}
	if res.NextToken != "" {
		out.NextContinuationToken = ptr(res.NextToken)
	}
	for _, o := range res.Objects {
		o := o
		out.Contents = append(out.Contents, gen.Object{
			Key:          ptr(o.Key),
			LastModified: ptr(o.LastModified),
			ETag:         ptr(o.ETag),
			Size:         ptr(o.Size),
		})
	}
	for _, cp := range res.CommonPrefixes {
		cp := cp
		out.CommonPrefixes = append(out.CommonPrefixes, gen.CommonPrefix{Prefix: &cp})
	}
	return out, nil
}

func (a *Adapter) GetObject(ctx context.Context, in *gen.GetObjectRequest) (*gen.GetObjectOutput, error) {
	obj, err := a.s.GetObject(ctx, in.Bucket, in.Key)
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.GetObjectOutput{
		Body:          obj.Body,
		ContentLength: ptr(obj.Size),
		ETag:          ptr(obj.ETag),
		LastModified:  ptr(obj.LastModified),
	}
	if obj.ContentType != "" {
		out.ContentType = ptr(obj.ContentType)
	}
	if obj.Metadata != nil {
		out.Metadata = gen.Metadata(obj.Metadata)
	}
	return out, nil
}

func (a *Adapter) PutObject(ctx context.Context, in *gen.PutObjectRequest) (*gen.PutObjectOutput, error) {
	res, err := a.s.PutObject(ctx, domain.PutObjectOptions{
		Bucket:      in.Bucket,
		Key:         in.Key,
		Body:        in.Body, // io.ReadCloser satisfies io.Reader
		ContentType: deref(in.ContentType, ""),
		Metadata:    map[string]string(in.Metadata),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.PutObjectOutput{ETag: ptr(res.ETag)}, nil
}

func (a *Adapter) DeleteObject(ctx context.Context, in *gen.DeleteObjectRequest) (*gen.DeleteObjectOutput, error) {
	if err := a.s.DeleteObject(ctx, in.Bucket, in.Key); err != nil {
		return nil, mapError(err)
	}
	return &gen.DeleteObjectOutput{}, nil
}

func (a *Adapter) HeadObject(ctx context.Context, in *gen.HeadObjectRequest) (*gen.HeadObjectOutput, error) {
	obj, err := a.s.HeadObject(ctx, in.Bucket, in.Key)
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.HeadObjectOutput{
		ContentLength: ptr(obj.Size),
		ETag:          ptr(obj.ETag),
		LastModified:  ptr(obj.LastModified),
	}
	if obj.ContentType != "" {
		out.ContentType = ptr(obj.ContentType)
	}
	if obj.Metadata != nil {
		out.Metadata = gen.Metadata(obj.Metadata)
	}
	return out, nil
}

func (a *Adapter) CopyObject(ctx context.Context, in *gen.CopyObjectRequest) (*gen.CopyObjectOutput, error) {
	srcBucket, srcKey, err := parseCopySource(in.CopySource)
	if err != nil {
		return nil, mapError(err)
	}
	res, err := a.s.CopyObject(ctx, domain.CopyObjectOptions{
		SrcBucket:   srcBucket,
		SrcKey:      srcKey,
		DstBucket:   in.Bucket,
		DstKey:      in.Key,
		ContentType: deref(in.ContentType, ""),
		Metadata:    map[string]string(in.Metadata),
	})
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.CopyObjectOutput{
		CopyObjectResult: &gen.CopyObjectResult{
			ETag:         ptr(res.ETag),
			LastModified: ptr(res.LastModified),
		},
	}, nil
}

func parseCopySource(s string) (string, string, error) {
	s = strings.TrimPrefix(s, "/")
	if i := strings.IndexByte(s, '?'); i >= 0 {
		s = s[:i] // strip versionId etc.
	}
	idx := strings.IndexByte(s, '/')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", domain.InvalidArgument("CopySource does not parse: " + s)
	}
	return s[:idx], s[idx+1:], nil
}

// ----------------------------------------------------------------------
// Multipart
// ----------------------------------------------------------------------

func (a *Adapter) CreateMultipartUpload(ctx context.Context, in *gen.CreateMultipartUploadRequest) (*gen.CreateMultipartUploadOutput, error) {
	id, err := a.s.CreateMultipartUpload(ctx, in.Bucket, in.Key,
		deref(in.ContentType, ""), map[string]string(in.Metadata))
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.CreateMultipartUploadOutput{
		Bucket:   ptr(in.Bucket),
		Key:      ptr(in.Key),
		UploadId: ptr(id),
	}, nil
}

func (a *Adapter) UploadPart(ctx context.Context, in *gen.UploadPartRequest) (*gen.UploadPartOutput, error) {
	etag, err := a.s.UploadPart(ctx, in.Bucket, in.Key, in.UploadId, in.PartNumber, in.Body)
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.UploadPartOutput{ETag: ptr(etag)}, nil
}

func (a *Adapter) CompleteMultipartUpload(ctx context.Context, in *gen.CompleteMultipartUploadRequest) (*gen.CompleteMultipartUploadOutput, error) {
	var parts []domain.CompletePartRef
	if in.MultipartUpload != nil {
		for _, p := range in.MultipartUpload.Parts {
			parts = append(parts, domain.CompletePartRef{
				Number: deref(p.PartNumber, 0),
				ETag:   deref(p.ETag, ""),
			})
		}
	}
	etag, err := a.s.CompleteMultipartUpload(ctx, in.Bucket, in.Key, in.UploadId, parts)
	if err != nil {
		return nil, mapError(err)
	}
	return &gen.CompleteMultipartUploadOutput{
		Bucket: ptr(in.Bucket),
		Key:    ptr(in.Key),
		ETag:   ptr(etag),
	}, nil
}

func (a *Adapter) AbortMultipartUpload(ctx context.Context, in *gen.AbortMultipartUploadRequest) (*gen.AbortMultipartUploadOutput, error) {
	if err := a.s.AbortMultipartUpload(ctx, in.Bucket, in.Key, in.UploadId); err != nil {
		return nil, mapError(err)
	}
	return &gen.AbortMultipartUploadOutput{}, nil
}

func (a *Adapter) ListMultipartUploads(ctx context.Context, in *gen.ListMultipartUploadsRequest) (*gen.ListMultipartUploadsOutput, error) {
	ups, err := a.s.ListMultipartUploads(ctx, in.Bucket, deref(in.Prefix, ""))
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.ListMultipartUploadsOutput{Bucket: ptr(in.Bucket), Prefix: in.Prefix}
	for _, u := range ups {
		u := u
		out.Uploads = append(out.Uploads, gen.MultipartUpload{
			UploadId:  ptr(u.UploadID),
			Key:       ptr(u.Key),
			Initiated: ptr(u.Initiated),
		})
	}
	return out, nil
}

func (a *Adapter) ListParts(ctx context.Context, in *gen.ListPartsRequest) (*gen.ListPartsOutput, error) {
	parts, err := a.s.ListParts(ctx, in.Bucket, in.Key, in.UploadId)
	if err != nil {
		return nil, mapError(err)
	}
	out := &gen.ListPartsOutput{
		Bucket:   ptr(in.Bucket),
		Key:      ptr(in.Key),
		UploadId: ptr(in.UploadId),
	}
	for _, p := range parts {
		p := p
		out.Parts = append(out.Parts, gen.Part{
			PartNumber:   ptr(p.Number),
			ETag:         ptr(p.ETag),
			LastModified: ptr(p.LastModified),
			Size:         ptr(p.Size),
		})
	}
	return out, nil
}

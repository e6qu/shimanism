// Package aws is the AWS S3 passthrough backend. It uses
// aws-sdk-go-v2/service/s3 (already a dependency, Apache 2.0) to
// drive real AWS S3. The "passthrough" name reflects that the front
// door is AWS S3 and the back door is also AWS S3 — useful for auth
// interception, observability injection, cross-region routing, or
// for using the same shim binary to talk to real S3 alongside
// shim-internal backends.
package aws

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awss3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Backend implements domain.Storage by calling real AWS S3.
type Backend struct {
	c *awss3.Client
}

// New constructs a Backend from a configured s3.Client. The caller
// is responsible for credential / region / endpoint configuration;
// passing the bare client keeps the backend agnostic to SDK config
// idioms (which evolve across SDK versions).
func New(client *awss3.Client) *Backend { return &Backend{c: client} }

// Compile-time check.
var _ domain.Storage = (*Backend)(nil)

// translateErr maps AWS SDK errors to domain errors using the error
// codes the AWS SDK exposes via smithy-go's APIError interface.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "NoSuchBucket":
			return domain.NoSuchBucket("")
		case "NoSuchKey", "NotFound":
			return domain.NoSuchKey("", "")
		case "NoSuchUpload":
			return domain.NoSuchUpload("")
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return domain.BucketAlreadyExists("")
		case "BucketNotEmpty":
			return domain.BucketNotEmpty("")
		case "InvalidArgument":
			return domain.InvalidArgument(ae.ErrorMessage())
		}
	}
	return err
}

// ----------------------------------------------------------------------
// Bucket lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListBuckets(ctx context.Context, opt domain.ListBucketsOptions) (domain.ListBucketsResult, error) {
	in := &awss3.ListBucketsInput{}
	if opt.Prefix != "" {
		in.Prefix = aws.String(opt.Prefix)
	}
	if opt.Region != "" {
		in.BucketRegion = aws.String(opt.Region)
	}
	if opt.MaxResults > 0 {
		mr := int32(opt.MaxResults)
		in.MaxBuckets = &mr
	}
	if opt.NextToken != "" {
		in.ContinuationToken = aws.String(opt.NextToken)
	}
	out, err := b.c.ListBuckets(ctx, in)
	if err != nil {
		return domain.ListBucketsResult{}, translateErr(err)
	}
	res := domain.ListBucketsResult{Prefix: opt.Prefix}
	if out.ContinuationToken != nil {
		res.NextToken = *out.ContinuationToken
	}
	if out.Owner != nil {
		res.Owner = domain.Owner{
			ID:          aws.ToString(out.Owner.ID),
			DisplayName: aws.ToString(out.Owner.DisplayName),
		}
	}
	for _, bk := range out.Buckets {
		res.Buckets = append(res.Buckets, domain.Bucket{
			Name:      aws.ToString(bk.Name),
			CreatedAt: aws.ToTime(bk.CreationDate),
			Region:    aws.ToString(bk.BucketRegion),
		})
	}
	return res, nil
}

func (b *Backend) CreateBucket(ctx context.Context, name, region string) error {
	in := &awss3.CreateBucketInput{Bucket: aws.String(name)}
	if region != "" && region != "us-east-1" {
		in.CreateBucketConfiguration = &awss3types.CreateBucketConfiguration{
			LocationConstraint: awss3types.BucketLocationConstraint(region),
		}
	}
	_, err := b.c.CreateBucket(ctx, in)
	return translateErr(err)
}

func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	_, err := b.c.DeleteBucket(ctx, &awss3.DeleteBucketInput{Bucket: aws.String(name)})
	return translateErr(err)
}

func (b *Backend) HeadBucket(ctx context.Context, name string) (domain.Bucket, error) {
	out, err := b.c.HeadBucket(ctx, &awss3.HeadBucketInput{Bucket: aws.String(name)})
	if err != nil {
		return domain.Bucket{}, translateErr(err)
	}
	return domain.Bucket{Name: name, Region: aws.ToString(out.BucketRegion)}, nil
}

// ----------------------------------------------------------------------
// Object lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListObjects(ctx context.Context, opt domain.ListObjectsOptions) (domain.ListObjectsResult, error) {
	in := &awss3.ListObjectsV2Input{Bucket: aws.String(opt.Bucket)}
	if opt.Prefix != "" {
		in.Prefix = aws.String(opt.Prefix)
	}
	if opt.Delimiter != "" {
		in.Delimiter = aws.String(opt.Delimiter)
	}
	if opt.StartAfter != "" {
		in.StartAfter = aws.String(opt.StartAfter)
	}
	if opt.NextToken != "" {
		in.ContinuationToken = aws.String(opt.NextToken)
	}
	if opt.MaxResults > 0 {
		mr := int32(opt.MaxResults)
		in.MaxKeys = &mr
	}
	out, err := b.c.ListObjectsV2(ctx, in)
	if err != nil {
		return domain.ListObjectsResult{}, translateErr(err)
	}
	res := domain.ListObjectsResult{
		Bucket:      opt.Bucket,
		Prefix:      opt.Prefix,
		Delimiter:   opt.Delimiter,
		IsTruncated: aws.ToBool(out.IsTruncated),
		KeyCount:    int(aws.ToInt32(out.KeyCount)),
	}
	if out.NextContinuationToken != nil {
		res.NextToken = *out.NextContinuationToken
	}
	for _, o := range out.Contents {
		res.Objects = append(res.Objects, domain.ObjectMetadata{
			Key:          aws.ToString(o.Key),
			Size:         aws.ToInt64(o.Size),
			ETag:         aws.ToString(o.ETag),
			LastModified: aws.ToTime(o.LastModified),
			StorageClass: string(o.StorageClass),
		})
	}
	for _, cp := range out.CommonPrefixes {
		if cp.Prefix != nil {
			res.CommonPrefixes = append(res.CommonPrefixes, *cp.Prefix)
		}
	}
	return res, nil
}

func (b *Backend) GetObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	out, err := b.c.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		return domain.Object{}, translateErr(err)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		ContentType:  aws.ToString(out.ContentType),
		Metadata:     out.Metadata,
		Body:         out.Body,
	}, nil
}

func (b *Backend) PutObject(ctx context.Context, opt domain.PutObjectOptions) (domain.PutObjectResult, error) {
	in := &awss3.PutObjectInput{
		Bucket:   aws.String(opt.Bucket),
		Key:      aws.String(opt.Key),
		Body:     opt.Body,
		Metadata: opt.Metadata,
	}
	if opt.ContentType != "" {
		in.ContentType = aws.String(opt.ContentType)
	}
	out, err := b.c.PutObject(ctx, in)
	if err != nil {
		return domain.PutObjectResult{}, translateErr(err)
	}
	return domain.PutObjectResult{ETag: aws.ToString(out.ETag)}, nil
}

func (b *Backend) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := b.c.DeleteObject(ctx, &awss3.DeleteObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	return translateErr(err)
}

func (b *Backend) HeadObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	out, err := b.c.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	if err != nil {
		return domain.Object{}, translateErr(err)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         aws.ToInt64(out.ContentLength),
		ETag:         aws.ToString(out.ETag),
		LastModified: aws.ToTime(out.LastModified),
		ContentType:  aws.ToString(out.ContentType),
		Metadata:     out.Metadata,
	}, nil
}

func (b *Backend) CopyObject(ctx context.Context, opt domain.CopyObjectOptions) (domain.CopyObjectResult, error) {
	in := &awss3.CopyObjectInput{
		Bucket:     aws.String(opt.DstBucket),
		Key:        aws.String(opt.DstKey),
		CopySource: aws.String(opt.SrcBucket + "/" + opt.SrcKey),
	}
	if opt.ContentType != "" {
		in.ContentType = aws.String(opt.ContentType)
	}
	if opt.MetadataDirective == "REPLACE" {
		in.MetadataDirective = awss3types.MetadataDirectiveReplace
		in.Metadata = opt.Metadata
	}
	out, err := b.c.CopyObject(ctx, in)
	if err != nil {
		return domain.CopyObjectResult{}, translateErr(err)
	}
	res := domain.CopyObjectResult{}
	if out.CopyObjectResult != nil {
		res.ETag = aws.ToString(out.CopyObjectResult.ETag)
		if out.CopyObjectResult.LastModified != nil {
			res.LastModified = *out.CopyObjectResult.LastModified
		}
	}
	return res, nil
}

// ----------------------------------------------------------------------
// Multipart
// ----------------------------------------------------------------------

func (b *Backend) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (string, error) {
	in := &awss3.CreateMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		Metadata: metadata,
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	out, err := b.c.CreateMultipartUpload(ctx, in)
	if err != nil {
		return "", translateErr(err)
	}
	return aws.ToString(out.UploadId), nil
}

func (b *Backend) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	out, err := b.c.UploadPart(ctx, &awss3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(uploadID),
		PartNumber: &partNumber,
		Body:       body,
	})
	if err != nil {
		return "", translateErr(err)
	}
	return aws.ToString(out.ETag), nil
}

func (b *Backend) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []domain.CompletePartRef) (string, error) {
	cp := make([]awss3types.CompletedPart, len(parts))
	for i, p := range parts {
		num := p.Number
		cp[i] = awss3types.CompletedPart{PartNumber: &num, ETag: aws.String(p.ETag)}
	}
	out, err := b.c.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &awss3types.CompletedMultipartUpload{Parts: cp},
	})
	if err != nil {
		return "", translateErr(err)
	}
	return aws.ToString(out.ETag), nil
}

func (b *Backend) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	_, err := b.c.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	return translateErr(err)
}

func (b *Backend) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]domain.MultipartUpload, error) {
	in := &awss3.ListMultipartUploadsInput{Bucket: aws.String(bucket)}
	if prefix != "" {
		in.Prefix = aws.String(prefix)
	}
	out, err := b.c.ListMultipartUploads(ctx, in)
	if err != nil {
		return nil, translateErr(err)
	}
	res := make([]domain.MultipartUpload, 0, len(out.Uploads))
	for _, u := range out.Uploads {
		res = append(res, domain.MultipartUpload{
			UploadID:  aws.ToString(u.UploadId),
			Bucket:    bucket,
			Key:       aws.ToString(u.Key),
			Initiated: aws.ToTime(u.Initiated),
		})
	}
	return res, nil
}

func (b *Backend) ListParts(ctx context.Context, bucket, key, uploadID string) ([]domain.Part, error) {
	out, err := b.c.ListParts(ctx, &awss3.ListPartsInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(uploadID),
	})
	if err != nil {
		return nil, translateErr(err)
	}
	res := make([]domain.Part, 0, len(out.Parts))
	for _, p := range out.Parts {
		var lm time.Time
		if p.LastModified != nil {
			lm = *p.LastModified
		}
		res = append(res, domain.Part{
			Number:       aws.ToInt32(p.PartNumber),
			ETag:         aws.ToString(p.ETag),
			Size:         aws.ToInt64(p.Size),
			LastModified: lm,
		})
	}
	return res, nil
}

// silence unused import warnings until the strings helper is used
// for richer error mapping (currently translateErr ignores resource
// names since the AWS SDK error type doesn't carry them).
var _ = strings.HasPrefix

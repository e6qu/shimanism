// Package gcs is the Google Cloud Storage backend for shimanism's
// neutral storage domain. It uses cloud.google.com/go/storage
// (Apache 2.0).
//
// This is the first *opposite-shape* backend: the frontend is the
// AWS S3 wire protocol; the backend is GCS. The shim's job is to
// translate one to the other faithfully via the domain interface.
//
// Multipart strategy: GCS has no native S3-style multipart with
// explicit part numbers and an upload ID. We map S3 multipart to
// GCS as follows:
//
//   - CreateMultipartUpload: generate a random uploadID; record it
//     by writing a marker object at <key>.uploads/<uploadID>/.init.
//   - UploadPart: write each part as a discrete GCS object at
//     <key>.uploads/<uploadID>/part-<N>.
//   - CompleteMultipartUpload: GCS Compose can join up to 32
//     objects in one call; for N>32, recursive composes are
//     necessary. The composed object replaces the final key. Part
//     objects are then deleted.
//   - AbortMultipartUpload: delete the marker + every part object.
//   - ListMultipartUploads / ListParts: list objects under the
//     .uploads/ prefix to reconstruct the session state.
//
// This stores the upload state in GCS itself (no separate database),
// which makes the backend horizontally stateless.
package gcs

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Config holds backend connection parameters.
type Config struct {
	// ProjectID is required for CreateBucket / ListBuckets.
	ProjectID string
	// DefaultRegion is reported when GCS doesn't tell us a bucket's
	// location (rare).
	DefaultRegion string
}

// Backend implements domain.Storage by calling GCS.
type Backend struct {
	c             *gcsstorage.Client
	projectID     string
	defaultRegion string
}

// New wraps an already-created GCS client.
func New(c *gcsstorage.Client, cfg Config) *Backend {
	r := cfg.DefaultRegion
	if r == "" {
		r = "us"
	}
	return &Backend{c: c, projectID: cfg.ProjectID, defaultRegion: r}
}

// Compile-time check.
var _ domain.Storage = (*Backend)(nil)

// translateErr maps GCS errors to domain errors. GCS uses
// googleapi.Error for HTTP errors; gcsstorage exposes sentinel
// errors for common cases.
func translateErr(err error, bucket, key string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gcsstorage.ErrBucketNotExist) {
		return domain.NoSuchBucket(bucket)
	}
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return domain.NoSuchKey(bucket, key)
	}
	var ge *googleapi.Error
	if errors.As(err, &ge) {
		switch ge.Code {
		case 404:
			if key != "" {
				return domain.NoSuchKey(bucket, key)
			}
			return domain.NoSuchBucket(bucket)
		case 409:
			return domain.BucketAlreadyExists(bucket)
		case 400:
			return domain.InvalidArgument(ge.Message)
		}
	}
	return err
}

// ----------------------------------------------------------------------
// Bucket lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListBuckets(ctx context.Context, opt domain.ListBucketsOptions) (domain.ListBucketsResult, error) {
	it := b.c.Buckets(ctx, b.projectID)
	if opt.Prefix != "" {
		it.Prefix = opt.Prefix
	}
	res := domain.ListBucketsResult{Prefix: opt.Prefix}
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return domain.ListBucketsResult{}, translateErr(err, "", "")
		}
		if opt.Region != "" && !strings.EqualFold(attrs.Location, opt.Region) {
			continue
		}
		res.Buckets = append(res.Buckets, domain.Bucket{
			Name:      attrs.Name,
			CreatedAt: attrs.Created,
			Region:    strings.ToLower(attrs.Location),
		})
		if opt.MaxResults > 0 && len(res.Buckets) >= opt.MaxResults {
			break
		}
	}
	return res, nil
}

func (b *Backend) CreateBucket(ctx context.Context, name, region string) error {
	if region == "" {
		region = b.defaultRegion
	}
	attrs := &gcsstorage.BucketAttrs{Location: strings.ToUpper(region)}
	err := b.c.Bucket(name).Create(ctx, b.projectID, attrs)
	if err == nil {
		return nil
	}
	// Idempotent recreate matches MinIO / inmem behaviour.
	var ge *googleapi.Error
	if errors.As(err, &ge) && ge.Code == 409 {
		return nil
	}
	return translateErr(err, name, "")
}

func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	return translateErr(b.c.Bucket(name).Delete(ctx), name, "")
}

func (b *Backend) HeadBucket(ctx context.Context, name string) (domain.Bucket, error) {
	attrs, err := b.c.Bucket(name).Attrs(ctx)
	if err != nil {
		return domain.Bucket{}, translateErr(err, name, "")
	}
	return domain.Bucket{
		Name:      attrs.Name,
		CreatedAt: attrs.Created,
		Region:    strings.ToLower(attrs.Location),
	}, nil
}

// ----------------------------------------------------------------------
// Object lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListObjects(ctx context.Context, opt domain.ListObjectsOptions) (domain.ListObjectsResult, error) {
	q := &gcsstorage.Query{
		Prefix:    opt.Prefix,
		Delimiter: opt.Delimiter,
	}
	if opt.StartAfter != "" {
		q.StartOffset = opt.StartAfter
	}
	if opt.NextToken != "" {
		q.StartOffset = opt.NextToken
	}
	it := b.c.Bucket(opt.Bucket).Objects(ctx, q)
	maxKeys := opt.MaxResults
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	res := domain.ListObjectsResult{
		Bucket:    opt.Bucket,
		Prefix:    opt.Prefix,
		Delimiter: opt.Delimiter,
	}
	cps := map[string]bool{}
	for len(res.Objects) < maxKeys {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return domain.ListObjectsResult{}, translateErr(err, opt.Bucket, "")
		}
		if attrs.Prefix != "" {
			cps[attrs.Prefix] = true
			continue
		}
		// Skip multipart-upload temp objects from the visible listing.
		if strings.Contains(attrs.Name, ".uploads/") {
			continue
		}
		res.Objects = append(res.Objects, domain.ObjectMetadata{
			Key:          attrs.Name,
			Size:         attrs.Size,
			ETag:         "\"" + attrs.Etag + "\"",
			LastModified: attrs.Updated,
			StorageClass: attrs.StorageClass,
		})
	}
	for cp := range cps {
		res.CommonPrefixes = append(res.CommonPrefixes, cp)
	}
	res.KeyCount = len(res.Objects)
	if len(res.Objects) >= maxKeys {
		res.IsTruncated = true
		if len(res.Objects) > 0 {
			res.NextToken = res.Objects[len(res.Objects)-1].Key
		}
	}
	return res, nil
}

func (b *Backend) GetObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	oh := b.c.Bucket(bucket).Object(key)
	attrs, err := oh.Attrs(ctx)
	if err != nil {
		return domain.Object{}, translateErr(err, bucket, key)
	}
	r, err := oh.NewReader(ctx)
	if err != nil {
		return domain.Object{}, translateErr(err, bucket, key)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         attrs.Size,
		ETag:         "\"" + attrs.Etag + "\"",
		LastModified: attrs.Updated,
		ContentType:  attrs.ContentType,
		Metadata:     attrs.Metadata,
		Body:         r,
	}, nil
}

func (b *Backend) PutObject(ctx context.Context, opt domain.PutObjectOptions) (domain.PutObjectResult, error) {
	w := b.c.Bucket(opt.Bucket).Object(opt.Key).NewWriter(ctx)
	if opt.ContentType != "" {
		w.ContentType = opt.ContentType
	}
	if len(opt.Metadata) > 0 {
		w.Metadata = opt.Metadata
	}
	if _, err := io.Copy(w, opt.Body); err != nil {
		_ = w.Close()
		return domain.PutObjectResult{}, translateErr(err, opt.Bucket, opt.Key)
	}
	if err := w.Close(); err != nil {
		return domain.PutObjectResult{}, translateErr(err, opt.Bucket, opt.Key)
	}
	return domain.PutObjectResult{ETag: "\"" + w.Attrs().Etag + "\""}, nil
}

func (b *Backend) DeleteObject(ctx context.Context, bucket, key string) error {
	err := b.c.Bucket(bucket).Object(key).Delete(ctx)
	if errors.Is(err, gcsstorage.ErrObjectNotExist) {
		return nil // S3 DeleteObject is idempotent
	}
	return translateErr(err, bucket, key)
}

func (b *Backend) HeadObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	attrs, err := b.c.Bucket(bucket).Object(key).Attrs(ctx)
	if err != nil {
		return domain.Object{}, translateErr(err, bucket, key)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         attrs.Size,
		ETag:         "\"" + attrs.Etag + "\"",
		LastModified: attrs.Updated,
		ContentType:  attrs.ContentType,
		Metadata:     attrs.Metadata,
	}, nil
}

func (b *Backend) CopyObject(ctx context.Context, opt domain.CopyObjectOptions) (domain.CopyObjectResult, error) {
	src := b.c.Bucket(opt.SrcBucket).Object(opt.SrcKey)
	dst := b.c.Bucket(opt.DstBucket).Object(opt.DstKey)
	copier := dst.CopierFrom(src)
	if opt.ContentType != "" {
		copier.ContentType = opt.ContentType
	}
	if opt.MetadataDirective == "REPLACE" {
		copier.Metadata = opt.Metadata
	}
	attrs, err := copier.Run(ctx)
	if err != nil {
		return domain.CopyObjectResult{}, translateErr(err, opt.DstBucket, opt.DstKey)
	}
	return domain.CopyObjectResult{
		ETag:         "\"" + attrs.Etag + "\"",
		LastModified: attrs.Updated,
	}, nil
}

// ----------------------------------------------------------------------
// Multipart (mapped to GCS compose-of-temp-objects)
// ----------------------------------------------------------------------

func uploadPrefix(key, uploadID string) string {
	return key + ".uploads/" + uploadID + "/"
}

func partName(key, uploadID string, partNumber int32) string {
	return fmt.Sprintf("%spart-%05d", uploadPrefix(key, uploadID), partNumber)
}

func (b *Backend) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (string, error) {
	src := fmt.Sprintf("%s|%s|%d", bucket, key, time.Now().UnixNano())
	sum := md5.Sum([]byte(src))
	id := hex.EncodeToString(sum[:])

	// Write a marker object so ListMultipartUploads can discover the
	// session. The marker stores the user-supplied contentType +
	// metadata so Complete can apply them to the final object.
	w := b.c.Bucket(bucket).Object(uploadPrefix(key, id) + ".init").NewWriter(ctx)
	if contentType != "" {
		w.Metadata = map[string]string{"shim-content-type": contentType}
	}
	for k, v := range metadata {
		if w.Metadata == nil {
			w.Metadata = map[string]string{}
		}
		w.Metadata["shim-user-"+k] = v
	}
	if _, err := w.Write([]byte{}); err != nil {
		_ = w.Close()
		return "", translateErr(err, bucket, key)
	}
	if err := w.Close(); err != nil {
		return "", translateErr(err, bucket, key)
	}
	return id, nil
}

func (b *Backend) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	if partNumber < 1 || partNumber > 10000 {
		return "", domain.InvalidArgument("PartNumber out of range")
	}
	w := b.c.Bucket(bucket).Object(partName(key, uploadID, partNumber)).NewWriter(ctx)
	if _, err := io.Copy(w, body); err != nil {
		_ = w.Close()
		return "", translateErr(err, bucket, key)
	}
	if err := w.Close(); err != nil {
		return "", translateErr(err, bucket, key)
	}
	return "\"" + w.Attrs().Etag + "\"", nil
}

func (b *Backend) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []domain.CompletePartRef) (string, error) {
	// Build the part-object list from the caller's explicit PartNumber
	// sequence rather than from a Bucket.Objects listing — that way
	// the assembly order is the order the caller declared, independent
	// of any storage-backend listing semantics.
	prefix := uploadPrefix(key, uploadID)
	markerName := prefix + ".init"
	markerObj := b.c.Bucket(bucket).Object(markerName)
	markerAttrs, err := markerObj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, gcsstorage.ErrObjectNotExist) {
			return "", domain.NoSuchUpload(uploadID)
		}
		return "", translateErr(err, bucket, key)
	}
	contentType := markerAttrs.Metadata["shim-content-type"]
	var userMeta map[string]string
	for k, v := range markerAttrs.Metadata {
		if strings.HasPrefix(k, "shim-user-") {
			if userMeta == nil {
				userMeta = map[string]string{}
			}
			userMeta[strings.TrimPrefix(k, "shim-user-")] = v
		}
	}
	if len(parts) == 0 {
		return "", domain.NoSuchUpload(uploadID)
	}
	partObjs := make([]*gcsstorage.ObjectHandle, 0, len(parts))
	for _, p := range parts {
		partObjs = append(partObjs, b.c.Bucket(bucket).Object(partName(key, uploadID, p.Number)))
	}

	// Compose into the final key. GCS Compose handles up to 32
	// sources at a time; chain via temp intermediates if more.
	finalObj := b.c.Bucket(bucket).Object(key)
	composed := partObjs
	for len(composed) > 1 {
		batch := composed
		var next []*gcsstorage.ObjectHandle
		for len(batch) > 0 {
			n := len(batch)
			if n > 32 {
				n = 32
			}
			group := batch[:n]
			batch = batch[n:]
			var target *gcsstorage.ObjectHandle
			if len(batch) == 0 && len(next) == 0 {
				target = finalObj
			} else {
				target = b.c.Bucket(bucket).Object(prefix + fmt.Sprintf(".compose-%d", len(next)))
			}
			c := target.ComposerFrom(group...)
			if target == finalObj {
				c.ContentType = contentType
				if userMeta != nil {
					c.Metadata = userMeta
				}
			}
			if _, err := c.Run(ctx); err != nil {
				return "", translateErr(err, bucket, key)
			}
			next = append(next, target)
		}
		composed = next
	}

	// If we had exactly one part, ComposerFrom with a single source
	// is OK; if we never composed, copy the lone part to the final.
	if len(composed) == 1 && composed[0] != finalObj {
		_, err := finalObj.CopierFrom(composed[0]).Run(ctx)
		if err != nil {
			return "", translateErr(err, bucket, key)
		}
	}

	// Clean up parts + marker.
	for _, p := range partObjs {
		_ = p.Delete(ctx)
	}
	if markerObj != nil {
		_ = markerObj.Delete(ctx)
	}

	// Return the S3 multipart ETag computed from the part ETags, not
	// the GCS-native composed-object Etag (which is CRC32C-derived
	// and has no defined cross-backend shape). Verifying the result
	// object exists is still useful as a fault check; we discard the
	// attrs.
	if _, err := finalObj.Attrs(ctx); err != nil {
		return "", translateErr(err, bucket, key)
	}
	return domain.MultipartETag(parts), nil
}

func (b *Backend) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	prefix := uploadPrefix(key, uploadID)
	it := b.c.Bucket(bucket).Objects(ctx, &gcsstorage.Query{Prefix: prefix})
	found := false
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return translateErr(err, bucket, key)
		}
		found = true
		_ = b.c.Bucket(bucket).Object(attrs.Name).Delete(ctx)
	}
	if !found {
		return domain.NoSuchUpload(uploadID)
	}
	return nil
}

func (b *Backend) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]domain.MultipartUpload, error) {
	// We discover sessions by scanning for marker objects
	// (key.uploads/<id>/.init). The user-supplied prefix filters by
	// the original key prefix.
	it := b.c.Bucket(bucket).Objects(ctx, &gcsstorage.Query{Prefix: prefix})
	seen := map[string]time.Time{}
	uploadKey := map[string]string{}
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, translateErr(err, bucket, "")
		}
		// Detect marker: name ends with .uploads/<id>/.init
		if !strings.HasSuffix(attrs.Name, "/.init") {
			continue
		}
		// Recover originalKey + uploadID by reversing uploadPrefix.
		nameNoInit := strings.TrimSuffix(attrs.Name, "/.init")
		i := strings.LastIndex(nameNoInit, ".uploads/")
		if i < 0 {
			continue
		}
		origKey := nameNoInit[:i]
		uploadID := nameNoInit[i+len(".uploads/"):]
		seen[uploadID] = attrs.Created
		uploadKey[uploadID] = origKey
	}
	out := make([]domain.MultipartUpload, 0, len(seen))
	for id, ts := range seen {
		out = append(out, domain.MultipartUpload{
			UploadID:  id,
			Bucket:    bucket,
			Key:       uploadKey[id],
			Initiated: ts,
		})
	}
	return out, nil
}

func (b *Backend) ListParts(ctx context.Context, bucket, key, uploadID string) ([]domain.Part, error) {
	prefix := uploadPrefix(key, uploadID)
	it := b.c.Bucket(bucket).Objects(ctx, &gcsstorage.Query{Prefix: prefix})
	var parts []domain.Part
	found := false
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, translateErr(err, bucket, key)
		}
		found = true
		if !strings.HasPrefix(attrs.Name, prefix+"part-") {
			continue
		}
		num := 0
		fmt.Sscanf(strings.TrimPrefix(attrs.Name, prefix+"part-"), "%d", &num)
		parts = append(parts, domain.Part{
			Number:       int32(num),
			ETag:         "\"" + attrs.Etag + "\"",
			Size:         attrs.Size,
			LastModified: attrs.Updated,
		})
	}
	if !found {
		return nil, domain.NoSuchUpload(uploadID)
	}
	return parts, nil
}

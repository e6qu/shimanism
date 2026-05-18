// Package inmem is a real, in-memory implementation of shimanism's
// neutral storage domain. It is intended for the conformance harness
// and for short-lived local development — it does not persist across
// restarts. Not a fake: every method performs real storage logic on
// an in-process map.
//
// Production deployments use a cloud-talking backend
// (services/storage/backends/aws, /gcs, /azureblob, /minio, /k8s).
package inmem

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Backend implements domain.Storage with an in-process map. Zero
// value is ready to use; call New for clarity.
type Backend struct {
	mu      sync.Mutex
	buckets map[string]*bucketState
	uploads map[string]*uploadState // upload IDs are account-wide
}

// New returns a fresh Backend.
func New() *Backend {
	return &Backend{
		buckets: map[string]*bucketState{},
		uploads: map[string]*uploadState{},
	}
}

type bucketState struct {
	created time.Time
	region  string
	objects map[string]*objectState
}

type objectState struct {
	data         []byte
	contentType  string
	metadata     map[string]string
	lastModified time.Time
	etag         string
}

type uploadState struct {
	bucket      string
	key         string
	created     time.Time
	parts       map[int32]*partState
	completed   bool
	contentType string
	metadata    map[string]string
}

type partState struct {
	data       []byte
	etag       string
	uploadedAt time.Time
}

// ---- helpers ----

func etagOf(data []byte) string {
	sum := md5.Sum(data)
	return fmt.Sprintf("%q", hex.EncodeToString(sum[:]))
}

func copyMeta(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Compile-time check.
var _ domain.Storage = (*Backend)(nil)

// ----------------------------------------------------------------------
// Bucket lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListBuckets(ctx context.Context, opt domain.ListBucketsOptions) (domain.ListBucketsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	names := make([]string, 0, len(b.buckets))
	for n, st := range b.buckets {
		if opt.Prefix != "" && !strings.HasPrefix(n, opt.Prefix) {
			continue
		}
		if opt.Region != "" && st.region != opt.Region {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	start := 0
	if opt.NextToken != "" {
		for i, n := range names {
			if n > opt.NextToken {
				start = i
				break
			}
			start = i + 1
		}
	}
	names = names[start:]

	var nextToken string
	if opt.MaxResults > 0 && opt.MaxResults < len(names) {
		cut := names[opt.MaxResults-1]
		names = names[:opt.MaxResults]
		nextToken = cut
	}

	res := domain.ListBucketsResult{
		Owner:     domain.Owner{ID: "shimanism-inmem", DisplayName: "inmem"},
		Prefix:    opt.Prefix,
		NextToken: nextToken,
	}
	for _, n := range names {
		st := b.buckets[n]
		res.Buckets = append(res.Buckets, domain.Bucket{
			Name: n, CreatedAt: st.created, Region: st.region,
		})
	}
	return res, nil
}

func (b *Backend) CreateBucket(ctx context.Context, name, region string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[name]; ok {
		// Idempotent here matches MinIO; AWS-specific 409 is layered
		// by the AWS passthrough backend.
		return nil
	}
	if region == "" {
		region = "us-east-1"
	}
	b.buckets[name] = &bucketState{
		created: time.Now().UTC(),
		region:  region,
		objects: map[string]*objectState{},
	}
	return nil
}

func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[name]
	if !ok {
		return domain.NoSuchBucket(name)
	}
	if len(st.objects) > 0 {
		return domain.BucketNotEmpty(name)
	}
	delete(b.buckets, name)
	return nil
}

func (b *Backend) HeadBucket(ctx context.Context, name string) (domain.Bucket, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[name]
	if !ok {
		return domain.Bucket{}, domain.NoSuchBucket(name)
	}
	return domain.Bucket{Name: name, CreatedAt: st.created, Region: st.region}, nil
}

// ----------------------------------------------------------------------
// Object lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListObjects(ctx context.Context, opt domain.ListObjectsOptions) (domain.ListObjectsResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[opt.Bucket]
	if !ok {
		return domain.ListObjectsResult{}, domain.NoSuchBucket(opt.Bucket)
	}

	startAfter := opt.StartAfter
	if opt.NextToken != "" {
		startAfter = opt.NextToken
	}
	maxKeys := opt.MaxResults
	if maxKeys <= 0 {
		maxKeys = 1000
	}

	keys := make([]string, 0, len(st.objects))
	for k := range st.objects {
		if !strings.HasPrefix(k, opt.Prefix) {
			continue
		}
		if startAfter != "" && k <= startAfter {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	commonPrefixes := map[string]bool{}
	var objects []domain.ObjectMetadata
	for _, k := range keys {
		if opt.Delimiter != "" {
			rem := k[len(opt.Prefix):]
			if idx := strings.Index(rem, opt.Delimiter); idx >= 0 {
				commonPrefixes[opt.Prefix+rem[:idx+len(opt.Delimiter)]] = true
				continue
			}
		}
		obj := st.objects[k]
		objects = append(objects, domain.ObjectMetadata{
			Key:          k,
			Size:         int64(len(obj.data)),
			ETag:         obj.etag,
			LastModified: obj.lastModified,
		})
		if len(objects) >= maxKeys {
			break
		}
	}

	res := domain.ListObjectsResult{
		Bucket:      opt.Bucket,
		Prefix:      opt.Prefix,
		Delimiter:   opt.Delimiter,
		Objects:     objects,
		KeyCount:    len(objects),
		IsTruncated: len(objects) >= maxKeys && len(keys) > maxKeys,
	}
	if res.IsTruncated && len(objects) > 0 {
		res.NextToken = objects[len(objects)-1].Key
	}
	for cp := range commonPrefixes {
		res.CommonPrefixes = append(res.CommonPrefixes, cp)
	}
	sort.Strings(res.CommonPrefixes)
	return res, nil
}

func (b *Backend) GetObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[bucket]
	if !ok {
		return domain.Object{}, domain.NoSuchBucket(bucket)
	}
	obj, ok := st.objects[key]
	if !ok {
		return domain.Object{}, domain.NoSuchKey(bucket, key)
	}
	body := make([]byte, len(obj.data))
	copy(body, obj.data)
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         int64(len(obj.data)),
		ETag:         obj.etag,
		LastModified: obj.lastModified,
		ContentType:  obj.contentType,
		Metadata:     copyMeta(obj.metadata),
		Body:         io.NopCloser(bytes.NewReader(body)),
	}, nil
}

func (b *Backend) PutObject(ctx context.Context, opt domain.PutObjectOptions) (domain.PutObjectResult, error) {
	var data []byte
	if opt.Body != nil {
		var err error
		data, err = io.ReadAll(opt.Body)
		if err != nil {
			return domain.PutObjectResult{}, fmt.Errorf("inmem PutObject read body: %w", err)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[opt.Bucket]
	if !ok {
		return domain.PutObjectResult{}, domain.NoSuchBucket(opt.Bucket)
	}
	obj := &objectState{
		data:         data,
		contentType:  opt.ContentType,
		metadata:     copyMeta(opt.Metadata),
		lastModified: time.Now().UTC(),
		etag:         etagOf(data),
	}
	if obj.contentType == "" {
		obj.contentType = "application/octet-stream"
	}
	st.objects[opt.Key] = obj
	return domain.PutObjectResult{ETag: obj.etag}, nil
}

func (b *Backend) DeleteObject(ctx context.Context, bucket, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[bucket]
	if !ok {
		return domain.NoSuchBucket(bucket)
	}
	delete(st.objects, key)
	return nil
}

func (b *Backend) HeadObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[bucket]
	if !ok {
		return domain.Object{}, domain.NoSuchBucket(bucket)
	}
	obj, ok := st.objects[key]
	if !ok {
		return domain.Object{}, domain.NoSuchKey(bucket, key)
	}
	return domain.Object{
		Bucket:       bucket,
		Key:          key,
		Size:         int64(len(obj.data)),
		ETag:         obj.etag,
		LastModified: obj.lastModified,
		ContentType:  obj.contentType,
		Metadata:     copyMeta(obj.metadata),
		// Body intentionally nil for HEAD.
	}, nil
}

func (b *Backend) CopyObject(ctx context.Context, opt domain.CopyObjectOptions) (domain.CopyObjectResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	srcSt, ok := b.buckets[opt.SrcBucket]
	if !ok {
		return domain.CopyObjectResult{}, domain.NoSuchBucket(opt.SrcBucket)
	}
	srcObj, ok := srcSt.objects[opt.SrcKey]
	if !ok {
		return domain.CopyObjectResult{}, domain.NoSuchKey(opt.SrcBucket, opt.SrcKey)
	}
	dstSt, ok := b.buckets[opt.DstBucket]
	if !ok {
		return domain.CopyObjectResult{}, domain.NoSuchBucket(opt.DstBucket)
	}
	body := make([]byte, len(srcObj.data))
	copy(body, srcObj.data)
	now := time.Now().UTC()
	dst := &objectState{
		data:         body,
		contentType:  srcObj.contentType,
		metadata:     copyMeta(srcObj.metadata),
		lastModified: now,
		etag:         srcObj.etag,
	}
	if opt.ContentType != "" {
		dst.contentType = opt.ContentType
	}
	if opt.MetadataDirective == "REPLACE" {
		dst.metadata = copyMeta(opt.Metadata)
	}
	dstSt.objects[opt.DstKey] = dst
	return domain.CopyObjectResult{ETag: dst.etag, LastModified: now}, nil
}

// ----------------------------------------------------------------------
// Multipart
// ----------------------------------------------------------------------

func (b *Backend) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[bucket]; !ok {
		return "", domain.NoSuchBucket(bucket)
	}
	src := fmt.Sprintf("%s|%s|%d", bucket, key, time.Now().UnixNano())
	sum := md5.Sum([]byte(src))
	id := hex.EncodeToString(sum[:])
	b.uploads[id] = &uploadState{
		bucket:      bucket,
		key:         key,
		created:     time.Now().UTC(),
		parts:       map[int32]*partState{},
		contentType: contentType,
		metadata:    copyMeta(metadata),
	}
	return id, nil
}

func (b *Backend) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	if partNumber < 1 || partNumber > 10000 {
		return "", domain.InvalidArgument("PartNumber out of range")
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return "", fmt.Errorf("inmem UploadPart read body: %w", err)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	up, ok := b.uploads[uploadID]
	if !ok {
		return "", domain.NoSuchUpload(uploadID)
	}
	if up.bucket != bucket || up.key != key {
		return "", domain.InvalidArgument("upload key/bucket mismatch")
	}
	part := &partState{
		data:       data,
		etag:       etagOf(data),
		uploadedAt: time.Now().UTC(),
	}
	up.parts[partNumber] = part
	return part.etag, nil
}

func (b *Backend) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []domain.CompletePartRef) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	up, ok := b.uploads[uploadID]
	if !ok {
		return "", domain.NoSuchUpload(uploadID)
	}
	if up.bucket != bucket || up.key != key {
		return "", domain.InvalidArgument("upload key/bucket mismatch")
	}
	bs, ok := b.buckets[bucket]
	if !ok {
		return "", domain.NoSuchBucket(bucket)
	}
	nums := make([]int32, 0, len(up.parts))
	for n := range up.parts {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	var assembled []byte
	for _, n := range nums {
		assembled = append(assembled, up.parts[n].data...)
	}
	now := time.Now().UTC()
	// S3 multipart ETag is `md5(concat(part-md5s))-<count>`, NOT the
	// md5 of the assembled object. Use the part refs to compute it so
	// SDK clients verifying multipart ETags see the canonical shape
	// across every backend.
	multipartTag := domain.MultipartETag(parts)
	obj := &objectState{
		data:         assembled,
		contentType:  up.contentType,
		metadata:     copyMeta(up.metadata),
		lastModified: now,
		etag:         multipartTag,
	}
	if obj.contentType == "" {
		obj.contentType = "application/octet-stream"
	}
	bs.objects[key] = obj
	delete(b.uploads, uploadID)
	return obj.etag, nil
}

func (b *Backend) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.uploads[uploadID]; !ok {
		return domain.NoSuchUpload(uploadID)
	}
	delete(b.uploads, uploadID)
	return nil
}

func (b *Backend) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]domain.MultipartUpload, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[bucket]; !ok {
		return nil, domain.NoSuchBucket(bucket)
	}
	type entry struct {
		id, key string
		created time.Time
	}
	var matched []entry
	for id, up := range b.uploads {
		if up.bucket != bucket {
			continue
		}
		if prefix != "" && !strings.HasPrefix(up.key, prefix) {
			continue
		}
		matched = append(matched, entry{id: id, key: up.key, created: up.created})
	}
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].key != matched[j].key {
			return matched[i].key < matched[j].key
		}
		return matched[i].id < matched[j].id
	})
	out := make([]domain.MultipartUpload, 0, len(matched))
	for _, m := range matched {
		out = append(out, domain.MultipartUpload{
			UploadID: m.id, Bucket: bucket, Key: m.key, Initiated: m.created,
		})
	}
	return out, nil
}

func (b *Backend) ListParts(ctx context.Context, bucket, key, uploadID string) ([]domain.Part, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	up, ok := b.uploads[uploadID]
	if !ok {
		return nil, domain.NoSuchUpload(uploadID)
	}
	if up.bucket != bucket || up.key != key {
		return nil, domain.InvalidArgument("upload key/bucket mismatch")
	}
	nums := make([]int32, 0, len(up.parts))
	for n := range up.parts {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	out := make([]domain.Part, 0, len(nums))
	for _, n := range nums {
		p := up.parts[n]
		out = append(out, domain.Part{
			Number:       n,
			ETag:         p.etag,
			Size:         int64(len(p.data)),
			LastModified: p.uploadedAt,
		})
	}
	return out, nil
}

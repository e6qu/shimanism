// Package inmem is a real, in-memory implementation of the storage
// shim's intersection-operation surface. It is intended for the
// conformance harness and for short-lived local development — it does
// not persist across restarts. The package is not a fake: every method
// performs real storage logic on an in-process map.
//
// Production deployments use one of the real backends:
//   - services/storage/backends/aws       (passthrough)
//   - services/storage/backends/gcs       (S3 -> GCS translation)
//   - services/storage/backends/azureblob (S3 -> Azure Blob)
//   - services/storage/backends/minio     (S3-compatible passthrough)
package inmem

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/restxml"
	gen "github.com/e6qu/shimanism/services/storage/gen"
)

// Backend is an in-memory implementation of every operation in
// services/storage/codegen.json — i.e., the intersection surface.
// Zero value is ready to use; call New for a documented constructor.
type Backend struct {
	mu      sync.Mutex
	buckets map[string]*bucketState
	// uploads is global (not per-bucket) because S3 upload IDs are
	// account-wide unique. The bucket field on each upload records
	// which bucket the parts belong to.
	uploads map[string]*uploadState
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
	etag         string // canonical S3 "..." quoted form
}

type uploadState struct {
	bucket    string
	key       string
	created   time.Time
	parts     map[int32]*partState
	completed bool
}

type partState struct {
	data       []byte
	etag       string
	uploadedAt time.Time
}

// ---- small helpers ----

func ptr[T any](v T) *T { return &v }

func deref[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func etagOf(data []byte) string {
	sum := md5.Sum(data)
	return fmt.Sprintf("%q", hex.EncodeToString(sum[:]))
}

// ----------------------------------------------------------------------
// Bucket-level operations
// ----------------------------------------------------------------------

// ListBuckets returns every bucket in lexicographic order, optionally
// filtered by Prefix / BucketRegion and paginated by ContinuationToken
// + MaxBuckets.
func (b *Backend) ListBuckets(ctx context.Context, in *gen.ListBucketsRequest) (*gen.ListBucketsOutput, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	names := make([]string, 0, len(b.buckets))
	for n, st := range b.buckets {
		if in != nil {
			if p := deref(in.Prefix, ""); p != "" && !strings.HasPrefix(n, p) {
				continue
			}
			if r := deref(in.BucketRegion, ""); r != "" && st.region != r {
				continue
			}
		}
		names = append(names, n)
	}
	sort.Strings(names)

	// Continuation: skip up to (and including) the named token.
	start := 0
	if in != nil {
		if tok := deref(in.ContinuationToken, ""); tok != "" {
			for i, n := range names {
				if n > tok {
					start = i
					break
				}
				start = i + 1
			}
		}
	}
	names = names[start:]

	var nextToken *string
	if in != nil {
		if max := deref(in.MaxBuckets, 0); max > 0 && int(max) < len(names) {
			cut := names[max-1]
			names = names[:max]
			nextToken = ptr(cut)
		}
	}

	out := &gen.ListBucketsOutput{
		Buckets: gen.Buckets{},
		Owner:   &gen.Owner{ID: ptr("shimanism-inmem"), DisplayName: ptr("inmem")},
	}
	if nextToken != nil {
		out.ContinuationToken = nextToken
	}
	if in != nil {
		out.Prefix = in.Prefix
	}
	for _, n := range names {
		st := b.buckets[n]
		out.Buckets.Items = append(out.Buckets.Items, gen.Bucket{
			Name:         ptr(n),
			CreationDate: ptr(st.created),
			BucketRegion: ptr(st.region),
		})
	}
	return out, nil
}

// CreateBucket creates a new bucket. Idempotent: re-creating an
// existing bucket succeeds (matches MinIO; real AWS returns 409, but
// the harness backend does not enforce that here — backends that need
// 409 layer it on).
func (b *Backend) CreateBucket(ctx context.Context, in *gen.CreateBucketRequest) (*gen.CreateBucketOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("CreateBucket: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok {
		region := "us-east-1"
		if in.CreateBucketConfiguration != nil && in.CreateBucketConfiguration.LocationConstraint != nil {
			region = string(*in.CreateBucketConfiguration.LocationConstraint)
		}
		b.buckets[in.Bucket] = &bucketState{
			created: time.Now().UTC(),
			region:  region,
			objects: map[string]*objectState{},
		}
	}
	return &gen.CreateBucketOutput{Location: ptr("/" + in.Bucket)}, nil
}

// DeleteBucket deletes a bucket; fails if not empty.
// DeleteBucket has no output type in the spec (returns 204).
func (b *Backend) DeleteBucket(ctx context.Context, in *gen.DeleteBucketRequest) (struct{}, error) {
	if in == nil {
		return struct{}{}, fmt.Errorf("DeleteBucket: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return struct{}{}, restxml.NoSuchBucket(in.Bucket)
	}
	if len(st.objects) > 0 {
		return struct{}{}, restxml.BucketNotEmpty(in.Bucket)
	}
	delete(b.buckets, in.Bucket)
	return struct{}{}, nil
}

// HeadBucket reports whether the bucket exists.
func (b *Backend) HeadBucket(ctx context.Context, in *gen.HeadBucketRequest) (*gen.HeadBucketOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("HeadBucket: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	return &gen.HeadBucketOutput{BucketRegion: ptr(st.region)}, nil
}

// ----------------------------------------------------------------------
// Object-level operations
// ----------------------------------------------------------------------

// ListObjectsV2 returns objects in a bucket with optional prefix /
// delimiter handling and pagination.
func (b *Backend) ListObjectsV2(ctx context.Context, in *gen.ListObjectsV2Request) (*gen.ListObjectsV2Output, error) {
	if in == nil {
		return nil, fmt.Errorf("ListObjectsV2: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	prefix := deref(in.Prefix, "")
	delimiter := deref(in.Delimiter, "")
	maxKeys := int(deref(in.MaxKeys, 1000))
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	startAfter := deref(in.StartAfter, "")
	contTok := deref(in.ContinuationToken, "")
	if contTok != "" {
		startAfter = contTok
	}

	keys := make([]string, 0, len(st.objects))
	for k := range st.objects {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if startAfter != "" && k <= startAfter {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	commonPrefixes := map[string]bool{}
	contents := make([]gen.Object, 0, len(keys))
	for _, k := range keys {
		if delimiter != "" {
			rem := k[len(prefix):]
			if idx := strings.Index(rem, delimiter); idx >= 0 {
				commonPrefixes[prefix+rem[:idx+len(delimiter)]] = true
				continue
			}
		}
		obj := st.objects[k]
		contents = append(contents, gen.Object{
			Key:          ptr(k),
			LastModified: ptr(obj.lastModified),
			ETag:         ptr(obj.etag),
			Size:         ptr(int64(len(obj.data))),
		})
		if len(contents) >= maxKeys {
			break
		}
	}

	var next *string
	if len(contents) >= maxKeys && len(keys) > maxKeys {
		next = ptr(*contents[len(contents)-1].Key)
	}

	cpList := make([]gen.CommonPrefix, 0, len(commonPrefixes))
	for cp := range commonPrefixes {
		cp := cp
		cpList = append(cpList, gen.CommonPrefix{Prefix: &cp})
	}
	sort.Slice(cpList, func(i, j int) bool { return *cpList[i].Prefix < *cpList[j].Prefix })

	out := &gen.ListObjectsV2Output{
		Name:                  ptr(in.Bucket),
		Prefix:                in.Prefix,
		Delimiter:             in.Delimiter,
		MaxKeys:               ptr(int32(maxKeys)),
		KeyCount:              ptr(int32(len(contents))),
		IsTruncated:           ptr(next != nil),
		Contents:              contents,
		CommonPrefixes:        cpList,
		ContinuationToken:     in.ContinuationToken,
		NextContinuationToken: next,
		StartAfter:            in.StartAfter,
	}
	return out, nil
}

// PutObject writes an object's bytes and metadata.
func (b *Backend) PutObject(ctx context.Context, in *gen.PutObjectRequest) (*gen.PutObjectOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("PutObject: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	data := []byte{}
	if in.Body != nil {
		data = in.Body
	}
	obj := &objectState{
		data:         data,
		contentType:  deref(in.ContentType, "application/octet-stream"),
		metadata:     map[string]string(in.Metadata),
		lastModified: time.Now().UTC(),
		etag:         etagOf(data),
	}
	st.objects[in.Key] = obj
	return &gen.PutObjectOutput{ETag: ptr(obj.etag)}, nil
}

// GetObject returns an object's bytes + metadata.
func (b *Backend) GetObject(ctx context.Context, in *gen.GetObjectRequest) (*gen.GetObjectOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("GetObject: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	obj, ok := st.objects[in.Key]
	if !ok {
		return nil, restxml.NoSuchKey(in.Bucket, in.Key)
	}
	body := make([]byte, len(obj.data))
	copy(body, obj.data)
	return &gen.GetObjectOutput{
		Body:          body,
		ContentType:   ptr(obj.contentType),
		ContentLength: ptr(int64(len(obj.data))),
		ETag:          ptr(obj.etag),
		LastModified:  ptr(obj.lastModified),
		Metadata:      gen.Metadata(obj.metadata),
	}, nil
}

// DeleteObject removes an object. Idempotent.
func (b *Backend) DeleteObject(ctx context.Context, in *gen.DeleteObjectRequest) (*gen.DeleteObjectOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("DeleteObject: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	delete(st.objects, in.Key)
	return &gen.DeleteObjectOutput{}, nil
}

// HeadObject returns object metadata only.
func (b *Backend) HeadObject(ctx context.Context, in *gen.HeadObjectRequest) (*gen.HeadObjectOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("HeadObject: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	obj, ok := st.objects[in.Key]
	if !ok {
		return nil, restxml.NoSuchKey(in.Bucket, in.Key)
	}
	return &gen.HeadObjectOutput{
		ContentType:   ptr(obj.contentType),
		ContentLength: ptr(int64(len(obj.data))),
		ETag:          ptr(obj.etag),
		LastModified:  ptr(obj.lastModified),
		Metadata:      gen.Metadata(obj.metadata),
	}, nil
}

// CopyObject copies an existing object to a new location. The source
// is encoded in the CopySource header as "<bucket>/<key>" or
// "/<bucket>/<key>".
func (b *Backend) CopyObject(ctx context.Context, in *gen.CopyObjectRequest) (*gen.CopyObjectOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("CopyObject: missing input")
	}
	src := in.CopySource
	if src == "" {
		return nil, restxml.InvalidArgument("CopySource is required")
	}
	srcBucket, srcKey, err := parseCopySource(src)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	srcState, ok := b.buckets[srcBucket]
	if !ok {
		return nil, restxml.NoSuchBucket(srcBucket)
	}
	srcObj, ok := srcState.objects[srcKey]
	if !ok {
		return nil, restxml.NoSuchKey(srcBucket, srcKey)
	}
	dstState, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
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
	dstState.objects[in.Key] = dst
	return &gen.CopyObjectOutput{
		CopyObjectResult: &gen.CopyObjectResult{
			ETag:         ptr(dst.etag),
			LastModified: ptr(now),
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
		return "", "", restxml.InvalidArgument("CopySource does not parse: " + s)
	}
	bucket := s[:idx]
	key, err := url.QueryUnescape(s[idx+1:])
	if err != nil {
		return "", "", restxml.InvalidArgument("CopySource key unescape: " + err.Error())
	}
	return bucket, key, nil
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

// ----------------------------------------------------------------------
// Multipart upload operations
// ----------------------------------------------------------------------

// CreateMultipartUpload starts a new multipart upload session.
func (b *Backend) CreateMultipartUpload(ctx context.Context, in *gen.CreateMultipartUploadRequest) (*gen.CreateMultipartUploadOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("CreateMultipartUpload: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	id := newUploadID(in.Bucket, in.Key, time.Now())
	b.uploads[id] = &uploadState{
		bucket:  in.Bucket,
		key:     in.Key,
		created: time.Now().UTC(),
		parts:   map[int32]*partState{},
	}
	return &gen.CreateMultipartUploadOutput{
		Bucket:   ptr(in.Bucket),
		Key:      ptr(in.Key),
		UploadId: ptr(id),
	}, nil
}

func newUploadID(bucket, key string, now time.Time) string {
	src := fmt.Sprintf("%s|%s|%d", bucket, key, now.UnixNano())
	sum := md5.Sum([]byte(src))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// UploadPart stores a single part of an in-progress upload.
func (b *Backend) UploadPart(ctx context.Context, in *gen.UploadPartRequest) (*gen.UploadPartOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("UploadPart: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	up, ok := b.uploads[in.UploadId]
	if !ok {
		return nil, restxml.NoSuchUpload(in.UploadId)
	}
	if up.bucket != in.Bucket || up.key != in.Key {
		return nil, restxml.InvalidArgument("upload key/bucket mismatch")
	}
	partNo := in.PartNumber
	if partNo < 1 || partNo > 10000 {
		return nil, restxml.InvalidArgument("PartNumber out of range")
	}
	data := []byte{}
	if in.Body != nil {
		data = in.Body
	}
	part := &partState{
		data:       data,
		etag:       etagOf(data),
		uploadedAt: time.Now().UTC(),
	}
	up.parts[partNo] = part
	return &gen.UploadPartOutput{ETag: ptr(part.etag)}, nil
}

// CompleteMultipartUpload concatenates parts in part-number order and
// writes the resulting object.
func (b *Backend) CompleteMultipartUpload(ctx context.Context, in *gen.CompleteMultipartUploadRequest) (*gen.CompleteMultipartUploadOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("CompleteMultipartUpload: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	upID := in.UploadId
	up, ok := b.uploads[upID]
	if !ok {
		return nil, restxml.NoSuchUpload(upID)
	}
	if up.bucket != in.Bucket || up.key != in.Key {
		return nil, restxml.InvalidArgument("upload key/bucket mismatch")
	}
	bucket, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	// Order parts by number; client supplies the list, but in-mem
	// concatenates in numeric order regardless.
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
	obj := &objectState{
		data:         assembled,
		contentType:  "application/octet-stream",
		lastModified: now,
		etag:         etagOf(assembled),
	}
	bucket.objects[in.Key] = obj
	delete(b.uploads, upID)
	return &gen.CompleteMultipartUploadOutput{
		Bucket: ptr(in.Bucket),
		Key:    ptr(in.Key),
		ETag:   ptr(obj.etag),
	}, nil
}

// AbortMultipartUpload discards a partial upload's accumulated parts.
func (b *Backend) AbortMultipartUpload(ctx context.Context, in *gen.AbortMultipartUploadRequest) (*gen.AbortMultipartUploadOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("AbortMultipartUpload: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	upID := in.UploadId
	if _, ok := b.uploads[upID]; !ok {
		return nil, restxml.NoSuchUpload(upID)
	}
	delete(b.uploads, upID)
	return &gen.AbortMultipartUploadOutput{}, nil
}

// ListMultipartUploads enumerates in-progress uploads for a bucket.
func (b *Backend) ListMultipartUploads(ctx context.Context, in *gen.ListMultipartUploadsRequest) (*gen.ListMultipartUploadsOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("ListMultipartUploads: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	type entry struct {
		id, key string
		created time.Time
	}
	var matched []entry
	for id, up := range b.uploads {
		if up.bucket != in.Bucket {
			continue
		}
		if p := deref(in.Prefix, ""); p != "" && !strings.HasPrefix(up.key, p) {
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
	out := &gen.ListMultipartUploadsOutput{Bucket: ptr(in.Bucket), Prefix: in.Prefix}
	for _, m := range matched {
		m := m
		out.Uploads = append(out.Uploads, gen.MultipartUpload{
			UploadId:  ptr(m.id),
			Key:       ptr(m.key),
			Initiated: ptr(m.created),
		})
	}
	return out, nil
}

// ListParts returns the parts already uploaded for an in-progress upload.
func (b *Backend) ListParts(ctx context.Context, in *gen.ListPartsRequest) (*gen.ListPartsOutput, error) {
	if in == nil {
		return nil, fmt.Errorf("ListParts: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	upID := in.UploadId
	up, ok := b.uploads[upID]
	if !ok {
		return nil, restxml.NoSuchUpload(upID)
	}
	if up.bucket != in.Bucket || up.key != in.Key {
		return nil, restxml.InvalidArgument("upload key/bucket mismatch")
	}
	nums := make([]int32, 0, len(up.parts))
	for n := range up.parts {
		nums = append(nums, n)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	out := &gen.ListPartsOutput{
		Bucket:   ptr(in.Bucket),
		Key:      ptr(in.Key),
		UploadId: ptr(upID),
	}
	for _, n := range nums {
		p := up.parts[n]
		out.Parts = append(out.Parts, gen.Part{
			PartNumber:   ptr(n),
			ETag:         ptr(p.etag),
			LastModified: ptr(p.uploadedAt),
			Size:         ptr(int64(len(p.data))),
		})
	}
	return out, nil
}

// Ensure the implementation satisfies the union interface at build time.
var _ gen.AmazonS3Backend = (*Backend)(nil)

// ----------------------------------------------------------------------
// Bucket-config probes (Terraform AWS provider's Read calls these on
// every aws_s3_bucket apply). Each returns either the "feature not
// configured" 404 in S3's vocabulary, or a default-state 200 — both of
// which translate universally across S3 / GCS / Azure Blob / MinIO.
// ----------------------------------------------------------------------

func (b *Backend) GetBucketLocation(ctx context.Context, in *gen.GetBucketLocationRequest) (*gen.GetBucketLocationOutput, error) {
	if in == nil {
		return nil, restxml.InvalidArgument("GetBucketLocation: missing input")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.buckets[in.Bucket]
	if !ok {
		return nil, restxml.NoSuchBucket(in.Bucket)
	}
	loc := gen.BucketLocationConstraint(st.region)
	return &gen.GetBucketLocationOutput{LocationConstraint: &loc}, nil
}

func (b *Backend) GetBucketPolicy(ctx context.Context, in *gen.GetBucketPolicyRequest) (*gen.GetBucketPolicyOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketPolicy: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchBucketPolicy(in.Bucket)
}

func (b *Backend) GetBucketAcl(ctx context.Context, in *gen.GetBucketAclRequest) (*gen.GetBucketAclOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketAcl: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return &gen.GetBucketAclOutput{
		Owner: &gen.Owner{ID: ptr("shimanism-inmem"), DisplayName: ptr("inmem")},
	}, nil
}

func (b *Backend) GetBucketVersioning(ctx context.Context, in *gen.GetBucketVersioningRequest) (*gen.GetBucketVersioningOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketVersioning: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return &gen.GetBucketVersioningOutput{}, nil // empty = unversioned default
}

func (b *Backend) GetBucketLogging(ctx context.Context, in *gen.GetBucketLoggingRequest) (*gen.GetBucketLoggingOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketLogging: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return &gen.GetBucketLoggingOutput{}, nil // empty = logging disabled
}

func (b *Backend) GetBucketCors(ctx context.Context, in *gen.GetBucketCorsRequest) (*gen.GetBucketCorsOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketCors: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchCORSConfiguration(in.Bucket)
}

func (b *Backend) GetBucketLifecycleConfiguration(ctx context.Context, in *gen.GetBucketLifecycleConfigurationRequest) (*gen.GetBucketLifecycleConfigurationOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketLifecycleConfiguration: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchLifecycleConfiguration(in.Bucket)
}

func (b *Backend) GetBucketReplication(ctx context.Context, in *gen.GetBucketReplicationRequest) (*gen.GetBucketReplicationOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketReplication: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.ReplicationConfigurationNotFound(in.Bucket)
}

func (b *Backend) GetBucketRequestPayment(ctx context.Context, in *gen.GetBucketRequestPaymentRequest) (*gen.GetBucketRequestPaymentOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketRequestPayment: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	p := gen.PayerBucketOwner
	return &gen.GetBucketRequestPaymentOutput{Payer: &p}, nil
}

func (b *Backend) GetBucketTagging(ctx context.Context, in *gen.GetBucketTaggingRequest) (*gen.GetBucketTaggingOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketTagging: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchTagSet(in.Bucket)
}

func (b *Backend) GetBucketWebsite(ctx context.Context, in *gen.GetBucketWebsiteRequest) (*gen.GetBucketWebsiteOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketWebsite: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchWebsiteConfiguration(in.Bucket)
}

func (b *Backend) GetBucketEncryption(ctx context.Context, in *gen.GetBucketEncryptionRequest) (*gen.GetBucketEncryptionOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketEncryption: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.ServerSideEncryptionConfigurationNotFound(in.Bucket)
}

func (b *Backend) GetBucketAccelerateConfiguration(ctx context.Context, in *gen.GetBucketAccelerateConfigurationRequest) (*gen.GetBucketAccelerateConfigurationOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketAccelerateConfiguration: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return &gen.GetBucketAccelerateConfigurationOutput{}, nil // default = Suspended/absent
}

func (b *Backend) GetObjectLockConfiguration(ctx context.Context, in *gen.GetObjectLockConfigurationRequest) (*gen.GetObjectLockConfigurationOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetObjectLockConfiguration: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.ObjectLockConfigurationNotFound(in.Bucket)
}

func (b *Backend) GetBucketNotificationConfiguration(ctx context.Context, in *gen.GetBucketNotificationConfigurationRequest) (*gen.NotificationConfiguration, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketNotificationConfiguration: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return &gen.NotificationConfiguration{}, nil // empty = no notifications
}

func (b *Backend) GetBucketOwnershipControls(ctx context.Context, in *gen.GetBucketOwnershipControlsRequest) (*gen.GetBucketOwnershipControlsOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketOwnershipControls: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.OwnershipControlsNotFound(in.Bucket)
}

func (b *Backend) GetBucketPolicyStatus(ctx context.Context, in *gen.GetBucketPolicyStatusRequest) (*gen.GetBucketPolicyStatusOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetBucketPolicyStatus: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	// No policy means status is empty/private; AWS returns 404 NoSuchBucketPolicy
	return nil, restxml.NoSuchBucketPolicy(in.Bucket)
}

func (b *Backend) GetPublicAccessBlock(ctx context.Context, in *gen.GetPublicAccessBlockRequest) (*gen.GetPublicAccessBlockOutput, error) {
	if in == nil { return nil, restxml.InvalidArgument("GetPublicAccessBlock: missing input") }
	b.mu.Lock(); defer b.mu.Unlock()
	if _, ok := b.buckets[in.Bucket]; !ok { return nil, restxml.NoSuchBucket(in.Bucket) }
	return nil, restxml.NoSuchPublicAccessBlockConfiguration(in.Bucket)
}

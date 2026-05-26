// Package azureblob is the Azure Blob Storage backend for
// shimanism's neutral storage domain. It uses Azure SDK for Go's
// azblob package (MIT, allowlisted).
//
// Cross-shape backend: the frontend speaks AWS S3; this backend
// translates to Azure Blob block-blob operations.
//
// Multipart strategy: Azure uses base64-encoded block IDs natively.
// We map S3 multipart as follows:
//
//   - CreateMultipartUpload: generate a random uploadID; write a
//     marker blob at <key>.uploads/<uploadID>/.init holding the
//     user-supplied content-type and metadata for the eventual
//     CommitBlockList.
//   - UploadPart: stage a single block on the target blob with
//     block ID derived from (uploadID, partNumber).
//   - CompleteMultipartUpload: gather block IDs in part-number
//     order, call CommitBlockList with the user metadata.
//   - AbortMultipartUpload: delete the marker; uncommitted blocks
//     auto-expire after 7 days per Azure policy.
//   - ListMultipartUploads: discover markers via blob listing.
//   - ListParts: GetBlockList(uncommitted) on the target blob.
package azureblob

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Backend implements domain.Storage using Azure Blob Storage.
type Backend struct {
	c *azblob.Client
	// Region from Azure Blob's perspective is the storage account's
	// region; not always discoverable per-container. DefaultRegion
	// fills HeadBucket / ListBuckets results when not otherwise known.
	defaultRegion string
}

// New wraps an existing azblob.Client.
func New(c *azblob.Client, defaultRegion string) *Backend {
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}
	return &Backend{c: c, defaultRegion: defaultRegion}
}

// Compile-time check.
var _ domain.Storage = (*Backend)(nil)

// translateErr maps azure-sdk-for-go errors to domain errors via
// azcore's typed ResponseError.
func translateErr(err error, bucket, key string) error {
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "ContainerNotFound":
			return domain.NoSuchBucket(bucket)
		case "BlobNotFound":
			return domain.NoSuchKey(bucket, key)
		case "ContainerAlreadyExists":
			return domain.BucketAlreadyExists(bucket)
		case "ContainerBeingDeleted":
			return domain.InvalidArgument("container is being deleted")
		case "InvalidArgument", "InvalidInput":
			return domain.InvalidArgument(re.RawResponse.Status)
		}
		// HTTP status fallback.
		if re.StatusCode == 404 {
			if key != "" {
				return domain.NoSuchKey(bucket, key)
			}
			return domain.NoSuchBucket(bucket)
		}
	}
	return err
}

// ----------------------------------------------------------------------
// Bucket lifecycle ("container" in Azure terminology)
// ----------------------------------------------------------------------

func (b *Backend) ListBuckets(ctx context.Context, opt domain.ListBucketsOptions) (domain.ListBucketsResult, error) {
	pager := b.c.NewListContainersPager(&service.ListContainersOptions{
		Prefix: nullIfEmpty(opt.Prefix),
	})
	res := domain.ListBucketsResult{Prefix: opt.Prefix}
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return domain.ListBucketsResult{}, translateErr(err, "", "")
		}
		for _, c := range page.ContainerItems {
			name := ""
			if c.Name != nil {
				name = *c.Name
			}
			created := time.Time{}
			etag := ""
			if c.Properties != nil {
				if c.Properties.LastModified != nil {
					created = *c.Properties.LastModified
				}
				if c.Properties.ETag != nil {
					etag = string(*c.Properties.ETag)
				}
			}
			res.Buckets = append(res.Buckets, domain.Bucket{
				Name:      name,
				CreatedAt: created,
				Region:    b.defaultRegion,
				ETag:      etag,
			})
			if opt.MaxResults > 0 && len(res.Buckets) >= opt.MaxResults {
				return res, nil
			}
		}
	}
	return res, nil
}

func (b *Backend) CreateBucket(ctx context.Context, name, region string) error {
	_, err := b.c.CreateContainer(ctx, name, nil)
	if err == nil {
		return nil
	}
	var re *azcore.ResponseError
	if errors.As(err, &re) && re.ErrorCode == "ContainerAlreadyExists" {
		return nil
	}
	return translateErr(err, name, "")
}

func (b *Backend) DeleteBucket(ctx context.Context, name string) error {
	_, err := b.c.DeleteContainer(ctx, name, nil)
	return translateErr(err, name, "")
}

func (b *Backend) HeadBucket(ctx context.Context, name string) (domain.Bucket, error) {
	cc := b.c.ServiceClient().NewContainerClient(name)
	props, err := cc.GetProperties(ctx, nil)
	if err != nil {
		return domain.Bucket{}, translateErr(err, name, "")
	}
	bucket := domain.Bucket{Name: name, Region: b.defaultRegion}
	if props.LastModified != nil {
		bucket.CreatedAt = *props.LastModified
	}
	if props.ETag != nil {
		bucket.ETag = string(*props.ETag)
	}
	return bucket, nil
}

// ----------------------------------------------------------------------
// Object lifecycle
// ----------------------------------------------------------------------

func (b *Backend) ListObjects(ctx context.Context, opt domain.ListObjectsOptions) (domain.ListObjectsResult, error) {
	cc := b.c.ServiceClient().NewContainerClient(opt.Bucket)
	maxKeys := opt.MaxResults
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	res := domain.ListObjectsResult{
		Bucket:    opt.Bucket,
		Prefix:    opt.Prefix,
		Delimiter: opt.Delimiter,
	}
	startAfter := opt.StartAfter
	if opt.NextToken != "" {
		startAfter = opt.NextToken
	}

	if opt.Delimiter == "" {
		pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
			Prefix: nullIfEmpty(opt.Prefix),
		})
		for pager.More() && len(res.Objects) < maxKeys {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return domain.ListObjectsResult{}, translateErr(err, opt.Bucket, "")
			}
			for _, blobItem := range page.Segment.BlobItems {
				name := stringFrom(blobItem.Name)
				if startAfter != "" && name <= startAfter {
					continue
				}
				if strings.Contains(name, ".uploads/") {
					continue
				}
				res.Objects = append(res.Objects, blobItemToObjectMeta(blobItem))
				if len(res.Objects) >= maxKeys {
					break
				}
			}
		}
	} else {
		pager := cc.NewListBlobsHierarchyPager(opt.Delimiter, &container.ListBlobsHierarchyOptions{
			Prefix: nullIfEmpty(opt.Prefix),
		})
		for pager.More() && len(res.Objects) < maxKeys {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return domain.ListObjectsResult{}, translateErr(err, opt.Bucket, "")
			}
			for _, p := range page.Segment.BlobPrefixes {
				if p.Name != nil {
					res.CommonPrefixes = append(res.CommonPrefixes, *p.Name)
				}
			}
			for _, blobItem := range page.Segment.BlobItems {
				name := stringFrom(blobItem.Name)
				if startAfter != "" && name <= startAfter {
					continue
				}
				if strings.Contains(name, ".uploads/") {
					continue
				}
				res.Objects = append(res.Objects, blobItemToObjectMeta(blobItem))
				if len(res.Objects) >= maxKeys {
					break
				}
			}
		}
	}
	res.KeyCount = len(res.Objects)
	if len(res.Objects) >= maxKeys {
		res.IsTruncated = true
		res.NextToken = res.Objects[len(res.Objects)-1].Key
	}
	return res, nil
}

func blobItemToObjectMeta(bi *container.BlobItem) domain.ObjectMetadata {
	m := domain.ObjectMetadata{Key: stringFrom(bi.Name)}
	if bi.Properties != nil {
		if bi.Properties.ContentLength != nil {
			m.Size = *bi.Properties.ContentLength
		}
		if bi.Properties.ETag != nil {
			m.ETag = "\"" + strings.Trim(string(*bi.Properties.ETag), "\"") + "\""
		}
		if bi.Properties.LastModified != nil {
			m.LastModified = *bi.Properties.LastModified
		}
		if bi.Properties.AccessTier != nil {
			m.StorageClass = string(*bi.Properties.AccessTier)
		}
	}
	return m
}

func (b *Backend) GetObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlobClient(key)
	resp, err := bc.DownloadStream(ctx, nil)
	if err != nil {
		return domain.Object{}, translateErr(err, bucket, key)
	}
	obj := domain.Object{
		Bucket: bucket,
		Key:    key,
		Body:   resp.Body,
	}
	if resp.ContentLength != nil {
		obj.Size = *resp.ContentLength
	}
	if resp.ETag != nil {
		obj.ETag = "\"" + strings.Trim(string(*resp.ETag), "\"") + "\""
	}
	if resp.LastModified != nil {
		obj.LastModified = *resp.LastModified
	}
	if resp.ContentType != nil {
		obj.ContentType = *resp.ContentType
	}
	if resp.Metadata != nil {
		obj.Metadata = map[string]string{}
		for k, v := range resp.Metadata {
			if v != nil {
				obj.Metadata[strings.ToLower(k)] = *v
			}
		}
	}
	return obj, nil
}

func (b *Backend) PutObject(ctx context.Context, opt domain.PutObjectOptions) (domain.PutObjectResult, error) {
	bc := b.c.ServiceClient().NewContainerClient(opt.Bucket).NewBlockBlobClient(opt.Key)
	uploadOpts := &blockblob.UploadStreamOptions{}
	if opt.ContentType != "" {
		uploadOpts.HTTPHeaders = &blob.HTTPHeaders{BlobContentType: &opt.ContentType}
	}
	if len(opt.Metadata) > 0 {
		uploadOpts.Metadata = map[string]*string{}
		for k, v := range opt.Metadata {
			v := v
			uploadOpts.Metadata[k] = &v
		}
	}
	resp, err := bc.UploadStream(ctx, opt.Body, uploadOpts)
	if err != nil {
		return domain.PutObjectResult{}, translateErr(err, opt.Bucket, opt.Key)
	}
	etag := ""
	if resp.ETag != nil {
		etag = "\"" + strings.Trim(string(*resp.ETag), "\"") + "\""
	}
	return domain.PutObjectResult{ETag: etag}, nil
}

func (b *Backend) DeleteObject(ctx context.Context, bucket, key string) error {
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlobClient(key)
	_, err := bc.Delete(ctx, nil)
	if err != nil {
		var re *azcore.ResponseError
		if errors.As(err, &re) && re.ErrorCode == "BlobNotFound" {
			return nil // idempotent
		}
	}
	return translateErr(err, bucket, key)
}

func (b *Backend) HeadObject(ctx context.Context, bucket, key string) (domain.Object, error) {
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlobClient(key)
	props, err := bc.GetProperties(ctx, nil)
	if err != nil {
		return domain.Object{}, translateErr(err, bucket, key)
	}
	obj := domain.Object{Bucket: bucket, Key: key}
	if props.ContentLength != nil {
		obj.Size = *props.ContentLength
	}
	if props.ETag != nil {
		obj.ETag = "\"" + strings.Trim(string(*props.ETag), "\"") + "\""
	}
	if props.LastModified != nil {
		obj.LastModified = *props.LastModified
	}
	if props.ContentType != nil {
		obj.ContentType = *props.ContentType
	}
	if props.Metadata != nil {
		obj.Metadata = map[string]string{}
		for k, v := range props.Metadata {
			if v != nil {
				obj.Metadata[strings.ToLower(k)] = *v
			}
		}
	}
	return obj, nil
}

func (b *Backend) CopyObject(ctx context.Context, opt domain.CopyObjectOptions) (domain.CopyObjectResult, error) {
	srcURL := b.c.ServiceClient().NewContainerClient(opt.SrcBucket).NewBlobClient(opt.SrcKey).URL()
	dstClient := b.c.ServiceClient().NewContainerClient(opt.DstBucket).NewBlobClient(opt.DstKey)
	startOpts := &blob.StartCopyFromURLOptions{}
	if opt.MetadataDirective == "REPLACE" && len(opt.Metadata) > 0 {
		startOpts.Metadata = map[string]*string{}
		for k, v := range opt.Metadata {
			v := v
			startOpts.Metadata[k] = &v
		}
	}
	resp, err := dstClient.StartCopyFromURL(ctx, srcURL, startOpts)
	if err != nil {
		return domain.CopyObjectResult{}, translateErr(err, opt.DstBucket, opt.DstKey)
	}
	// Poll for completion. Same-account copies are usually immediate,
	// but Azure technically allows asynchronous copy for cross-account
	// or very large blobs. The loop fails loud on Azure-reported
	// "failed"/"aborted" status (no silent partial copy) and on
	// still-pending after the budget (matches the
	// no-fakes/no-fallbacks rule).
	status := ""
	if resp.CopyStatus != nil {
		status = string(*resp.CopyStatus)
	}
	deadline := time.Now().Add(30 * time.Second)
	for status == "pending" && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		props, perr := dstClient.GetProperties(ctx, nil)
		if perr != nil {
			return domain.CopyObjectResult{}, translateErr(perr, opt.DstBucket, opt.DstKey)
		}
		if props.CopyStatus != nil {
			status = string(*props.CopyStatus)
		}
		resp.ETag = props.ETag
		resp.LastModified = props.LastModified
	}
	switch status {
	case "success", "":
		// ok
	case "pending":
		return domain.CopyObjectResult{}, domain.InvalidArgument("copy still pending after 30s")
	default:
		return domain.CopyObjectResult{}, domain.InvalidArgument("copy " + status)
	}
	etag := ""
	if resp.ETag != nil {
		etag = "\"" + strings.Trim(string(*resp.ETag), "\"") + "\""
	}
	lm := time.Time{}
	if resp.LastModified != nil {
		lm = *resp.LastModified
	}
	return domain.CopyObjectResult{ETag: etag, LastModified: lm}, nil
}

// ----------------------------------------------------------------------
// Multipart (Azure block-blob block IDs)
// ----------------------------------------------------------------------

func blockIDFor(uploadID string, partNumber int32) string {
	// Block IDs must be base64-encoded and must all be the same
	// length within a blob. Encode (uploadID, partNumber) as a
	// fixed-length string before base64.
	raw := fmt.Sprintf("shim-%s-%05d", uploadID, partNumber)
	return base64.StdEncoding.EncodeToString([]byte(raw))
}

func markerKey(key, uploadID string) string {
	return key + ".uploads/" + uploadID + "/.init"
}

func (b *Backend) CreateMultipartUpload(ctx context.Context, bucket, key, contentType string, metadata map[string]string) (string, error) {
	src := fmt.Sprintf("%s|%s|%d", bucket, key, time.Now().UnixNano())
	sum := md5.Sum([]byte(src))
	id := hex.EncodeToString(sum[:])

	// Write a marker blob recording user-supplied content-type +
	// metadata so CompleteMultipartUpload can apply them.
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlockBlobClient(markerKey(key, id))
	meta := map[string]*string{}
	if contentType != "" {
		ct := contentType
		meta["shim-content-type"] = &ct
	}
	for k, v := range metadata {
		v := v
		meta["shim-user-"+k] = &v
	}
	_, err := bc.UploadBuffer(ctx, []byte{}, &blockblob.UploadBufferOptions{Metadata: meta})
	if err != nil {
		return "", translateErr(err, bucket, key)
	}
	return id, nil
}

func (b *Backend) UploadPart(ctx context.Context, bucket, key, uploadID string, partNumber int32, body io.Reader) (string, error) {
	if partNumber < 1 || partNumber > 10000 {
		return "", domain.InvalidArgument("PartNumber out of range")
	}
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlockBlobClient(key)
	blockID := blockIDFor(uploadID, partNumber)
	// StageBlock needs io.ReadSeekCloser (Azure SDK may retry). The
	// per-part buffering cost is bounded by the part size (typically
	// 5–64 MiB in S3 multipart workflows); object bodies in
	// PutObject still stream because UploadStream chunks internally.
	data, err := io.ReadAll(body)
	if err != nil {
		return "", translateErr(err, bucket, key)
	}
	rsc := streaming.NopCloser(bytes.NewReader(data))
	resp, err := bc.StageBlock(ctx, blockID, rsc, nil)
	if err != nil {
		return "", translateErr(err, bucket, key)
	}
	// Azure returns a content MD5 on stage; we use it as the "ETag"
	// of the part for clients that depend on it. Otherwise return
	// the block ID itself (which is what S3 clients typically use
	// for ordering anyway).
	etag := blockID
	if resp.ContentMD5 != nil {
		etag = base64.StdEncoding.EncodeToString(resp.ContentMD5)
	}
	return "\"" + etag + "\"", nil
}

func (b *Backend) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []domain.CompletePartRef) (string, error) {
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlockBlobClient(key)
	blockIDs := make([]string, len(parts))
	for i, p := range parts {
		blockIDs[i] = blockIDFor(uploadID, p.Number)
	}
	// Pull marker for content-type / metadata.
	mc := b.c.ServiceClient().NewContainerClient(bucket).NewBlobClient(markerKey(key, uploadID))
	headers := &blob.HTTPHeaders{}
	var meta map[string]*string
	if props, err := mc.GetProperties(ctx, nil); err == nil {
		if ct, ok := props.Metadata["shim-content-type"]; ok && ct != nil {
			headers.BlobContentType = ct
		}
		for k, v := range props.Metadata {
			if strings.HasPrefix(k, "shim-user-") && v != nil {
				if meta == nil {
					meta = map[string]*string{}
				}
				meta[strings.TrimPrefix(k, "shim-user-")] = v
			}
		}
	}
	if _, err := bc.CommitBlockList(ctx, blockIDs, &blockblob.CommitBlockListOptions{
		HTTPHeaders: headers,
		Metadata:    meta,
	}); err != nil {
		return "", translateErr(err, bucket, key)
	}
	// Best-effort cleanup of the marker; failure doesn't break the upload.
	_, _ = mc.Delete(ctx, nil)
	// Return the S3 multipart ETag, not Azure's native block-blob
	// ETag (which is unrelated to part md5s and would surprise S3
	// clients that verify multipart ETags).
	return domain.MultipartETag(parts), nil
}

func (b *Backend) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	// Azure doesn't expose explicit cancel for staged-but-uncommitted
	// blocks; they expire after 7 days. We delete the marker to free
	// the session state we own.
	mc := b.c.ServiceClient().NewContainerClient(bucket).NewBlobClient(markerKey(key, uploadID))
	_, err := mc.Delete(ctx, nil)
	if err != nil {
		var re *azcore.ResponseError
		if errors.As(err, &re) && re.ErrorCode == "BlobNotFound" {
			return domain.NoSuchUpload(uploadID)
		}
		return translateErr(err, bucket, key)
	}
	return nil
}

func (b *Backend) ListMultipartUploads(ctx context.Context, bucket, prefix string) ([]domain.MultipartUpload, error) {
	cc := b.c.ServiceClient().NewContainerClient(bucket)
	pager := cc.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{
		Prefix: nullIfEmpty(prefix),
	})
	var out []domain.MultipartUpload
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, translateErr(err, bucket, "")
		}
		for _, bi := range page.Segment.BlobItems {
			name := stringFrom(bi.Name)
			if !strings.HasSuffix(name, "/.init") {
				continue
			}
			nameNoInit := strings.TrimSuffix(name, "/.init")
			i := strings.LastIndex(nameNoInit, ".uploads/")
			if i < 0 {
				continue
			}
			origKey := nameNoInit[:i]
			uploadID := nameNoInit[i+len(".uploads/"):]
			created := time.Time{}
			if bi.Properties != nil && bi.Properties.LastModified != nil {
				created = *bi.Properties.LastModified
			}
			out = append(out, domain.MultipartUpload{
				UploadID:  uploadID,
				Bucket:    bucket,
				Key:       origKey,
				Initiated: created,
			})
		}
	}
	return out, nil
}

func (b *Backend) ListParts(ctx context.Context, bucket, key, uploadID string) ([]domain.Part, error) {
	bc := b.c.ServiceClient().NewContainerClient(bucket).NewBlockBlobClient(key)
	resp, err := bc.GetBlockList(ctx, blockblob.BlockListTypeUncommitted, nil)
	if err != nil {
		return nil, translateErr(err, bucket, key)
	}
	var parts []domain.Part
	for _, block := range resp.UncommittedBlocks {
		if block.Name == nil {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(*block.Name)
		if err != nil {
			continue
		}
		// Decode our "shim-<uploadID>-NNNNN" format.
		s := string(raw)
		if !strings.HasPrefix(s, "shim-"+uploadID+"-") {
			continue
		}
		num := 0
		fmt.Sscanf(strings.TrimPrefix(s, "shim-"+uploadID+"-"), "%d", &num)
		size := int64(0)
		if block.Size != nil {
			size = *block.Size
		}
		parts = append(parts, domain.Part{
			Number: int32(num),
			ETag:   "\"" + *block.Name + "\"",
			Size:   size,
		})
	}
	return parts, nil
}

// ---- helpers ----

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

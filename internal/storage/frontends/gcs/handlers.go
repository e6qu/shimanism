package gcs

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	raw "google.golang.org/api/storage/v1"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// ----------------------------------------------------------------------
// Buckets
// ----------------------------------------------------------------------

func (srv *Server) listBuckets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opt := domain.ListBucketsOptions{
		Prefix: q.Get("prefix"),
	}
	if mr := q.Get("maxResults"); mr != "" {
		if n, err := strconv.Atoi(mr); err == nil {
			opt.MaxResults = n
		}
	}
	opt.NextToken = q.Get("pageToken")
	out, err := srv.s.ListBuckets(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := raw.Buckets{
		Kind:          "storage#buckets",
		NextPageToken: out.NextToken,
	}
	for _, b := range out.Buckets {
		b := b
		resp.Items = append(resp.Items, &raw.Bucket{
			Kind:        "storage#bucket",
			Id:          b.Name,
			Name:        b.Name,
			Location:    strings.ToUpper(b.Region),
			TimeCreated: b.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	b, err := srv.s.HeadBucket(r.Context(), bucket)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &raw.Bucket{
		Kind:        "storage#bucket",
		Id:          b.Name,
		Name:        b.Name,
		Location:    strings.ToUpper(b.Region),
		TimeCreated: b.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (srv *Server) insertBucket(w http.ResponseWriter, r *http.Request) {
	var in raw.Bucket
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "parseError", "invalid JSON body: "+err.Error())
		return
	}
	if in.Name == "" {
		writeError(w, http.StatusBadRequest, "required", "bucket name is required")
		return
	}
	region := strings.ToLower(in.Location)
	if err := srv.s.CreateBucket(r.Context(), in.Name, region); err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &raw.Bucket{
		Kind:        "storage#bucket",
		Id:          in.Name,
		Name:        in.Name,
		Location:    strings.ToUpper(in.Location),
		TimeCreated: time.Now().UTC().Format(time.RFC3339),
	})
}

func (srv *Server) deleteBucket(w http.ResponseWriter, r *http.Request, bucket string) {
	if err := srv.s.DeleteBucket(r.Context(), bucket); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getBucketStorageLayout returns a default storage layout for a
// bucket. `gcloud storage cp` queries this endpoint on every copy;
// a 404 here triggers a Python bug in gcloud (a TypeError on
// endswith) that aborts the transfer. Returning the canonical
// "default-state" payload lets the copy proceed.
func (srv *Server) getBucketStorageLayout(w http.ResponseWriter, r *http.Request, bucket string) {
	if _, err := srv.s.HeadBucket(r.Context(), bucket); err != nil {
		mapDomainError(w, err)
		return
	}
	resp := map[string]interface{}{
		"kind":         "storage#storageLayout",
		"bucket":       bucket,
		"location":     "US",
		"locationType": "multi-region",
		"customPlacementConfig": map[string]interface{}{
			"dataLocations": []string{},
		},
		"hierarchicalNamespace": map[string]interface{}{
			"enabled": false,
		},
	}
	writeJSON(w, http.StatusOK, &resp)
}

// ----------------------------------------------------------------------
// Objects
// ----------------------------------------------------------------------

func (srv *Server) listObjects(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	opt := domain.ListObjectsOptions{
		Bucket:    bucket,
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		NextToken: q.Get("pageToken"),
	}
	if mr := q.Get("maxResults"); mr != "" {
		if n, err := strconv.Atoi(mr); err == nil {
			opt.MaxResults = n
		}
	}
	out, err := srv.s.ListObjects(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := raw.Objects{
		Kind:          "storage#objects",
		NextPageToken: out.NextToken,
		Prefixes:      out.CommonPrefixes,
	}
	for _, o := range out.Objects {
		o := o
		resp.Items = append(resp.Items, &raw.Object{
			Kind:    "storage#object",
			Bucket:  bucket,
			Name:    o.Key,
			Size:    uint64(o.Size),
			Etag:    strings.Trim(o.ETag, "\""),
			Updated: o.LastModified.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, &resp)
}

func (srv *Server) getObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	alt := r.URL.Query().Get("alt")
	if alt == "media" {
		srv.getObjectMedia(w, r, bucket, object)
		return
	}
	obj, err := srv.s.HeadObject(r.Context(), bucket, object)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, objectMetadataResponse(bucket, object, obj))
}

func (srv *Server) getObjectMedia(w http.ResponseWriter, r *http.Request, bucket, object string) {
	obj, err := srv.s.GetObject(r.Context(), bucket, object)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	defer func() { _ = obj.Body.Close() }()
	w.Header().Set("Content-Type", chooseContentType(obj.ContentType))
	if obj.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	w.Header().Set("ETag", quote(obj.ETag))
	// gcloud / GCS SDK verify the download against the
	// `x-goog-hash` response header. The header is a comma-separated
	// list of `<algo>=<value>` pairs; `md5=<base64>` is the one
	// every SDK consumes. Surface it whenever the backend's ETag is
	// a hex-MD5 (the convention for our streaming-md5 helper).
	if md5Bytes, err := hex.DecodeString(strings.Trim(obj.ETag, "\"")); err == nil && len(md5Bytes) == 16 {
		w.Header().Set("x-goog-hash", "md5="+base64.StdEncoding.EncodeToString(md5Bytes))
	}
	w.Header().Set("x-goog-storage-class", "STANDARD")
	w.Header().Set("x-goog-generation", "1")
	w.Header().Set("x-goog-metageneration", "1")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, obj.Body)
}

func (srv *Server) deleteObject(w http.ResponseWriter, r *http.Request, bucket, object string) {
	if err := srv.s.DeleteObject(r.Context(), bucket, object); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadObject handles `POST /upload/storage/v1/b/{bucket}/o` for
// uploadType=media and uploadType=multipart. Resumable uploads
// (uploadType=resumable) are not implemented; SDK clients that
// would use resumable fall back to multipart if we don't advertise
// it, so this covers the intersection.
func (srv *Server) uploadObject(w http.ResponseWriter, r *http.Request, bucket string) {
	q := r.URL.Query()
	switch q.Get("uploadType") {
	case "media":
		srv.uploadMedia(w, r, bucket, q.Get("name"))
	case "multipart":
		srv.uploadMultipart(w, r, bucket)
	case "resumable":
		// Mirror what GCS does: kick off the resumable session. We
		// don't fully implement resumable; instead we 400 with the
		// canonical reason so clients downgrade to multipart.
		writeError(w, http.StatusBadRequest, "required", "resumable uploads are not supported by this shim; use uploadType=multipart")
	default:
		writeError(w, http.StatusBadRequest, "required", "uploadType must be one of: media, multipart")
	}
}

func (srv *Server) uploadMedia(w http.ResponseWriter, r *http.Request, bucket, name string) {
	if name == "" {
		writeError(w, http.StatusBadRequest, "required", "name query parameter is required for uploadType=media")
		return
	}
	hasher := md5.New()
	counted := &countingReader{R: io.TeeReader(r.Body, hasher), H: hasher}
	opt := domain.PutObjectOptions{
		Bucket:      bucket,
		Key:         name,
		Body:        counted,
		ContentType: r.Header.Get("Content-Type"),
	}
	res, err := srv.s.PutObject(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &raw.Object{
		Kind:    "storage#object",
		Bucket:  bucket,
		Name:    name,
		Etag:    strings.Trim(res.ETag, "\""),
		Md5Hash: base64.StdEncoding.EncodeToString(hasher.Sum(nil)),
		Updated: time.Now().UTC().Format(time.RFC3339),
		Size:    uint64(counted.N), //nolint:gosec
	})
}

// uploadMultipart parses the GCS multipart-related upload format:
// two MIME parts — JSON metadata then opaque media bytes. The
// object name can come from the JSON metadata or from the `name`
// query parameter (gcloud sometimes uses the query form).
func (srv *Server) uploadMultipart(w http.ResponseWriter, r *http.Request, bucket string) {
	ct := r.Header.Get("Content-Type")
	mediaType, boundary, ok := parseMultipartContentType(ct)
	if !ok || !strings.HasPrefix(mediaType, "multipart/") {
		writeError(w, http.StatusBadRequest, "parseError", "expected multipart Content-Type, got "+ct)
		return
	}
	mr := multipart.NewReader(r.Body, boundary)
	// Part 1: JSON metadata.
	metaPart, err := mr.NextPart()
	if err != nil {
		writeError(w, http.StatusBadRequest, "parseError", "missing metadata part (boundary="+boundary+"): "+err.Error())
		return
	}
	var meta raw.Object
	if err := json.NewDecoder(metaPart).Decode(&meta); err != nil {
		_ = metaPart.Close()
		writeError(w, http.StatusBadRequest, "parseError", "invalid metadata JSON: "+err.Error())
		return
	}
	_ = metaPart.Close()
	// Object name: prefer the metadata, fall back to ?name= query
	// (gcloud uploads use the query form).
	name := meta.Name
	if name == "" {
		name = r.URL.Query().Get("name")
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "required", "object name must be provided in metadata or via ?name=")
		return
	}
	// Part 2: media bytes.
	mediaPart, err := mr.NextPart()
	if err != nil {
		writeError(w, http.StatusBadRequest, "parseError", "missing media part: "+err.Error())
		return
	}
	contentType := mediaPart.Header.Get("Content-Type")
	if contentType == "" {
		contentType = meta.ContentType
	}
	hasher := md5.New()
	counted := &countingReader{R: io.TeeReader(mediaPart, hasher), H: hasher}
	opt := domain.PutObjectOptions{
		Bucket:      bucket,
		Key:         name,
		Body:        counted,
		ContentType: contentType,
		Metadata:    meta.Metadata,
	}
	res, err := srv.s.PutObject(r.Context(), opt)
	_ = mediaPart.Close()
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, &raw.Object{
		Kind:        "storage#object",
		Bucket:      bucket,
		Name:        name,
		Etag:        strings.Trim(res.ETag, "\""),
		Md5Hash:     base64.StdEncoding.EncodeToString(hasher.Sum(nil)),
		Size:        uint64(counted.N), //nolint:gosec
		Updated:     time.Now().UTC().Format(time.RFC3339),
		ContentType: contentType,
		Metadata:    meta.Metadata,
	})
}

// ----------------------------------------------------------------------
// CopyObject / RewriteObject
// ----------------------------------------------------------------------

func (srv *Server) copyTo(w http.ResponseWriter, r *http.Request, srcBucket, srcObject, dstBucket, dstObject string) {
	srv.doCopy(w, r, srcBucket, srcObject, dstBucket, dstObject, false)
}

func (srv *Server) rewriteTo(w http.ResponseWriter, r *http.Request, srcBucket, srcObject, dstBucket, dstObject string) {
	srv.doCopy(w, r, srcBucket, srcObject, dstBucket, dstObject, true)
}

func (srv *Server) doCopy(w http.ResponseWriter, r *http.Request, srcBucket, srcObject, dstBucket, dstObject string, rewrite bool) {
	// Optional JSON body specifies replacement metadata.
	var meta raw.Object
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
			writeError(w, http.StatusBadRequest, "parseError", "invalid JSON body: "+err.Error())
			return
		}
	}
	opt := domain.CopyObjectOptions{
		SrcBucket:   srcBucket,
		SrcKey:      srcObject,
		DstBucket:   dstBucket,
		DstKey:      dstObject,
		ContentType: meta.ContentType,
	}
	if len(meta.Metadata) > 0 {
		opt.MetadataDirective = "REPLACE"
		opt.Metadata = meta.Metadata
	}
	res, err := srv.s.CopyObject(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	finalObj := &raw.Object{
		Kind:        "storage#object",
		Bucket:      dstBucket,
		Name:        dstObject,
		Etag:        strings.Trim(res.ETag, "\""),
		Updated:     res.LastModified.UTC().Format(time.RFC3339),
		ContentType: meta.ContentType,
		Metadata:    meta.Metadata,
	}
	if rewrite {
		writeJSON(w, http.StatusOK, &raw.RewriteResponse{
			Kind:                "storage#rewriteResponse",
			TotalBytesRewritten: 0, // backend doesn't track; OK to leave 0
			ObjectSize:          0,
			Done:                true,
			Resource:            finalObj,
		})
		return
	}
	writeJSON(w, http.StatusOK, finalObj)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func chooseContentType(ct string) string {
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

func quote(s string) string {
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "\"") {
		return s
	}
	return "\"" + s + "\""
}

func objectMetadataResponse(bucket, object string, obj domain.Object) *raw.Object {
	r := &raw.Object{
		Kind:           "storage#object",
		Bucket:         bucket,
		Name:           object,
		Etag:           strings.Trim(obj.ETag, "\""),
		ContentType:    obj.ContentType,
		Size:           uint64(obj.Size),
		Metadata:       obj.Metadata,
		Generation:     1,
		Metageneration: 1,
		StorageClass:   "STANDARD",
	}
	if r.ContentType == "" {
		r.ContentType = "application/octet-stream"
	}
	// If the ETag is a hex-encoded MD5 (16 bytes after hex-decoding),
	// expose it as md5Hash too — the GCS SDK + gcloud verify this
	// against the bytes they downloaded.
	if md5Bytes, err := hex.DecodeString(strings.Trim(obj.ETag, "\"")); err == nil && len(md5Bytes) == 16 {
		r.Md5Hash = base64.StdEncoding.EncodeToString(md5Bytes)
	}
	if !obj.LastModified.IsZero() {
		r.Updated = obj.LastModified.UTC().Format(time.RFC3339)
		r.TimeCreated = r.Updated
		r.TimeStorageClassUpdated = r.Updated
	}
	return r
}

// parseMultipartContentType parses a multipart Content-Type header
// and returns the media type and boundary. It tolerates the
// gcloud-style single-quoted boundary value
// ("multipart/related; boundary='==='") that mime.ParseMediaType
// rejects as an invalid quoted-string.
func parseMultipartContentType(ct string) (mediaType, boundary string, ok bool) {
	if mt, params, err := mime.ParseMediaType(ct); err == nil {
		return mt, params["boundary"], true
	}
	// Fallback: scan for `boundary=` manually. Strip quotes (single
	// or double) around the value. The media-type prefix is
	// everything before the first ';' or before whitespace.
	semi := strings.IndexByte(ct, ';')
	if semi < 0 {
		return strings.TrimSpace(ct), "", false
	}
	mediaType = strings.TrimSpace(ct[:semi])
	rest := ct[semi+1:]
	for _, kv := range strings.Split(rest, ";") {
		kv = strings.TrimSpace(kv)
		if !strings.HasPrefix(kv, "boundary=") {
			continue
		}
		val := strings.TrimPrefix(kv, "boundary=")
		val = strings.Trim(val, "'\"")
		return mediaType, val, val != ""
	}
	return mediaType, "", false
}

// countingReader wraps an io.Reader so the caller can read the
// total bytes seen after the consumer has drained it. Used to
// report Size + Md5Hash in the upload response without pre-reading
// the body.
type countingReader struct {
	R io.Reader
	H hash.Hash
	N int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	c.N += int64(n)
	return n, err
}

// Ensure the errors symbol is used (avoid "imported and not used"
// when the file compiles before mapDomainError takes wing).
var _ = errors.As

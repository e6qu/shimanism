package gcs

import (
	"encoding/json"
	"errors"
	"fmt"
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
	body, n, err := readWithLength(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parseError", err.Error())
		return
	}
	opt := domain.PutObjectOptions{
		Bucket:      bucket,
		Key:         name,
		Body:        body,
		ContentType: r.Header.Get("Content-Type"),
	}
	_ = n
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
		Updated: time.Now().UTC().Format(time.RFC3339),
		Size:    uint64(n), //nolint:gosec
	})
}

// uploadMultipart parses the GCS multipart-related upload format:
// two MIME parts — JSON metadata then opaque media bytes.
func (srv *Server) uploadMultipart(w http.ResponseWriter, r *http.Request, bucket string) {
	ct := r.Header.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		writeError(w, http.StatusBadRequest, "parseError", "expected multipart Content-Type, got "+ct)
		return
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	// Part 1: JSON metadata.
	metaPart, err := mr.NextPart()
	if err != nil {
		writeError(w, http.StatusBadRequest, "parseError", "missing metadata part: "+err.Error())
		return
	}
	var meta raw.Object
	if err := json.NewDecoder(metaPart).Decode(&meta); err != nil {
		_ = metaPart.Close()
		writeError(w, http.StatusBadRequest, "parseError", "invalid metadata JSON: "+err.Error())
		return
	}
	_ = metaPart.Close()
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
	opt := domain.PutObjectOptions{
		Bucket:      bucket,
		Key:         meta.Name,
		Body:        mediaPart,
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
		Name:        meta.Name,
		Etag:        strings.Trim(res.ETag, "\""),
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
		Kind:        "storage#object",
		Bucket:      bucket,
		Name:        object,
		Etag:        strings.Trim(obj.ETag, "\""),
		ContentType: obj.ContentType,
		Size:        uint64(obj.Size),
		Metadata:    obj.Metadata,
	}
	if !obj.LastModified.IsZero() {
		r.Updated = obj.LastModified.UTC().Format(time.RFC3339)
	}
	return r
}

// readWithLength returns the request body and the declared length.
// Falls back to chunked read when Content-Length is missing.
func readWithLength(r *http.Request) (io.Reader, int64, error) {
	n := r.ContentLength
	if n > 0 {
		return r.Body, n, nil
	}
	// No declared length — read fully so backends that need a size
	// can compute one. The intersection ops we care about all carry
	// Content-Length, but defensiveness is cheap.
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}
	return strings.NewReader(string(buf)), int64(len(buf)), nil
}

// Ensure the errors symbol is used (avoid "imported and not used"
// when the file compiles before mapDomainError takes wing).
var _ = errors.As

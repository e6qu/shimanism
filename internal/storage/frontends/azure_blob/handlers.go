package azure_blob

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// ----------------------------------------------------------------------
// Wire types matching the Azure Blob REST API responses.
//
// These mirror the shapes the official `azure-sdk-for-go` internal
// `generated/` package uses to decode. Each `xml` struct tag is the
// element name the Azure SDK expects.
// ----------------------------------------------------------------------

// quoteETag wraps a backend-supplied ETag value in the quoted form
// real Azure Blob Storage emits ("0x8DAAEFF…"). Empty stays empty.
// The function is idempotent: backends that return already-quoted
// values round-trip through Trim+wrap unchanged.
func quoteETag(raw string) string {
	s := strings.Trim(raw, "\"")
	if s == "" {
		return ""
	}
	return `"` + s + `"`
}

type containerXML struct {
	Name       string `xml:"Name"`
	Properties struct {
		LastModified string `xml:"Last-Modified"`
		ETag         string `xml:"Etag"`
	} `xml:"Properties"`
}

type listContainersResponse struct {
	XMLName    xml.Name        `xml:"EnumerationResults"`
	Prefix     string          `xml:"Prefix,omitempty"`
	Containers containersGroup `xml:"Containers"`
	NextMarker string          `xml:"NextMarker"`
}

type containersGroup struct {
	Containers []containerXML `xml:"Container"`
}

type blobXML struct {
	Name       string         `xml:"Name"`
	Properties blobProperties `xml:"Properties"`
}

type blobProperties struct {
	LastModified  string `xml:"Last-Modified"`
	ETag          string `xml:"Etag"`
	ContentLength int64  `xml:"Content-Length"`
	ContentType   string `xml:"Content-Type,omitempty"`
	BlobType      string `xml:"BlobType"`
}

type listBlobsResponse struct {
	XMLName    xml.Name   `xml:"EnumerationResults"`
	Prefix     string     `xml:"Prefix,omitempty"`
	Delimiter  string     `xml:"Delimiter,omitempty"`
	Blobs      blobsGroup `xml:"Blobs"`
	NextMarker string     `xml:"NextMarker"`
}

type blobsGroup struct {
	Blobs      []blobXML       `xml:"Blob"`
	BlobPrefix []blobPrefixXML `xml:"BlobPrefix"`
}

type blobPrefixXML struct {
	Name string `xml:"Name"`
}

// ----------------------------------------------------------------------
// Container ops
// ----------------------------------------------------------------------

func (srv *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opt := domain.ListBucketsOptions{
		Prefix:    q.Get("prefix"),
		NextToken: q.Get("marker"),
	}
	if mr := q.Get("maxresults"); mr != "" {
		if n, err := strconv.Atoi(mr); err == nil {
			opt.MaxResults = n
		}
	}
	out, err := srv.s.ListBuckets(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := listContainersResponse{Prefix: opt.Prefix, NextMarker: out.NextToken}
	for _, b := range out.Buckets {
		c := containerXML{Name: b.Name}
		c.Properties.LastModified = b.CreatedAt.UTC().Format(http.TimeFormat)
		c.Properties.ETag = quoteETag(b.ETag)
		resp.Containers.Containers = append(resp.Containers.Containers, c)
	}
	writeXML(w, http.StatusOK, &resp)
}

func (srv *Server) createContainer(w http.ResponseWriter, r *http.Request, container string) {
	if err := srv.s.CreateBucket(r.Context(), container, ""); err != nil {
		mapDomainError(w, err)
		return
	}
	w.Header().Set("ETag", `"created"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) getContainerProperties(w http.ResponseWriter, r *http.Request, container string) {
	b, err := srv.s.HeadBucket(r.Context(), container)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	w.Header().Set("Last-Modified", b.CreatedAt.UTC().Format(http.TimeFormat))
	if etag := quoteETag(b.ETag); etag != "" {
		w.Header().Set("ETag", etag)
	}
	w.Header().Set("x-ms-lease-status", "unlocked")
	w.Header().Set("x-ms-lease-state", "available")
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) deleteContainer(w http.ResponseWriter, r *http.Request, container string) {
	if err := srv.s.DeleteBucket(r.Context(), container); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) listBlobs(w http.ResponseWriter, r *http.Request, container string) {
	q := r.URL.Query()
	opt := domain.ListObjectsOptions{
		Bucket:    container,
		Prefix:    q.Get("prefix"),
		Delimiter: q.Get("delimiter"),
		NextToken: q.Get("marker"),
	}
	if mr := q.Get("maxresults"); mr != "" {
		if n, err := strconv.Atoi(mr); err == nil {
			opt.MaxResults = n
		}
	}
	out, err := srv.s.ListObjects(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	resp := listBlobsResponse{Prefix: opt.Prefix, Delimiter: opt.Delimiter, NextMarker: out.NextToken}
	for _, o := range out.Objects {
		b := blobXML{Name: o.Key}
		b.Properties.ContentLength = o.Size
		b.Properties.ETag = quoteETag(o.ETag)
		b.Properties.LastModified = o.LastModified.UTC().Format(http.TimeFormat)
		b.Properties.BlobType = "BlockBlob"
		resp.Blobs.Blobs = append(resp.Blobs.Blobs, b)
	}
	for _, p := range out.CommonPrefixes {
		resp.Blobs.BlobPrefix = append(resp.Blobs.BlobPrefix, blobPrefixXML{Name: p})
	}
	writeXML(w, http.StatusOK, &resp)
}

// ----------------------------------------------------------------------
// Blob ops
// ----------------------------------------------------------------------

func (srv *Server) putBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	metadata := extractMetadata(r.Header)
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = r.Header.Get("x-ms-blob-content-type")
	}
	opt := domain.PutObjectOptions{
		Bucket:      container,
		Key:         blob,
		Body:        r.Body,
		ContentType: contentType,
		Metadata:    metadata,
	}
	res, err := srv.s.PutObject(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	etag := strings.Trim(res.ETag, "\"")
	if etag == "" {
		etag = "shim"
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (srv *Server) getBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	obj, err := srv.s.GetObject(r.Context(), container, blob)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	defer func() { _ = obj.Body.Close() }()

	// Range support. The Python Azure Storage SDK (used by both
	// `az storage blob download` and any other Python client)
	// unconditionally validates Content-Range on the response and
	// errors out with `ValueError: Required Content-Range response
	// header is missing or malformed.` when it isn't there — even
	// when the request didn't carry a Range header. So we always
	// emit Content-Range on a successful blob GET.
	rng := r.Header.Get("Range")
	if rng == "" {
		writeBlobHeaders(w, obj)
		if obj.Size > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", obj.Size-1, obj.Size))
		} else {
			w.Header().Set("Content-Range", "bytes 0-0/0")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, obj.Body)
		return
	}
	start, end, ok := parseSingleRange(rng, obj.Size)
	if !ok {
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "InvalidRange",
			"unparseable Range header: "+rng)
		return
	}
	// Skip the first `start` bytes then read `end-start+1` bytes.
	// io.CopyN handles short reads as EOF, which is fine here.
	if start > 0 {
		if _, err := io.CopyN(io.Discard, obj.Body, start); err != nil {
			writeError(w, http.StatusInternalServerError, "InternalError",
				"seeking to range start: "+err.Error())
			return
		}
	}
	length := end - start + 1
	writeBlobHeaders(w, obj)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, obj.Size))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.CopyN(w, obj.Body, length)
}

// parseSingleRange parses an HTTP "Range: bytes=START-END" header,
// returning the inclusive [start, end] byte range. Suffix ranges
// ("bytes=-500") and open-ended ranges ("bytes=500-") are supported.
// Multi-range requests aren't supported (Azure clients don't issue
// them).
func parseSingleRange(h string, size int64) (start, end int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(h, prefix) {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(h, prefix)
	if strings.Contains(spec, ",") {
		return 0, 0, false
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false
	}
	startS, endS := spec[:dash], spec[dash+1:]
	if startS == "" {
		// Suffix: bytes=-N → last N bytes.
		n, err := strconv.ParseInt(endS, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	s, err := strconv.ParseInt(startS, 10, 64)
	if err != nil || s < 0 || s >= size {
		return 0, 0, false
	}
	if endS == "" {
		return s, size - 1, true
	}
	e, err := strconv.ParseInt(endS, 10, 64)
	if err != nil || e < s {
		return 0, 0, false
	}
	if e >= size {
		e = size - 1
	}
	return s, e, true
}

func (srv *Server) headBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	obj, err := srv.s.HeadObject(r.Context(), container, blob)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	writeBlobHeaders(w, obj)
	w.WriteHeader(http.StatusOK)
}

func (srv *Server) deleteBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	if err := srv.s.DeleteObject(r.Context(), container, blob); err != nil {
		mapDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (srv *Server) copyBlob(w http.ResponseWriter, r *http.Request, container, blob string) {
	src := r.Header.Get("x-ms-copy-source")
	srcContainer, srcBlob, ok := parseCopySource(src)
	if !ok {
		writeError(w, http.StatusBadRequest, "InvalidInput", "malformed x-ms-copy-source: "+src)
		return
	}
	opt := domain.CopyObjectOptions{
		SrcBucket: srcContainer,
		SrcKey:    srcBlob,
		DstBucket: container,
		DstKey:    blob,
	}
	if md := extractMetadata(r.Header); len(md) > 0 {
		opt.MetadataDirective = "REPLACE"
		opt.Metadata = md
	}
	res, err := srv.s.CopyObject(r.Context(), opt)
	if err != nil {
		mapDomainError(w, err)
		return
	}
	etag := strings.Trim(res.ETag, "\"")
	if etag == "" {
		etag = "shim"
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Last-Modified", res.LastModified.UTC().Format(http.TimeFormat))
	w.Header().Set("x-ms-copy-status", "success")
	w.Header().Set("x-ms-copy-id", "shim-copy")
	w.WriteHeader(http.StatusAccepted)
}

// ----------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------

func writeXML(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	_ = enc.Encode(body)
}

func writeBlobHeaders(w http.ResponseWriter, obj domain.Object) {
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if obj.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	etag := strings.Trim(obj.ETag, "\"")
	if etag != "" {
		w.Header().Set("ETag", `"`+etag+`"`)
	}
	if !obj.LastModified.IsZero() {
		w.Header().Set("Last-Modified", obj.LastModified.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("x-ms-blob-type", "BlockBlob")
	for k, v := range obj.Metadata {
		w.Header().Set("x-ms-meta-"+k, v)
	}
}

func extractMetadata(h http.Header) map[string]string {
	var out map[string]string
	for k, vs := range h {
		const pfx = "X-Ms-Meta-"
		if !strings.HasPrefix(k, pfx) || len(vs) == 0 {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[strings.ToLower(k[len(pfx):])] = vs[0]
	}
	return out
}

// parseCopySource accepts either a URL (https://<account>.blob.core.windows.net/<container>/<blob>)
// or a relative path (/<container>/<blob>). The shim doesn't care
// about the host portion — only container + blob.
func parseCopySource(src string) (container, blob string, ok bool) {
	rest := src
	if i := strings.Index(rest, "://"); i >= 0 {
		rest = rest[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[j:]
		}
	}
	rest = strings.TrimPrefix(rest, "/")
	// Strip query string.
	if q := strings.IndexByte(rest, '?'); q >= 0 {
		rest = rest[:q]
	}
	// Strip account-name segment if present.
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		first := rest[:i]
		if isAccountSegment(first) {
			rest = rest[i+1:]
		}
	}
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	return rest[:slash], rest[slash+1:], true
}

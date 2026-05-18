package azure_blob

import (
	"encoding/xml"
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
	Blobs       []blobXML        `xml:"Blob"`
	BlobPrefix  []blobPrefixXML  `xml:"BlobPrefix"`
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
	w.Header().Set("ETag", `"shim"`)
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
		b.Properties.ETag = strings.Trim(o.ETag, "\"")
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
	writeBlobHeaders(w, obj)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, obj.Body)
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

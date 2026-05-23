package gcs

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/e6qu/shimanism/internal/storage/domain"

	_ "github.com/e6qu/shimanism/services/storage/gen/gcp" // Phase 13.B spec-drift contract; gen.gcp.Routes is the canonical route inventory.
)

var _ = strings.HasPrefix

// Server is a GCS-shaped HTTP frontend that dispatches to a
// domain.Storage backend. Mount with http.Handle / http.Serve;
// the harness or cmd/shim wires it directly.
type Server struct {
	s domain.Storage
}

// New returns a GCS frontend wired to the given storage backend.
func New(s domain.Storage) *Server { return &Server{s: s} }

// route patterns. The `(?:/storage/v1)?` prefix is optional because
// `cloud.google.com/go/storage` constructs URLs relative to the
// configured endpoint without the `/storage/v1` prefix when an
// endpoint override is set, but the bare `gcloud storage` CLI hits
// the full `/storage/v1/...` path. Both shapes route the same way.
var (
	// .../b/{bucket}/o/{src}/rewriteTo/b/{dst}/o/{dstObj}
	reRewriteTo = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/o/(.+?)/rewriteTo/b/([^/]+)/o/(.+)$`)
	// .../b/{bucket}/o/{src}/copyTo/b/{dst}/o/{dstObj}
	reCopyTo = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/o/(.+?)/copyTo/b/([^/]+)/o/(.+)$`)
	// .../b/{bucket}/o/{object}
	reBucketObject = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/o/(.+)$`)
	// .../b/{bucket}/o
	reBucketObjects = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/o/?$`)
	// .../b/{bucket}/storageLayout
	reBucketStorageLayout = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/storageLayout/?$`)
	// .../b/{bucket}
	reBucket = regexp.MustCompile(`^(?:/storage/v1)?/b/([^/]+)/?$`)
	// .../b
	reBuckets = regexp.MustCompile(`^(?:/storage/v1)?/b/?$`)
	// /upload/storage/v1/b/{bucket}/o OR /upload/b/{bucket}/o
	reUploadObjects = regexp.MustCompile(`^/upload(?:/storage/v1)?/b/([^/]+)/o/?$`)
)

// ServeHTTP routes the request. The GCS REST surface is regular
// enough that regex matching is fine; we don't have the
// (method, path, query) ambiguity that drove the AWS router's
// disambiguation layer.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Upload routes live under /upload/. Both simple and multipart
	// land here; routing branches on the `uploadType` query.
	if m := reUploadObjects.FindStringSubmatch(path); m != nil {
		if method == http.MethodPost {
			srv.uploadObject(w, r, m[1])
			return
		}
	}

	if m := reRewriteTo.FindStringSubmatch(path); m != nil {
		if method == http.MethodPost {
			srv.rewriteTo(w, r, m[1], decodeObject(m[2]), m[3], decodeObject(m[4]))
			return
		}
	}
	if m := reCopyTo.FindStringSubmatch(path); m != nil {
		if method == http.MethodPost {
			srv.copyTo(w, r, m[1], decodeObject(m[2]), m[3], decodeObject(m[4]))
			return
		}
	}
	if m := reBucketObject.FindStringSubmatch(path); m != nil {
		bucket, obj := m[1], decodeObject(m[2])
		switch method {
		case http.MethodGet:
			srv.getObject(w, r, bucket, obj)
		case http.MethodDelete:
			srv.deleteObject(w, r, bucket, obj)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on object")
		}
		return
	}
	if reBucketObjects.MatchString(path) {
		m := reBucketObjects.FindStringSubmatch(path)
		switch method {
		case http.MethodGet:
			srv.listObjects(w, r, m[1])
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on objects")
		}
		return
	}
	if m := reBucketStorageLayout.FindStringSubmatch(path); m != nil {
		if method == http.MethodGet {
			srv.getBucketStorageLayout(w, r, m[1])
			return
		}
		writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on storageLayout")
		return
	}
	if m := reBucket.FindStringSubmatch(path); m != nil {
		bucket := m[1]
		switch method {
		case http.MethodGet:
			srv.getBucket(w, r, bucket)
		case http.MethodDelete:
			srv.deleteBucket(w, r, bucket)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on bucket")
		}
		return
	}
	if reBuckets.MatchString(path) {
		switch method {
		case http.MethodGet:
			srv.listBuckets(w, r)
		case http.MethodPost:
			srv.insertBucket(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on buckets")
		}
		return
	}

	// Fallback: media download at /{bucket}/{object}. The GCS SDK
	// uses this "XML API"-style URL for object reads when an
	// endpoint override is configured. The first path segment is
	// the bucket; everything after is the (potentially slash-bearing)
	// object name. Unlike `/storage/v1/b/{bucket}/o/{object}`, the
	// bare path always serves bytes — no `alt=media` switch.
	if method == http.MethodGet {
		if bucket, object, ok := splitMediaPath(path); ok {
			srv.getObjectMedia(w, r, bucket, object)
			return
		}
	}

	writeError(w, http.StatusNotFound, "notFound", "no GCS route matches "+method+" "+path)
}

// splitMediaPath parses `/{bucket}/{object}` into its parts. The
// path must not start with one of the reserved keywords used by
// the JSON API ({/b, /storage, /upload, /batch}) so we don't shadow
// the structured routes above.
func splitMediaPath(path string) (bucket, object string, ok bool) {
	if path == "" || path == "/" {
		return "", "", false
	}
	if path[0] != '/' {
		return "", "", false
	}
	rest := path[1:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	bucket = rest[:slash]
	object = decodeObject(rest[slash+1:])
	switch bucket {
	case "b", "storage", "upload", "batch":
		return "", "", false
	}
	return bucket, object, true
}

// decodeObject reverses GCS's URL-encoded object-name path segments.
// `foo%2Fbar.txt` → `foo/bar.txt`. The regex captures the raw
// segment; we URL-decode here.
func decodeObject(s string) string {
	// Object names in GCS REST URLs are %-encoded with `/` as %2F.
	// net/url's QueryUnescape handles + → space which we don't
	// want, so do a direct PathUnescape via a small loop.
	out := strings.Builder{}
	out.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) {
			hi := fromHex(s[i+1])
			lo := fromHex(s[i+2])
			if hi >= 0 && lo >= 0 {
				out.WriteByte(byte(hi<<4 | lo))
				i += 2
				continue
			}
		}
		out.WriteByte(c)
	}
	return out.String()
}

func fromHex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

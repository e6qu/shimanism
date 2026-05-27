package gcs

import (
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/storage/domain"

	_ "github.com/e6qu/shimanism/services/storage/gen/gcp" // Phase 14.C spec-drift contract; gen.gcp.Routes is the canonical route inventory.
)

// Server is a GCS-shaped HTTP frontend that dispatches to a
// domain.Storage backend. Mount with http.Handle / http.Serve;
// the harness or cmd/shim wires it directly.
type Server struct {
	s domain.Storage
}

// New returns a GCS frontend wired to the given storage backend.
func New(s domain.Storage) *Server { return &Server{s: s} }

// stripGCSPrefix removes the optional `/storage/v1` segment so
// downstream parsing can work with the version-neutral remainder.
// Both `gcloud storage` (full `/storage/v1/b/...`) and the Go SDK's
// endpoint-override path (bare `/b/...`) route the same way.
func stripGCSPrefix(path string) (rest string, hadV1 bool) {
	if r, ok := strings.CutPrefix(path, "/storage/v1"); ok {
		return r, true
	}
	return path, false
}

// ServeHTTP dispatches by path-shape inspection. Existing
// `TestGCPRoutes_Storage_FrontendDispatchCoverage` pins behavior.
//
// Routes covered (with optional `/storage/v1` prefix):
//
//	GET    /b
//	POST   /b
//	GET    /b/{bucket}
//	DELETE /b/{bucket}
//	GET    /b/{bucket}/storageLayout
//	GET    /b/{bucket}/managedFolders
//	GET    /b/{bucket}/o
//	GET    /b/{bucket}/o/{object}
//	DELETE /b/{bucket}/o/{object}
//	POST   /b/{src-bucket}/o/{src-obj}/rewriteTo/b/{dst-bucket}/o/{dst-obj}
//	POST   /b/{src-bucket}/o/{src-obj}/copyTo/b/{dst-bucket}/o/{dst-obj}
//	POST   /upload[/storage/v1]/b/{bucket}/o
//	GET    /download[/storage/v1]/b/{bucket}/o/{object}
//	GET    /{bucket}/{object}            (XML-style media fallback)
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method

	// Upload routes.
	if rest, ok := strings.CutPrefix(path, "/upload"); ok {
		rest, _ = stripGCSPrefix(rest)
		segs := strings.Split(strings.TrimPrefix(rest, "/"), "/")
		// segs = ["b", bucket, "o", ...]
		if len(segs) >= 3 && segs[0] == "b" && segs[2] == "o" && (len(segs) == 3 || (len(segs) == 4 && segs[3] == "")) {
			if method == http.MethodPost {
				srv.uploadObject(w, r, segs[1])
				return
			}
		}
		writeError(w, http.StatusNotFound, "notFound", "no GCS route matches "+method+" "+path)
		return
	}

	// Download routes.
	if rest, ok := strings.CutPrefix(path, "/download"); ok {
		rest, _ = stripGCSPrefix(rest)
		// rest = "/b/{bucket}/o/{object…}"
		if bucket, obj, ok := splitBucketObject(rest); ok {
			if method == http.MethodGet {
				srv.getObjectMedia(w, r, bucket, decodeObject(obj))
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on media download")
			return
		}
		writeError(w, http.StatusNotFound, "notFound", "no GCS route matches "+method+" "+path)
		return
	}

	// JSON API routes with optional /storage/v1 prefix.
	if rest, _ := stripGCSPrefix(path); strings.HasPrefix(rest, "/b") {
		// Check for inline rewriteTo / copyTo cuts first (they nest /b/...).
		if i := strings.Index(rest, "/rewriteTo/b/"); i >= 0 && method == http.MethodPost {
			if srcBucket, srcObj, ok := splitBucketObject(rest[:i]); ok {
				dstPart := rest[i+len("/rewriteTo"):]
				if dstBucket, dstObj, ok := splitBucketObject(dstPart); ok {
					srv.rewriteTo(w, r, srcBucket, decodeObject(srcObj), dstBucket, decodeObject(dstObj))
					return
				}
			}
		}
		if i := strings.Index(rest, "/copyTo/b/"); i >= 0 && method == http.MethodPost {
			if srcBucket, srcObj, ok := splitBucketObject(rest[:i]); ok {
				dstPart := rest[i+len("/copyTo"):]
				if dstBucket, dstObj, ok := splitBucketObject(dstPart); ok {
					srv.copyTo(w, r, srcBucket, decodeObject(srcObj), dstBucket, decodeObject(dstObj))
					return
				}
			}
		}
		// Strip leading "/" so segs starts at "b".
		segs := strings.Split(strings.TrimPrefix(rest, "/"), "/")
		// segs starts with "b". Possible shapes:
		//   ["b"]                          /b
		//   ["b", ""]                      /b/
		//   ["b", bucket]                  /b/{bucket}
		//   ["b", bucket, ""]              /b/{bucket}/
		//   ["b", bucket, "storageLayout"] /b/{bucket}/storageLayout
		//   ["b", bucket, "managedFolders"]/b/{bucket}/managedFolders
		//   ["b", bucket, "o"]             /b/{bucket}/o
		//   ["b", bucket, "o", obj...]     /b/{bucket}/o/{object}
		switch {
		case len(segs) == 1, len(segs) == 2 && segs[1] == "":
			switch method {
			case http.MethodGet:
				srv.listBuckets(w, r)
			case http.MethodPost:
				srv.insertBucket(w, r)
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on buckets")
			}
			return
		case len(segs) == 2 || (len(segs) == 3 && segs[2] == ""):
			bucket := segs[1]
			switch method {
			case http.MethodGet:
				srv.getBucket(w, r, bucket)
			case http.MethodDelete:
				srv.deleteBucket(w, r, bucket)
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on bucket")
			}
			return
		case len(segs) == 3 && segs[2] == "storageLayout", len(segs) == 4 && segs[2] == "storageLayout" && segs[3] == "":
			if method == http.MethodGet {
				srv.getBucketStorageLayout(w, r, segs[1])
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on storageLayout")
			return
		case len(segs) == 3 && segs[2] == "managedFolders", len(segs) == 4 && segs[2] == "managedFolders" && segs[3] == "":
			if method == http.MethodGet {
				srv.listManagedFolders(w, r, segs[1])
				return
			}
			writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on managedFolders")
			return
		case len(segs) == 3 && segs[2] == "o", len(segs) == 4 && segs[2] == "o" && segs[3] == "":
			switch method {
			case http.MethodGet:
				srv.listObjects(w, r, segs[1])
			default:
				writeError(w, http.StatusMethodNotAllowed, "methodNotAllowed", method+" not allowed on objects")
			}
			return
		case len(segs) >= 4 && segs[2] == "o":
			bucket := segs[1]
			obj := decodeObject(strings.Join(segs[3:], "/"))
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

// splitBucketObject parses `/b/{bucket}/o/{object…}` (object may
// contain slashes) into the bucket name and the raw escaped object
// path. Returns ok=false if the shape doesn't match.
func splitBucketObject(rest string) (bucket, obj string, ok bool) {
	rest = strings.TrimPrefix(rest, "/")
	if !strings.HasPrefix(rest, "b/") {
		return "", "", false
	}
	rest = rest[len("b/"):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "", "", false
	}
	bucket = rest[:slash]
	rest = rest[slash+1:]
	if !strings.HasPrefix(rest, "o/") {
		return "", "", false
	}
	obj = rest[len("o/"):]
	if obj == "" {
		return "", "", false
	}
	return bucket, obj, true
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

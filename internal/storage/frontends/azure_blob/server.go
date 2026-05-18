package azure_blob

import (
	"net/http"
	"strings"

	"github.com/e6qu/shimanism/internal/storage/domain"
)

// Server is an Azure-Blob-shaped HTTP frontend that dispatches to a
// domain.Storage backend. Routing follows Azure's `?restype=` +
// `?comp=` query convention plus per-method dispatch — no need for
// the AWS S3-style required-headers/required-queries matrix because
// Azure's URL grammar disambiguates operations explicitly via these
// query params.
type Server struct {
	s domain.Storage
}

// New returns an Azure Blob frontend wired to the given backend.
func New(s domain.Storage) *Server { return &Server{s: s} }

// ServeHTTP routes the request. Azure routes are:
//
//	/                        — account-level (ListContainers when ?comp=list)
//	/{account}/              — same, with explicit account prefix (path-style override)
//	/{container}             — container ops (PUT create / GET props / DELETE delete) with ?restype=container
//	/{container}?comp=list   — list blobs with ?restype=container&comp=list
//	/{container}/{blob}      — blob ops (GET / HEAD / PUT / DELETE)
//
// When the Azure SDK is given an endpoint override pointing at the
// shim, it constructs URLs with the storage-account name as the
// first path segment: `/devstoreaccount1/container/blob`. We accept
// both shapes — with and without the account prefix.
func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	// Strip a leading "account" segment if present. The shim is
	// account-agnostic; the account name is purely a routing hint
	// for Azure's host-style URLs, not a state-of-record concept
	// for our backends.
	if i := strings.IndexByte(path, '/'); i >= 0 {
		first := path[:i]
		// Heuristic: an "account" segment never contains `=` or `.`
		// and never matches reserved keywords. The rest of the path
		// must start with a container name or be empty.
		if isAccountSegment(first) {
			path = path[i+1:]
		}
	} else if isAccountSegment(path) {
		// Just /{account} → list-containers at the account.
		path = ""
	}
	q := r.URL.Query()
	restype := q.Get("restype")
	comp := q.Get("comp")
	method := r.Method

	switch {
	case path == "" && method == http.MethodGet && comp == "list":
		srv.listContainers(w, r)
		return
	case path == "" && method == http.MethodGet:
		// Bare GET on root with no comp= falls back to listing.
		srv.listContainers(w, r)
		return
	}

	// Split container/blob.
	slash := strings.IndexByte(path, '/')
	if slash < 0 {
		// /{container}
		container := path
		switch {
		case method == http.MethodPut && restype == "container":
			srv.createContainer(w, r, container)
		case method == http.MethodGet && restype == "container" && comp == "list":
			srv.listBlobs(w, r, container)
		case method == http.MethodGet && restype == "container":
			srv.getContainerProperties(w, r, container)
		case method == http.MethodHead && restype == "container":
			srv.getContainerProperties(w, r, container)
		case method == http.MethodDelete && restype == "container":
			srv.deleteContainer(w, r, container)
		default:
			writeError(w, http.StatusBadRequest, "InvalidInput",
				"unrecognised container-level request: "+method+" /"+container+"?"+r.URL.RawQuery)
		}
		return
	}

	container := path[:slash]
	blob := path[slash+1:]
	switch method {
	case http.MethodPut:
		if r.Header.Get("x-ms-copy-source") != "" {
			srv.copyBlob(w, r, container, blob)
			return
		}
		srv.putBlob(w, r, container, blob)
	case http.MethodGet:
		srv.getBlob(w, r, container, blob)
	case http.MethodHead:
		srv.headBlob(w, r, container, blob)
	case http.MethodDelete:
		srv.deleteBlob(w, r, container, blob)
	default:
		writeError(w, http.StatusMethodNotAllowed, "InvalidInput", method+" not allowed on blob")
	}
}

// isAccountSegment reports whether a path segment looks like an
// Azure storage-account name (lowercase letters + digits, between
// 3 and 24 chars). Conservative — any segment that doesn't match
// is treated as a container.
func isAccountSegment(s string) bool {
	if len(s) < 3 || len(s) > 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

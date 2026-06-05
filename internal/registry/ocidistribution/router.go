// Package ocidistribution is shimanism's hand-written OCI Distribution
// Spec v1 (/v2/) data-plane router. It is the registry analog of
// internal/ec2query: a shared protocol runtime that each cloud frontend
// (ECR / Artifact Registry / ACR) mounts at /v2/ behind its own auth
// middleware. The protocol is byte-identical across clouds by spec; only
// repository-name mapping and the Location-header base differ, which an
// Adapter supplies.
//
// The router translates the OCI HTTP surface onto domain.Registry's
// streaming methods. Bodies stream through (io.Reader end-to-end) — the
// router never buffers a whole layer — and the sha256 digest is verified
// in-flight on upload (N34). Upload-session state lives in the backend;
// the router only round-trips the backend's session ID through the
// Location header, so the shim holds no cross-request state.
package ocidistribution

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

// Adapter supplies the per-frontend bindings the otherwise-identical
// protocol core needs. 18.A ships the default (standard OCI parsing);
// cloud frontends provide their own in later sub-phases (e.g. Artifact
// Registry's project path prefix).
type Adapter interface {
	// RepoName maps the parsed path repository name onto the backend
	// repository name. The default returns it unchanged.
	RepoName(parsed string) string
}

// defaultAdapter is the identity binding.
type defaultAdapter struct{}

func (defaultAdapter) RepoName(parsed string) string { return parsed }

// Router serves the OCI /v2/ API against a domain.Registry.
type Router struct {
	reg     domain.Registry
	adapter Adapter
}

// New returns a router with the default adapter.
func New(reg domain.Registry) *Router { return &Router{reg: reg, adapter: defaultAdapter{}} }

// NewWithAdapter returns a router with a per-frontend adapter.
func NewWithAdapter(reg domain.Registry, a Adapter) *Router {
	return &Router{reg: reg, adapter: a}
}

const apiVersionHeader = "Docker-Distribution-API-Version"
const apiVersionValue = "registry/2.0"

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v2/")
	if rest == r.URL.Path { // path did not start with /v2/
		writeOCIError(w, http.StatusNotFound, codeNameUnknown, "not a /v2/ path")
		return
	}
	w.Header().Set(apiVersionHeader, apiVersionValue)

	// Base / version check: GET /v2/ (rest == "" or "/").
	if rest == "" || rest == "/" {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{}"))
			return
		}
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed on /v2/")
		return
	}

	name, kind, arg, ok := parsePath(rest)
	if !ok {
		writeOCIError(w, http.StatusNotFound, codeNameUnknown, "unrecognized /v2/ path: "+r.URL.Path)
		return
	}
	repo := rt.adapter.RepoName(name)

	switch kind {
	case kindBlobUploadStart:
		rt.startUpload(w, r, repo)
	case kindBlobUpload:
		rt.continueUpload(w, r, repo, arg)
	case kindBlob:
		rt.blob(w, r, repo, arg)
	case kindManifest:
		rt.manifest(w, r, repo, arg)
	case kindTagsList:
		rt.tagsList(w, r, repo)
	default:
		writeOCIError(w, http.StatusNotFound, codeNameUnknown, "unrecognized /v2/ path")
	}
}

// ─── blob upload ─────────────────────────────────────────────────────

// POST /v2/{name}/blobs/uploads/[?digest=]
func (rt *Router) startUpload(w http.ResponseWriter, r *http.Request, repo string) {
	if r.Method != http.MethodPost {
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed")
		return
	}
	sess, err := rt.reg.StartBlobUpload(r.Context(), repo)
	if err != nil {
		writeDomainError(w, err, codeNameUnknown)
		return
	}
	// Single-POST monolithic upload: digest present on the POST itself.
	if dg := r.URL.Query().Get("digest"); dg != "" {
		rt.finalize(w, r, repo, sess, dg)
		return
	}
	rt.writeUploadAccepted(w, repo, sess, http.StatusAccepted)
}

// PATCH (chunk) or PUT (finalize) /v2/{name}/blobs/uploads/{id}
func (rt *Router) continueUpload(w http.ResponseWriter, r *http.Request, repo, id string) {
	sess := domain.UploadSession{ID: id}
	switch r.Method {
	case http.MethodPatch:
		updated, err := rt.reg.UploadChunk(r.Context(), repo, sess, r.Body)
		if err != nil {
			writeDomainError(w, err, codeBlobUploadInval)
			return
		}
		rt.writeUploadAccepted(w, repo, updated, http.StatusAccepted)
	case http.MethodPut:
		dg := r.URL.Query().Get("digest")
		if dg == "" {
			writeOCIError(w, http.StatusBadRequest, codeDigestInvalid, "PUT requires a digest query parameter")
			return
		}
		rt.finalize(w, r, repo, sess, dg)
	default:
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed")
	}
}

func (rt *Router) finalize(w http.ResponseWriter, r *http.Request, repo string, sess domain.UploadSession, digest string) {
	desc, err := rt.reg.CompleteBlobUpload(r.Context(), repo, sess, digest, r.Body)
	if err != nil {
		writeDomainError(w, err, codeBlobUploadInval)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/%s", repo, desc.Digest))
	w.Header().Set("Docker-Content-Digest", desc.Digest)
	w.WriteHeader(http.StatusCreated)
}

// writeUploadAccepted emits the 202 in-progress-upload response with the
// Location (next request target) and Range (bytes committed) headers.
func (rt *Router) writeUploadAccepted(w http.ResponseWriter, repo string, sess domain.UploadSession, status int) {
	w.Header().Set("Location", fmt.Sprintf("/v2/%s/blobs/uploads/%s", repo, sess.ID))
	w.Header().Set("Docker-Upload-UUID", sess.ID)
	if sess.Offset > 0 {
		w.Header().Set("Range", fmt.Sprintf("0-%d", sess.Offset-1))
	} else {
		w.Header().Set("Range", "0-0")
	}
	w.WriteHeader(status)
}

// ─── blob get / head ─────────────────────────────────────────────────

// HEAD/GET /v2/{name}/blobs/{digest}
func (rt *Router) blob(w http.ResponseWriter, r *http.Request, repo, digest string) {
	switch r.Method {
	case http.MethodHead:
		desc, err := rt.reg.BlobExists(r.Context(), repo, digest)
		if err != nil {
			writeDomainError(w, err, codeBlobUnknown)
			return
		}
		w.Header().Set("Docker-Content-Digest", desc.Digest)
		w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		rc, desc, err := rt.reg.GetBlob(r.Context(), repo, digest)
		if err != nil {
			writeDomainError(w, err, codeBlobUnknown)
			return
		}
		defer rc.Close()
		w.Header().Set("Docker-Content-Digest", desc.Digest)
		w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	default:
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed")
	}
}

// ─── manifest ────────────────────────────────────────────────────────

// PUT/GET/HEAD/DELETE /v2/{name}/manifests/{reference}
func (rt *Router) manifest(w http.ResponseWriter, r *http.Request, repo, ref string) {
	switch r.Method {
	case http.MethodPut:
		mediaType := r.Header.Get("Content-Type")
		desc, err := rt.reg.PutManifest(r.Context(), repo, ref, mediaType, r.Body)
		if err != nil {
			writeDomainError(w, err, codeManifestUnknown)
			return
		}
		w.Header().Set("Docker-Content-Digest", desc.Digest)
		w.Header().Set("Location", fmt.Sprintf("/v2/%s/manifests/%s", repo, desc.Digest))
		w.WriteHeader(http.StatusCreated)
	case http.MethodGet:
		rc, desc, err := rt.reg.GetManifest(r.Context(), repo, ref)
		if err != nil {
			writeDomainError(w, err, codeManifestUnknown)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", desc.MediaType)
		w.Header().Set("Docker-Content-Digest", desc.Digest)
		w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, rc)
	case http.MethodHead:
		desc, err := rt.reg.HeadManifest(r.Context(), repo, ref)
		if err != nil {
			writeDomainError(w, err, codeManifestUnknown)
			return
		}
		w.Header().Set("Content-Type", desc.MediaType)
		w.Header().Set("Docker-Content-Digest", desc.Digest)
		w.Header().Set("Content-Length", strconv.FormatInt(desc.Size, 10))
		w.WriteHeader(http.StatusOK)
	case http.MethodDelete:
		if err := rt.reg.DeleteManifest(r.Context(), repo, ref); err != nil {
			writeDomainError(w, err, codeManifestUnknown)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	default:
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed")
	}
}

// ─── tags ────────────────────────────────────────────────────────────

// GET /v2/{name}/tags/list
func (rt *Router) tagsList(w http.ResponseWriter, r *http.Request, repo string) {
	if r.Method != http.MethodGet {
		writeOCIError(w, http.StatusMethodNotAllowed, codeUnsupported, r.Method+" not allowed")
		return
	}
	tags, err := rt.reg.ListTags(r.Context(), repo, domain.ListOptions{})
	if err != nil {
		writeDomainError(w, err, codeNameUnknown)
		return
	}
	if tags == nil {
		tags = []string{}
	}
	writeJSON(w, http.StatusOK, tagsListResponse{Name: repo, Tags: tags})
}

type tagsListResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

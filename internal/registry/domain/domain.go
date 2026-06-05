// Package domain holds shimanism's neutral container-registry interface
// and types — the lingua franca between three frontend protocols (AWS
// ECR, GCP Artifact Registry, Azure ACR) and four backends (the three
// clouds + the CNCF distribution K8s peer).
//
// Phase 18 has two protocol planes in one service:
//
//   - Control plane — repository lifecycle plus image list/delete.
//     Per-cloud RPC shapes (ECR awsJson1_1, AR Discovery REST, ACR ARM),
//     codegen'd.
//   - Data plane — the OCI Distribution Spec v1 /v2/ API (blob, manifest,
//     and tag operations). One shared hand-written router, served behind
//     every frontend.
//
// The shim is stateless: blobs and manifests never live in the shim.
// Data-plane methods take and return io.Reader / io.ReadCloser so layers
// stream through the shim without buffering — the shim verifies the
// sha256 digest in-flight (per-request scratch) and forwards the bytes to
// the backend, which holds the content. Content-addressable storage needs
// no name->location table in the shim, and chunked-upload session state
// lives in the backend (the frontend rewrites the Location header so the
// next PATCH lands on the same backend session).
package domain

import (
	"context"
	"errors"
	"io"
	"time"
)

// Sentinel errors. Frontends map these to each cloud's error vocabulary
// (and to the OCI error envelope on the /v2/ data plane); backends
// produce them from their registry's native error codes.
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInvalidInput  = errors.New("invalid input")
	ErrNotSupported  = errors.New("operation not supported")
	// ErrDigestMismatch is returned when an uploaded blob's computed
	// sha256 does not match the digest the client claimed.
	ErrDigestMismatch = errors.New("digest mismatch")
)

func IsNotFound(err error) bool       { return errors.Is(err, ErrNotFound) }
func IsAlreadyExists(err error) bool  { return errors.Is(err, ErrAlreadyExists) }
func IsInvalidInput(err error) bool   { return errors.Is(err, ErrInvalidInput) }
func IsNotSupported(err error) bool   { return errors.Is(err, ErrNotSupported) }
func IsDigestMismatch(err error) bool { return errors.Is(err, ErrDigestMismatch) }

// Repository is a named image repository within a registry. Tags here are
// cloud resource tags (metadata), not image tags.
type Repository struct {
	Name      string // e.g. "team/app"
	CreatedAt time.Time
	Tags      map[string]string
}

// Image is one manifest in a repository, addressed by its digest and
// pointed at by zero or more image tags.
type Image struct {
	Digest    string   // "sha256:..."
	Tags      []string // image tags resolving to this manifest
	MediaType string   // OCI/Docker manifest media type, verbatim (N32)
	Size      int64
	PushedAt  time.Time
}

// Descriptor is a content-addressable handle to a blob or manifest.
type Descriptor struct {
	Digest    string // "sha256:..."
	MediaType string
	Size      int64
}

// UploadSession identifies an in-progress chunked blob upload. The
// session lives in the backend; ID is the backend's own upload handle and
// Offset is the number of bytes committed so far. The frontend maps this
// onto the OCI Location header it returns to the client.
type UploadSession struct {
	ID     string
	Offset int64
}

// CreateRepoOptions carries inputs for CreateRepository.
type CreateRepoOptions struct {
	Tags map[string]string
}

// ListOptions is shared pagination input for the list operations.
type ListOptions struct {
	PageSize  int
	PageToken string
}

// ListReposResult is the result of ListRepositories.
type ListReposResult struct {
	Repositories  []Repository
	NextPageToken string
}

// ListImagesResult is the result of ListImages.
type ListImagesResult struct {
	Images        []Image
	NextPageToken string
}

// Registry is the neutral container-registry interface. The control-plane
// methods translate per-cloud RPC shapes; the data-plane methods serve
// the shared OCI Distribution /v2/ router.
type Registry interface {
	// ── Control plane ──

	// CreateRepository creates an (empty) repository. On registries where
	// repositories are implicit (Azure ACR — see N30) this is an
	// idempotent metadata no-op that succeeds; the repository materializes
	// on first push.
	CreateRepository(ctx context.Context, name string, opt CreateRepoOptions) (Repository, error)
	// DeleteRepository removes a repository and all its content. force
	// permits deleting a non-empty repository.
	DeleteRepository(ctx context.Context, name string, force bool) error
	DescribeRepository(ctx context.Context, name string) (Repository, error)
	ListRepositories(ctx context.Context, opt ListOptions) (ListReposResult, error)
	// ListImages lists the manifests in a repository.
	ListImages(ctx context.Context, repo string, opt ListOptions) (ListImagesResult, error)
	// DeleteImage deletes a manifest by tag or digest reference.
	DeleteImage(ctx context.Context, repo, reference string) error

	// ── OCI data plane (streaming, content-addressed) ──

	// BlobExists reports whether a blob with the given digest is present
	// (HEAD /v2/{repo}/blobs/{digest}).
	BlobExists(ctx context.Context, repo, digest string) (Descriptor, error)
	// GetBlob streams a blob's content (GET /v2/{repo}/blobs/{digest}).
	// The caller must Close the returned reader.
	GetBlob(ctx context.Context, repo, digest string) (io.ReadCloser, Descriptor, error)

	// StartBlobUpload opens an upload session
	// (POST /v2/{repo}/blobs/uploads/).
	StartBlobUpload(ctx context.Context, repo string) (UploadSession, error)
	// UploadChunk appends a chunk to an upload session
	// (PATCH /v2/{repo}/blobs/uploads/{id}). It returns the updated
	// session (with the new committed Offset).
	UploadChunk(ctx context.Context, repo string, sess UploadSession, r io.Reader) (UploadSession, error)
	// CompleteBlobUpload finalizes an upload (PUT
	// /v2/{repo}/blobs/uploads/{id}?digest=...). r carries any trailing
	// bytes (for a monolithic PUT, the whole blob). The backend verifies
	// the assembled content against digest and returns its descriptor;
	// ErrDigestMismatch on mismatch.
	CompleteBlobUpload(ctx context.Context, repo string, sess UploadSession, digest string, r io.Reader) (Descriptor, error)

	// PutManifest stores a manifest under a tag or digest reference
	// (PUT /v2/{repo}/manifests/{reference}). mediaType is stored verbatim
	// (N32). Returns the manifest's descriptor (digest computed by the
	// backend).
	PutManifest(ctx context.Context, repo, reference, mediaType string, r io.Reader) (Descriptor, error)
	// GetManifest streams a manifest by tag or digest reference.
	GetManifest(ctx context.Context, repo, reference string) (io.ReadCloser, Descriptor, error)
	// HeadManifest returns a manifest's descriptor without its body.
	HeadManifest(ctx context.Context, repo, reference string) (Descriptor, error)
	// DeleteManifest deletes a manifest by tag or digest reference.
	DeleteManifest(ctx context.Context, repo, reference string) error
	// ListTags lists the image tags in a repository
	// (GET /v2/{repo}/tags/list).
	ListTags(ctx context.Context, repo string, opt ListOptions) ([]string, error)
}

// Package inmem is an in-memory container-registry backend for tests. It
// is a real backend, not a fake: it is a genuine content-addressable
// store — blobs and manifests are keyed by their real sha256 digest,
// CompleteBlobUpload verifies the assembled content against the claimed
// digest, and chunked-upload session state is held here (the backend),
// never in the shim. It is the test backend-of-record the conformance
// suite runs against when no connected registry is configured.
//
// Buffering bytes in memory is correct here: this backend *is* the
// storage. The statelessness rule constrains the shim, not the backend.
package inmem

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

type storedManifest struct {
	mediaType string
	content   []byte
	digest    string
	pushedAt  time.Time
}

type repoEntry struct {
	name        string
	createdAt   time.Time
	tags        map[string]string         // cloud resource tags
	blobs       map[string][]byte         // digest -> content
	manifests   map[string]storedManifest // digest -> manifest
	tagToDigest map[string]string         // image tag -> manifest digest
}

func newRepo(name string, now time.Time, tags map[string]string) *repoEntry {
	return &repoEntry{
		name: name, createdAt: now, tags: tags,
		blobs: map[string][]byte{}, manifests: map[string]storedManifest{},
		tagToDigest: map[string]string{},
	}
}

type uploadSession struct {
	repo string
	buf  []byte
}

// Backend implements domain.Registry entirely in memory.
type Backend struct {
	mu      sync.RWMutex
	repos   map[string]*repoEntry
	uploads map[string]*uploadSession
	seq     int
	now     func() time.Time
}

// New returns an empty in-memory registry backend.
func New() *Backend {
	return &Backend{repos: map[string]*repoEntry{}, uploads: map[string]*uploadSession{}, now: time.Now}
}

var _ domain.Registry = (*Backend)(nil)

func sha256Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func isDigestRef(ref string) bool { return bytes.ContainsRune([]byte(ref), ':') }

// repoOrCreate returns the repo, creating it if absent (OCI base
// behavior: a push to a fresh repository materializes it). Caller holds
// the write lock.
func (b *Backend) repoOrCreate(name string) *repoEntry {
	r, ok := b.repos[name]
	if !ok {
		r = newRepo(name, b.now().UTC(), nil)
		b.repos[name] = r
	}
	return r
}

// ── Control plane ──

func (b *Backend) CreateRepository(_ context.Context, name string, opt domain.CreateRepoOptions) (domain.Repository, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.repos[name]; ok {
		return domain.Repository{}, fmt.Errorf("repository %q: %w", name, domain.ErrAlreadyExists)
	}
	r := newRepo(name, b.now().UTC(), copyTags(opt.Tags))
	b.repos[name] = r
	return domain.Repository{Name: name, CreatedAt: r.createdAt, Tags: copyTags(r.tags)}, nil
}

func (b *Backend) DeleteRepository(_ context.Context, name string, force bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.repos[name]
	if !ok {
		return fmt.Errorf("repository %q: %w", name, domain.ErrNotFound)
	}
	if !force && len(r.manifests) > 0 {
		return fmt.Errorf("repository %q not empty (use force): %w", name, domain.ErrInvalidInput)
	}
	delete(b.repos, name)
	return nil
}

func (b *Backend) DescribeRepository(_ context.Context, name string) (domain.Repository, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.repos[name]
	if !ok {
		return domain.Repository{}, fmt.Errorf("repository %q: %w", name, domain.ErrNotFound)
	}
	return domain.Repository{Name: name, CreatedAt: r.createdAt, Tags: copyTags(r.tags)}, nil
}

func (b *Backend) ListRepositories(_ context.Context, _ domain.ListOptions) (domain.ListReposResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	names := make([]string, 0, len(b.repos))
	for n := range b.repos {
		names = append(names, n)
	}
	sort.Strings(names)
	out := domain.ListReposResult{}
	for _, n := range names {
		r := b.repos[n]
		out.Repositories = append(out.Repositories, domain.Repository{Name: n, CreatedAt: r.createdAt, Tags: copyTags(r.tags)})
	}
	return out, nil
}

func (b *Backend) ListImages(_ context.Context, repo string, _ domain.ListOptions) (domain.ListImagesResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.repos[repo]
	if !ok {
		return domain.ListImagesResult{}, fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	// digest -> tags
	tagsByDigest := map[string][]string{}
	for tag, dg := range r.tagToDigest {
		tagsByDigest[dg] = append(tagsByDigest[dg], tag)
	}
	digests := make([]string, 0, len(r.manifests))
	for dg := range r.manifests {
		digests = append(digests, dg)
	}
	sort.Strings(digests)
	out := domain.ListImagesResult{}
	for _, dg := range digests {
		m := r.manifests[dg]
		tags := tagsByDigest[dg]
		sort.Strings(tags)
		out.Images = append(out.Images, domain.Image{
			Digest: dg, Tags: tags, MediaType: m.mediaType, Size: int64(len(m.content)), PushedAt: m.pushedAt,
		})
	}
	return out, nil
}

func (b *Backend) DeleteImage(_ context.Context, repo, reference string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.repos[repo]
	if !ok {
		return fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	dg, ok := r.resolve(reference)
	if !ok {
		return fmt.Errorf("manifest %q: %w", reference, domain.ErrNotFound)
	}
	r.deleteManifest(dg)
	return nil
}

// ── OCI data plane ──

func (b *Backend) BlobExists(_ context.Context, repo, digest string) (domain.Descriptor, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.repos[repo]
	if !ok {
		return domain.Descriptor{}, fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	content, ok := r.blobs[digest]
	if !ok {
		return domain.Descriptor{}, fmt.Errorf("blob %q: %w", digest, domain.ErrNotFound)
	}
	return domain.Descriptor{Digest: digest, Size: int64(len(content))}, nil
}

func (b *Backend) GetBlob(_ context.Context, repo, digest string) (io.ReadCloser, domain.Descriptor, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.repos[repo]
	if !ok {
		return nil, domain.Descriptor{}, fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	content, ok := r.blobs[digest]
	if !ok {
		return nil, domain.Descriptor{}, fmt.Errorf("blob %q: %w", digest, domain.ErrNotFound)
	}
	cp := make([]byte, len(content))
	copy(cp, content)
	return io.NopCloser(bytes.NewReader(cp)), domain.Descriptor{Digest: digest, Size: int64(len(cp))}, nil
}

func (b *Backend) StartBlobUpload(_ context.Context, repo string) (domain.UploadSession, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.repoOrCreate(repo)
	b.seq++
	id := fmt.Sprintf("upload-%08d", b.seq)
	b.uploads[id] = &uploadSession{repo: repo}
	return domain.UploadSession{ID: id, Offset: 0}, nil
}

func (b *Backend) UploadChunk(_ context.Context, repo string, sess domain.UploadSession, r io.Reader) (domain.UploadSession, error) {
	chunk, err := io.ReadAll(r)
	if err != nil {
		return domain.UploadSession{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	u, ok := b.uploads[sess.ID]
	if !ok || u.repo != repo {
		return domain.UploadSession{}, fmt.Errorf("upload %q: %w", sess.ID, domain.ErrNotFound)
	}
	u.buf = append(u.buf, chunk...)
	return domain.UploadSession{ID: sess.ID, Offset: int64(len(u.buf))}, nil
}

func (b *Backend) CompleteBlobUpload(_ context.Context, repo string, sess domain.UploadSession, digest string, r io.Reader) (domain.Descriptor, error) {
	rest, err := io.ReadAll(r)
	if err != nil {
		return domain.Descriptor{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	u, ok := b.uploads[sess.ID]
	if !ok || u.repo != repo {
		return domain.Descriptor{}, fmt.Errorf("upload %q: %w", sess.ID, domain.ErrNotFound)
	}
	content := append(u.buf, rest...)
	if got := sha256Digest(content); got != digest {
		return domain.Descriptor{}, fmt.Errorf("computed %s, claimed %s: %w", got, digest, domain.ErrDigestMismatch)
	}
	r2 := b.repoOrCreate(repo)
	r2.blobs[digest] = content
	delete(b.uploads, sess.ID)
	return domain.Descriptor{Digest: digest, Size: int64(len(content))}, nil
}

func (b *Backend) PutManifest(_ context.Context, repo, reference, mediaType string, r io.Reader) (domain.Descriptor, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return domain.Descriptor{}, err
	}
	digest := sha256Digest(content)
	b.mu.Lock()
	defer b.mu.Unlock()
	rp := b.repoOrCreate(repo)
	rp.manifests[digest] = storedManifest{mediaType: mediaType, content: content, digest: digest, pushedAt: b.now().UTC()}
	if !isDigestRef(reference) {
		rp.tagToDigest[reference] = digest // tag push
	}
	return domain.Descriptor{Digest: digest, MediaType: mediaType, Size: int64(len(content))}, nil
}

func (b *Backend) GetManifest(_ context.Context, repo, reference string) (io.ReadCloser, domain.Descriptor, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	m, desc, err := b.lookupManifest(repo, reference)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	cp := make([]byte, len(m.content))
	copy(cp, m.content)
	return io.NopCloser(bytes.NewReader(cp)), desc, nil
}

func (b *Backend) HeadManifest(_ context.Context, repo, reference string) (domain.Descriptor, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, desc, err := b.lookupManifest(repo, reference)
	return desc, err
}

func (b *Backend) DeleteManifest(_ context.Context, repo, reference string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.repos[repo]
	if !ok {
		return fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	dg, ok := r.resolve(reference)
	if !ok {
		return fmt.Errorf("manifest %q: %w", reference, domain.ErrNotFound)
	}
	r.deleteManifest(dg)
	return nil
}

func (b *Backend) ListTags(_ context.Context, repo string, _ domain.ListOptions) ([]string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	r, ok := b.repos[repo]
	if !ok {
		return nil, fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	tags := make([]string, 0, len(r.tagToDigest))
	for t := range r.tagToDigest {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags, nil
}

// lookupManifest resolves a tag/digest reference to its stored manifest.
// Caller holds at least the read lock.
func (b *Backend) lookupManifest(repo, reference string) (storedManifest, domain.Descriptor, error) {
	r, ok := b.repos[repo]
	if !ok {
		return storedManifest{}, domain.Descriptor{}, fmt.Errorf("repository %q: %w", repo, domain.ErrNotFound)
	}
	dg, ok := r.resolve(reference)
	if !ok {
		return storedManifest{}, domain.Descriptor{}, fmt.Errorf("manifest %q: %w", reference, domain.ErrNotFound)
	}
	m := r.manifests[dg]
	return m, domain.Descriptor{Digest: dg, MediaType: m.mediaType, Size: int64(len(m.content))}, nil
}

// resolve maps a tag or digest reference to a manifest digest.
func (r *repoEntry) resolve(reference string) (string, bool) {
	if isDigestRef(reference) {
		if _, ok := r.manifests[reference]; ok {
			return reference, true
		}
		return "", false
	}
	dg, ok := r.tagToDigest[reference]
	return dg, ok
}

// deleteManifest removes a manifest and any tags pointing at it.
func (r *repoEntry) deleteManifest(digest string) {
	delete(r.manifests, digest)
	for tag, dg := range r.tagToDigest {
		if dg == digest {
			delete(r.tagToDigest, tag)
		}
	}
}

func copyTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

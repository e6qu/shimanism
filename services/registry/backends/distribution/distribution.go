// Package distribution implements domain.Registry against a real CNCF
// Distribution-compatible registry endpoint. The backend keeps no
// shim-owned repository or upload state: repository answers come from the
// upstream /v2/ catalog/tag/manifest APIs, and upload sessions round-trip
// the upstream upload Location as the domain session ID.
package distribution

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

const (
	manifestAccept = "application/vnd.oci.image.manifest.v1+json, " +
		"application/vnd.docker.distribution.manifest.v2+json, " +
		"application/vnd.docker.distribution.manifest.list.v2+json, " +
		"application/vnd.oci.image.index.v1+json"
)

// Options configures a connected Distribution backend.
type Options struct {
	BaseURL       string
	Client        *http.Client
	Username      string
	Password      string
	RequestEditor func(context.Context, *http.Request) error
}

// Backend is a real HTTP client for a Distribution /v2/ registry.
type Backend struct {
	base     *url.URL
	client   *http.Client
	username string
	password string
	editReq  func(context.Context, *http.Request) error
}

var _ domain.Registry = (*Backend)(nil)

// New returns a backend connected to baseURL, for example
// "http://127.0.0.1:5000".
func New(baseURL string) (*Backend, error) {
	return NewWithOptions(Options{BaseURL: baseURL})
}

// NewWithOptions returns a backend connected to a Distribution registry.
func NewWithOptions(opt Options) (*Backend, error) {
	if opt.BaseURL == "" {
		return nil, fmt.Errorf("distribution base URL is required: %w", domain.ErrInvalidInput)
	}
	u, err := url.Parse(opt.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid distribution base URL %q: %w", opt.BaseURL, domain.ErrInvalidInput)
	}
	c := opt.Client
	if c == nil {
		c = http.DefaultClient
	}
	return &Backend{base: u, client: c, username: opt.Username, password: opt.Password, editReq: opt.RequestEditor}, nil
}

// CreateRepository cannot be implemented honestly against Distribution:
// repositories materialize only when content is pushed.
func (b *Backend) CreateRepository(_ context.Context, name string, _ domain.CreateRepoOptions) (domain.Repository, error) {
	return domain.Repository{}, fmt.Errorf("distribution cannot create empty repository %q: %w", name, domain.ErrNotSupported)
}

func (b *Backend) DeleteRepository(ctx context.Context, name string, force bool) error {
	images, err := b.ListImages(ctx, name, domain.ListOptions{})
	if err != nil {
		return err
	}
	if len(images.Images) > 0 && !force {
		return fmt.Errorf("repository %q not empty (use force): %w", name, domain.ErrInvalidInput)
	}
	for _, img := range images.Images {
		if err := b.DeleteManifest(ctx, name, img.Digest); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) DescribeRepository(ctx context.Context, name string) (domain.Repository, error) {
	tags, err := b.ListTags(ctx, name, domain.ListOptions{PageSize: 1})
	if err != nil {
		return domain.Repository{}, err
	}
	if len(tags) == 0 {
		return domain.Repository{}, fmt.Errorf("repository %q has no visible tags in distribution: %w", name, domain.ErrNotFound)
	}
	return domain.Repository{Name: name}, nil
}

func (b *Backend) ListRepositories(ctx context.Context, opt domain.ListOptions) (domain.ListReposResult, error) {
	q := url.Values{}
	if opt.PageSize > 0 {
		q.Set("n", strconv.Itoa(opt.PageSize))
	}
	if opt.PageToken != "" {
		q.Set("last", opt.PageToken)
	}
	req, err := b.newRequest(ctx, http.MethodGet, b.urlFor("_catalog", q), nil)
	if err != nil {
		return domain.ListReposResult{}, err
	}
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return domain.ListReposResult{}, err
	}
	defer resp.Body.Close()

	var body catalogResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return domain.ListReposResult{}, err
	}
	out := domain.ListReposResult{NextPageToken: nextToken(resp.Header.Get("Link"))}
	for _, name := range body.Repositories {
		out.Repositories = append(out.Repositories, domain.Repository{Name: name})
	}
	return out, nil
}

func (b *Backend) ListImages(ctx context.Context, repo string, opt domain.ListOptions) (domain.ListImagesResult, error) {
	tags, err := b.ListTags(ctx, repo, opt)
	if err != nil {
		return domain.ListImagesResult{}, err
	}
	byDigest := map[string]*domain.Image{}
	for _, tag := range tags {
		desc, err := b.HeadManifest(ctx, repo, tag)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		img := byDigest[desc.Digest]
		if img == nil {
			img = &domain.Image{Digest: desc.Digest, MediaType: desc.MediaType, Size: desc.Size}
			byDigest[desc.Digest] = img
		}
		img.Tags = append(img.Tags, tag)
	}
	digests := make([]string, 0, len(byDigest))
	for dg := range byDigest {
		digests = append(digests, dg)
	}
	sort.Strings(digests)
	out := domain.ListImagesResult{}
	for _, dg := range digests {
		img := *byDigest[dg]
		sort.Strings(img.Tags)
		out.Images = append(out.Images, img)
	}
	return out, nil
}

func (b *Backend) DeleteImage(ctx context.Context, repo, reference string) error {
	return b.DeleteManifest(ctx, repo, reference)
}

func (b *Backend) BlobExists(ctx context.Context, repo, digest string) (domain.Descriptor, error) {
	req, err := b.newRequest(ctx, http.MethodHead, b.repoURL(repo, "blobs", digest, nil), nil)
	if err != nil {
		return domain.Descriptor{}, err
	}
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return domain.Descriptor{}, err
	}
	resp.Body.Close()
	return blobDescriptor(resp, digest), nil
}

func (b *Backend) GetBlob(ctx context.Context, repo, digest string) (io.ReadCloser, domain.Descriptor, error) {
	req, err := b.newRequest(ctx, http.MethodGet, b.repoURL(repo, "blobs", digest, nil), nil)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	return resp.Body, blobDescriptor(resp, digest), nil
}

func (b *Backend) StartBlobUpload(ctx context.Context, repo string) (domain.UploadSession, error) {
	req, err := b.newRequest(ctx, http.MethodPost, b.urlFor(escapeRepo(repo)+"/blobs/uploads/", nil), nil)
	if err != nil {
		return domain.UploadSession{}, err
	}
	resp, err := b.do(req, http.StatusAccepted)
	if err != nil {
		return domain.UploadSession{}, err
	}
	defer resp.Body.Close()
	location, err := resolvedLocation(resp)
	if err != nil {
		return domain.UploadSession{}, err
	}
	return domain.UploadSession{ID: encodeLocation(location), Offset: uploadOffset(resp)}, nil
}

func (b *Backend) UploadChunk(ctx context.Context, repo string, sess domain.UploadSession, r io.Reader) (domain.UploadSession, error) {
	location, err := decodeLocation(sess.ID)
	if err != nil {
		return domain.UploadSession{}, err
	}
	req, err := b.newRequest(ctx, http.MethodPatch, location, r)
	if err != nil {
		return domain.UploadSession{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := b.do(req, http.StatusAccepted)
	if err != nil {
		return domain.UploadSession{}, err
	}
	defer resp.Body.Close()
	next, err := resolvedLocation(resp)
	if err != nil {
		return domain.UploadSession{}, err
	}
	return domain.UploadSession{ID: encodeLocation(next), Offset: uploadOffset(resp)}, nil
}

func (b *Backend) CompleteBlobUpload(ctx context.Context, repo string, sess domain.UploadSession, digest string, r io.Reader) (domain.Descriptor, error) {
	location, err := decodeLocation(sess.ID)
	if err != nil {
		return domain.Descriptor{}, err
	}
	u, err := url.Parse(location)
	if err != nil {
		return domain.Descriptor{}, fmt.Errorf("invalid distribution upload location: %w", err)
	}
	q := u.Query()
	q.Set("digest", digest)
	u.RawQuery = q.Encode()
	req, err := b.newRequest(ctx, http.MethodPut, u.String(), r)
	if err != nil {
		return domain.Descriptor{}, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := b.do(req, http.StatusCreated)
	if err != nil {
		return domain.Descriptor{}, err
	}
	defer resp.Body.Close()
	got := resp.Header.Get("Docker-Content-Digest")
	if got == "" {
		got = digest
	}
	return domain.Descriptor{Digest: got, Size: contentLength(resp)}, nil
}

func (b *Backend) PutManifest(ctx context.Context, repo, reference, mediaType string, r io.Reader) (domain.Descriptor, error) {
	req, err := b.newRequest(ctx, http.MethodPut, b.repoURL(repo, "manifests", reference, nil), r)
	if err != nil {
		return domain.Descriptor{}, err
	}
	if mediaType != "" {
		req.Header.Set("Content-Type", mediaType)
	}
	resp, err := b.do(req, http.StatusCreated)
	if err != nil {
		return domain.Descriptor{}, err
	}
	defer resp.Body.Close()
	digest := resp.Header.Get("Docker-Content-Digest")
	return domain.Descriptor{Digest: digest, MediaType: mediaType, Size: contentLength(resp)}, nil
}

func (b *Backend) GetManifest(ctx context.Context, repo, reference string) (io.ReadCloser, domain.Descriptor, error) {
	req, err := b.newRequest(ctx, http.MethodGet, b.repoURL(repo, "manifests", reference, nil), nil)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	return resp.Body, manifestDescriptor(resp), nil
}

func (b *Backend) HeadManifest(ctx context.Context, repo, reference string) (domain.Descriptor, error) {
	req, err := b.newRequest(ctx, http.MethodHead, b.repoURL(repo, "manifests", reference, nil), nil)
	if err != nil {
		return domain.Descriptor{}, err
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return domain.Descriptor{}, err
	}
	resp.Body.Close()
	return manifestDescriptor(resp), nil
}

func (b *Backend) DeleteManifest(ctx context.Context, repo, reference string) error {
	digest := reference
	if !strings.Contains(reference, ":") {
		desc, err := b.HeadManifest(ctx, repo, reference)
		if err != nil {
			return err
		}
		digest = desc.Digest
	}
	req, err := b.newRequest(ctx, http.MethodDelete, b.repoURL(repo, "manifests", digest, nil), nil)
	if err != nil {
		return err
	}
	resp, err := b.do(req, http.StatusAccepted)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (b *Backend) ListTags(ctx context.Context, repo string, opt domain.ListOptions) ([]string, error) {
	q := url.Values{}
	if opt.PageSize > 0 {
		q.Set("n", strconv.Itoa(opt.PageSize))
	}
	if opt.PageToken != "" {
		q.Set("last", opt.PageToken)
	}
	req, err := b.newRequest(ctx, http.MethodGet, b.repoURL(repo, "tags/list", "", q), nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.do(req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	sort.Strings(body.Tags)
	return body.Tags, nil
}

func (b *Backend) newRequest(ctx context.Context, method, rawurl string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawurl, body)
	if err != nil {
		return nil, err
	}
	if b.username != "" || b.password != "" {
		req.SetBasicAuth(b.username, b.password)
	}
	if b.editReq != nil {
		if err := b.editReq(ctx, req); err != nil {
			return nil, err
		}
	}
	return req, nil
}

func (b *Backend) do(req *http.Request, want int) (*http.Response, error) {
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == want {
		return resp, nil
	}
	err = mapError(resp)
	resp.Body.Close()
	return nil, err
}

func (b *Backend) urlFor(rest string, q url.Values) string {
	u := *b.base
	basePath := strings.TrimRight(u.EscapedPath(), "/")
	rawPath := basePath + "/v2/" + strings.TrimLeft(rest, "/")
	u.RawPath = rawPath
	u.Path, _ = url.PathUnescape(rawPath)
	u.RawQuery = q.Encode()
	return u.String()
}

func (b *Backend) repoURL(repo, kind, arg string, q url.Values) string {
	parts := []string{escapeRepo(repo)}
	if kind != "" {
		parts = append(parts, kind)
	}
	if arg != "" {
		parts = append(parts, url.PathEscape(arg))
	}
	return b.urlFor(strings.Join(parts, "/"), q)
}

func escapeRepo(repo string) string {
	segments := strings.Split(strings.Trim(repo, "/"), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

type tagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type errorEnvelope struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func mapError(resp *http.Response) error {
	msg := resp.Status
	code := ""
	var env errorEnvelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&env); err == nil && len(env.Errors) > 0 {
		code = env.Errors[0].Code
		if env.Errors[0].Message != "" {
			msg = env.Errors[0].Message
		}
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("distribution %s: %w", msg, domain.ErrNotFound)
	case http.StatusBadRequest:
		if code == "DIGEST_INVALID" {
			return fmt.Errorf("distribution %s: %w", msg, domain.ErrDigestMismatch)
		}
		return fmt.Errorf("distribution %s: %w", msg, domain.ErrInvalidInput)
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return fmt.Errorf("distribution %s: %w", msg, domain.ErrNotSupported)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("distribution %s: %w", msg, domain.ErrInvalidInput)
	default:
		if code == "NAME_UNKNOWN" || code == "BLOB_UNKNOWN" || code == "MANIFEST_UNKNOWN" {
			return fmt.Errorf("distribution %s: %w", msg, domain.ErrNotFound)
		}
		return fmt.Errorf("distribution %s", msg)
	}
}

func encodeLocation(location string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(location))
}

func decodeLocation(id string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return "", fmt.Errorf("invalid distribution upload session: %w", domain.ErrInvalidInput)
	}
	location := string(raw)
	if location == "" {
		return "", fmt.Errorf("empty distribution upload session: %w", domain.ErrInvalidInput)
	}
	return location, nil
}

func resolvedLocation(resp *http.Response) (string, error) {
	location, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("distribution response missing valid Location: %w", domain.ErrInvalidInput)
	}
	return location.String(), nil
}

func uploadOffset(resp *http.Response) int64 {
	rng := resp.Header.Get("Range")
	_, tail, ok := strings.Cut(rng, "-")
	if !ok {
		return 0
	}
	last, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || last < 0 {
		return 0
	}
	return last + 1
}

func blobDescriptor(resp *http.Response, fallbackDigest string) domain.Descriptor {
	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		digest = fallbackDigest
	}
	return domain.Descriptor{Digest: digest, Size: contentLength(resp)}
}

func manifestDescriptor(resp *http.Response) domain.Descriptor {
	return domain.Descriptor{
		Digest:    resp.Header.Get("Docker-Content-Digest"),
		MediaType: mediaType(resp.Header.Get("Content-Type")),
		Size:      contentLength(resp),
	}
}

func contentLength(resp *http.Response) int64 {
	if resp.ContentLength >= 0 {
		return resp.ContentLength
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func mediaType(v string) string {
	mt, _, _ := strings.Cut(v, ";")
	return strings.TrimSpace(mt)
}

func nextToken(link string) string {
	for _, part := range strings.Split(link, ",") {
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		u, err := url.Parse(part[start+1 : end])
		if err == nil {
			return u.Query().Get("last")
		}
	}
	return ""
}

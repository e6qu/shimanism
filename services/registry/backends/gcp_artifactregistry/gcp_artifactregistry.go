// Package gcp_artifactregistry implements domain.Registry against real
// Google Artifact Registry.
package gcp_artifactregistry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/oauth2"
	arraw "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/googleapi"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/services/registry/backends/distribution"
)

// Config configures the real Artifact Registry backend.
type Config struct {
	// Parent is the resource parent used by ListRepositories, for example
	// projects/my-project/locations/us-central1.
	Parent string
	// DataPlaneBaseURL is the Docker host, for example
	// https://us-docker.pkg.dev.
	DataPlaneBaseURL string
	// TokenSource supplies OAuth2 access tokens for the Docker /v2/ data plane.
	TokenSource oauth2.TokenSource
	// HTTPClient is used for the Docker /v2/ data plane.
	HTTPClient *http.Client
}

// Backend is a connected Google Artifact Registry backend.
type Backend struct {
	svc *arraw.Service
	cfg Config
}

var _ domain.Registry = (*Backend)(nil)

// New returns a backend using the provided Artifact Registry REST service.
func New(svc *arraw.Service, cfg Config) (*Backend, error) {
	if svc == nil {
		return nil, fmt.Errorf("artifactregistry service is required: %w", domain.ErrInvalidInput)
	}
	return &Backend{svc: svc, cfg: cfg}, nil
}

func (b *Backend) CreateRepository(ctx context.Context, name string, opt domain.CreateRepoOptions) (domain.Repository, error) {
	parent, id, err := parseRepositoryName(name)
	if err != nil {
		return domain.Repository{}, err
	}
	op, err := b.svc.Projects.Locations.Repositories.Create(parent, &arraw.Repository{
		Format: "DOCKER",
		Labels: opt.Tags,
	}).RepositoryId(id).Context(ctx).Do()
	if err != nil {
		return domain.Repository{}, wrap("CreateRepository", err)
	}
	if err := b.wait(ctx, op); err != nil {
		return domain.Repository{}, err
	}
	return b.DescribeRepository(ctx, name)
}

func (b *Backend) DeleteRepository(ctx context.Context, name string, force bool) error {
	if !force {
		images, err := b.ListImages(ctx, name, domain.ListOptions{PageSize: 1})
		if err != nil {
			return err
		}
		if len(images.Images) > 0 {
			return fmt.Errorf("artifactregistry repository %q is not empty: %w", name, domain.ErrInvalidInput)
		}
	}
	op, err := b.svc.Projects.Locations.Repositories.Delete(name).Context(ctx).Do()
	if err != nil {
		return wrap("DeleteRepository", err)
	}
	return b.wait(ctx, op)
}

func (b *Backend) DescribeRepository(ctx context.Context, name string) (domain.Repository, error) {
	repo, err := b.svc.Projects.Locations.Repositories.Get(name).Context(ctx).Do()
	if err != nil {
		return domain.Repository{}, wrap("DescribeRepository", err)
	}
	return repository(repo)
}

func (b *Backend) ListRepositories(ctx context.Context, opt domain.ListOptions) (domain.ListReposResult, error) {
	if b.cfg.Parent == "" {
		return domain.ListReposResult{}, fmt.Errorf("artifactregistry list parent is required: %w", domain.ErrInvalidInput)
	}
	call := b.svc.Projects.Locations.Repositories.List(b.cfg.Parent).Context(ctx)
	if opt.PageSize > 0 {
		call.PageSize(int64(opt.PageSize))
	}
	if opt.PageToken != "" {
		call.PageToken(opt.PageToken)
	}
	res, err := call.Do()
	if err != nil {
		return domain.ListReposResult{}, wrap("ListRepositories", err)
	}
	repos := make([]domain.Repository, 0, len(res.Repositories))
	for _, r := range res.Repositories {
		repo, err := repository(r)
		if err != nil {
			return domain.ListReposResult{}, err
		}
		repos = append(repos, repo)
	}
	return domain.ListReposResult{Repositories: repos, NextPageToken: res.NextPageToken}, nil
}

func (b *Backend) ListImages(ctx context.Context, repo string, opt domain.ListOptions) (domain.ListImagesResult, error) {
	if !strings.HasPrefix(repo, "projects/") {
		d, err := b.data(ctx)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		tags, err := d.ListTags(ctx, repo, opt)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		return imagesFromTags(ctx, d, repo, tags)
	}
	call := b.svc.Projects.Locations.Repositories.DockerImages.List(repo).Context(ctx)
	if opt.PageSize > 0 {
		call.PageSize(int64(opt.PageSize))
	}
	if opt.PageToken != "" {
		call.PageToken(opt.PageToken)
	}
	res, err := call.Do()
	if err != nil {
		return domain.ListImagesResult{}, wrap("ListImages", err)
	}
	images := make([]domain.Image, 0, len(res.DockerImages))
	for _, d := range res.DockerImages {
		img, err := dockerImage(d)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		images = append(images, img)
	}
	return domain.ListImagesResult{Images: images, NextPageToken: res.NextPageToken}, nil
}

func (b *Backend) DeleteImage(ctx context.Context, repo, reference string) error {
	return b.DeleteManifest(ctx, repo, reference)
}

func (b *Backend) BlobExists(ctx context.Context, repo, digest string) (domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.Descriptor{}, err
	}
	return d.BlobExists(ctx, repo, digest)
}

func (b *Backend) GetBlob(ctx context.Context, repo, digest string) (io.ReadCloser, domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	return d.GetBlob(ctx, repo, digest)
}

func (b *Backend) StartBlobUpload(ctx context.Context, repo string) (domain.UploadSession, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.UploadSession{}, err
	}
	return d.StartBlobUpload(ctx, repo)
}

func (b *Backend) UploadChunk(ctx context.Context, repo string, sess domain.UploadSession, r io.Reader) (domain.UploadSession, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.UploadSession{}, err
	}
	return d.UploadChunk(ctx, repo, sess, r)
}

func (b *Backend) CompleteBlobUpload(ctx context.Context, repo string, sess domain.UploadSession, digest string, r io.Reader) (domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.Descriptor{}, err
	}
	return d.CompleteBlobUpload(ctx, repo, sess, digest, r)
}

func (b *Backend) PutManifest(ctx context.Context, repo, reference, mediaType string, r io.Reader) (domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.Descriptor{}, err
	}
	return d.PutManifest(ctx, repo, reference, mediaType, r)
}

func (b *Backend) GetManifest(ctx context.Context, repo, reference string) (io.ReadCloser, domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return nil, domain.Descriptor{}, err
	}
	return d.GetManifest(ctx, repo, reference)
}

func (b *Backend) HeadManifest(ctx context.Context, repo, reference string) (domain.Descriptor, error) {
	d, err := b.data(ctx)
	if err != nil {
		return domain.Descriptor{}, err
	}
	return d.HeadManifest(ctx, repo, reference)
}

func (b *Backend) DeleteManifest(ctx context.Context, repo, reference string) error {
	d, err := b.data(ctx)
	if err != nil {
		return err
	}
	return d.DeleteManifest(ctx, repo, reference)
}

func (b *Backend) ListTags(ctx context.Context, repo string, opt domain.ListOptions) ([]string, error) {
	d, err := b.data(ctx)
	if err != nil {
		return nil, err
	}
	return d.ListTags(ctx, repo, opt)
}

func (b *Backend) data(ctx context.Context) (*distribution.Backend, error) {
	if b.cfg.DataPlaneBaseURL == "" {
		return nil, fmt.Errorf("artifactregistry data-plane base URL is required: %w", domain.ErrInvalidInput)
	}
	if b.cfg.TokenSource == nil {
		return nil, fmt.Errorf("artifactregistry data-plane token source is required: %w", domain.ErrInvalidInput)
	}
	return distribution.NewWithOptions(distribution.Options{
		BaseURL: b.cfg.DataPlaneBaseURL,
		Client:  b.cfg.HTTPClient,
		RequestEditor: func(ctx context.Context, req *http.Request) error {
			tok, err := b.cfg.TokenSource.Token()
			if err != nil {
				return err
			}
			if tok.AccessToken == "" {
				return fmt.Errorf("artifactregistry token source returned empty access token: %w", domain.ErrInvalidInput)
			}
			req.Header.Set("Authorization", tok.Type()+" "+tok.AccessToken)
			return nil
		},
	})
}

func (b *Backend) wait(ctx context.Context, op *arraw.Operation) error {
	if op == nil {
		return fmt.Errorf("artifactregistry operation response is empty: %w", domain.ErrInvalidInput)
	}
	for {
		if op.Done {
			if op.Error != nil {
				return fmt.Errorf("artifactregistry operation %q failed: %s: %w", op.Name, op.Error.Message, domain.ErrInvalidInput)
			}
			return nil
		}
		if op.Name == "" {
			return fmt.Errorf("artifactregistry operation name is empty: %w", domain.ErrInvalidInput)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
		next, err := b.svc.Projects.Locations.Operations.Get(op.Name).Context(ctx).Do()
		if err != nil {
			return wrap("GetOperation", err)
		}
		op = next
	}
}

func parseRepositoryName(name string) (string, string, error) {
	i := strings.LastIndex(name, "/repositories/")
	if i < 0 || i == 0 || i+len("/repositories/") >= len(name) {
		return "", "", fmt.Errorf("artifactregistry repository name must be projects/{project}/locations/{location}/repositories/{id}: %w", domain.ErrInvalidInput)
	}
	parent := name[:i]
	id := name[i+len("/repositories/"):]
	if strings.Contains(id, "/") {
		return "", "", fmt.Errorf("artifactregistry repository id %q contains a slash: %w", id, domain.ErrInvalidInput)
	}
	return parent, id, nil
}

func repository(r *arraw.Repository) (domain.Repository, error) {
	if r == nil || r.Name == "" {
		return domain.Repository{}, fmt.Errorf("artifactregistry repository response missing name: %w", domain.ErrInvalidInput)
	}
	repo := domain.Repository{Name: r.Name, Tags: r.Labels}
	if r.CreateTime != "" {
		t, err := time.Parse(time.RFC3339, r.CreateTime)
		if err != nil {
			return domain.Repository{}, fmt.Errorf("artifactregistry createTime %q: %w", r.CreateTime, domain.ErrInvalidInput)
		}
		repo.CreatedAt = t
	}
	return repo, nil
}

func dockerImage(d *arraw.DockerImage) (domain.Image, error) {
	if d == nil {
		return domain.Image{}, fmt.Errorf("artifactregistry docker image response is empty: %w", domain.ErrInvalidInput)
	}
	digest := digestFromImage(d)
	if digest == "" {
		return domain.Image{}, fmt.Errorf("artifactregistry docker image %q missing digest: %w", d.Name, domain.ErrInvalidInput)
	}
	img := domain.Image{
		Digest:    digest,
		Tags:      append([]string(nil), d.Tags...),
		MediaType: d.MediaType,
		Size:      d.ImageSizeBytes,
	}
	sort.Strings(img.Tags)
	if d.UploadTime != "" {
		t, err := time.Parse(time.RFC3339, d.UploadTime)
		if err != nil {
			return domain.Image{}, fmt.Errorf("artifactregistry uploadTime %q: %w", d.UploadTime, domain.ErrInvalidInput)
		}
		img.PushedAt = t
	}
	return img, nil
}

func digestFromImage(d *arraw.DockerImage) string {
	for _, s := range []string{d.Uri, d.Name} {
		if _, digest, ok := strings.Cut(s, "@"); ok && digest != "" {
			return digest
		}
	}
	return ""
}

func imagesFromTags(ctx context.Context, d *distribution.Backend, repo string, tags []string) (domain.ListImagesResult, error) {
	byDigest := map[string]domain.Image{}
	for _, tag := range tags {
		desc, err := d.HeadManifest(ctx, repo, tag)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		img := byDigest[desc.Digest]
		img.Digest = desc.Digest
		img.MediaType = desc.MediaType
		img.Size = desc.Size
		img.Tags = append(img.Tags, tag)
		byDigest[desc.Digest] = img
	}
	images := make([]domain.Image, 0, len(byDigest))
	for _, img := range byDigest {
		sort.Strings(img.Tags)
		images = append(images, img)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Digest < images[j].Digest })
	return domain.ListImagesResult{Images: images}, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("artifactregistry %s: %w", op, mapError(err))
}

func mapError(err error) error {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return err
	}
	switch gerr.Code {
	case http.StatusNotFound:
		return fmt.Errorf("%s: %w", gerr.Message, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("%s: %w", gerr.Message, domain.ErrAlreadyExists)
	case http.StatusBadRequest:
		return fmt.Errorf("%s: %w", gerr.Message, domain.ErrInvalidInput)
	case http.StatusNotImplemented, http.StatusMethodNotAllowed:
		return fmt.Errorf("%s: %w", gerr.Message, domain.ErrNotSupported)
	default:
		return err
	}
}

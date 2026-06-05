// Package azure_acr implements domain.Registry against real Azure
// Container Registry.
package azure_acr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/services/registry/backends/distribution"
)

const defaultAADScope = "https://management.azure.com/.default"

// Config configures the real ACR backend.
type Config struct {
	BaseURL      string
	Service      string
	Credential   azcore.TokenCredential
	AADScopes    []string
	RefreshToken string
	HTTPClient   *http.Client
}

// Backend is a connected Azure Container Registry backend.
type Backend struct {
	base    *url.URL
	service string
	client  *http.Client
	cfg     Config
}

var _ domain.Registry = (*Backend)(nil)

// New returns a backend connected to a real ACR endpoint.
func New(cfg Config) (*Backend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("acr base URL is required: %w", domain.ErrInvalidInput)
	}
	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid acr base URL %q: %w", cfg.BaseURL, domain.ErrInvalidInput)
	}
	c := cfg.HTTPClient
	if c == nil {
		c = http.DefaultClient
	}
	service := cfg.Service
	if service == "" {
		service = u.Host
	}
	return &Backend{base: u, service: service, client: c, cfg: cfg}, nil
}

func (b *Backend) CreateRepository(_ context.Context, name string, _ domain.CreateRepoOptions) (domain.Repository, error) {
	return domain.Repository{}, fmt.Errorf("acr cannot create empty repository %q; repositories materialize on push: %w", name, domain.ErrNotSupported)
}

func (b *Backend) DeleteRepository(ctx context.Context, name string, force bool) error {
	if !force {
		images, err := b.ListImages(ctx, name, domain.ListOptions{PageSize: 1})
		if err != nil {
			return err
		}
		if len(images.Images) > 0 {
			return fmt.Errorf("acr repository %q is not empty: %w", name, domain.ErrInvalidInput)
		}
	}
	return b.request(ctx, http.MethodDelete, "/acr/v1/"+escapePath(name), "repository:"+name+":delete", nil, http.StatusAccepted, http.StatusOK, http.StatusNoContent)
}

func (b *Backend) DescribeRepository(ctx context.Context, name string) (domain.Repository, error) {
	var body repositoryResponse
	if err := b.request(ctx, http.MethodGet, "/acr/v1/"+escapePath(name), "repository:"+name+":pull", &body, http.StatusOK); err != nil {
		return domain.Repository{}, err
	}
	repo := domain.Repository{Name: name}
	if body.Name != "" {
		repo.Name = body.Name
	}
	if body.CreatedTime != "" {
		t, err := time.Parse(time.RFC3339, body.CreatedTime)
		if err != nil {
			return domain.Repository{}, fmt.Errorf("acr createdTime %q: %w", body.CreatedTime, domain.ErrInvalidInput)
		}
		repo.CreatedAt = t
	}
	return repo, nil
}

func (b *Backend) ListRepositories(ctx context.Context, opt domain.ListOptions) (domain.ListReposResult, error) {
	path := "/acr/v1/_catalog"
	q := url.Values{}
	if opt.PageSize > 0 {
		q.Set("n", fmt.Sprint(opt.PageSize))
	}
	if opt.PageToken != "" {
		q.Set("last", opt.PageToken)
	}
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}
	var body catalogResponse
	if err := b.request(ctx, http.MethodGet, path, "registry:catalog:*", &body, http.StatusOK); err != nil {
		return domain.ListReposResult{}, err
	}
	repos := make([]domain.Repository, 0, len(body.Repositories))
	for _, name := range body.Repositories {
		if name == "" {
			continue
		}
		repos = append(repos, domain.Repository{Name: name})
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return domain.ListReposResult{Repositories: repos, NextPageToken: nextToken(body.Link)}, nil
}

func (b *Backend) ListImages(ctx context.Context, repo string, opt domain.ListOptions) (domain.ListImagesResult, error) {
	path := "/acr/v1/" + escapePath(repo) + "/_manifests"
	q := url.Values{}
	if opt.PageSize > 0 {
		q.Set("n", fmt.Sprint(opt.PageSize))
	}
	if opt.PageToken != "" {
		q.Set("last", opt.PageToken)
	}
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}
	var body manifestsResponse
	if err := b.request(ctx, http.MethodGet, path, "repository:"+repo+":pull", &body, http.StatusOK); err != nil {
		return domain.ListImagesResult{}, err
	}
	images := make([]domain.Image, 0, len(body.Manifests))
	for _, m := range body.Manifests {
		img, err := image(m)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		images = append(images, img)
	}
	return domain.ListImagesResult{Images: images, NextPageToken: nextToken(body.Link)}, nil
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
	return distribution.NewWithOptions(distribution.Options{
		BaseURL: b.base.String(),
		Client:  b.client,
		RequestEditor: func(ctx context.Context, req *http.Request) error {
			scope := scopeFor(req.Method, req.URL.Path)
			token, err := b.accessToken(ctx, scope)
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		},
	})
}

func (b *Backend) request(ctx context.Context, method, path, scope string, out any, want ...int) error {
	token, err := b.accessToken(ctx, scope)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, b.url(path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, status := range want {
		if resp.StatusCode == status {
			if out == nil || resp.Body == nil {
				return nil
			}
			return json.NewDecoder(resp.Body).Decode(out)
		}
	}
	return mapHTTPError(resp)
}

func (b *Backend) refreshToken(ctx context.Context) (string, error) {
	if b.cfg.RefreshToken != "" {
		return b.cfg.RefreshToken, nil
	}
	if b.cfg.Credential == nil {
		return "", fmt.Errorf("acr credential or refresh token is required: %w", domain.ErrInvalidInput)
	}
	scopes := b.cfg.AADScopes
	if len(scopes) == 0 {
		scopes = []string{defaultAADScope}
	}
	tok, err := b.cfg.Credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: scopes})
	if err != nil {
		return "", err
	}
	if tok.Token == "" {
		return "", fmt.Errorf("acr credential returned empty Entra token: %w", domain.ErrInvalidInput)
	}
	form := url.Values{
		"grant_type":   {"access_token"},
		"service":      {b.service},
		"access_token": {tok.Token},
	}
	var body refreshResponse
	if err := b.postForm(ctx, "/oauth2/exchange", form, &body, http.StatusOK); err != nil {
		return "", err
	}
	if body.RefreshToken == "" {
		return "", fmt.Errorf("acr exchange returned empty refresh token: %w", domain.ErrInvalidInput)
	}
	return body.RefreshToken, nil
}

func (b *Backend) accessToken(ctx context.Context, scope string) (string, error) {
	refresh, err := b.refreshToken(ctx)
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"service":       {b.service},
		"scope":         {scope},
		"refresh_token": {refresh},
	}
	var body accessResponse
	if err := b.postForm(ctx, "/oauth2/token", form, &body, http.StatusOK); err != nil {
		return "", err
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("acr token exchange returned empty access token: %w", domain.ErrInvalidInput)
	}
	return body.AccessToken, nil
}

func (b *Backend) postForm(ctx context.Context, path string, form url.Values, out any, want ...int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.url(path), strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, status := range want {
		if resp.StatusCode == status {
			return json.NewDecoder(resp.Body).Decode(out)
		}
	}
	return mapHTTPError(resp)
}

func (b *Backend) url(path string) string {
	u := *b.base
	if strings.HasPrefix(path, "/") {
		u.Path = strings.TrimRight(u.Path, "/") + path
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + "/" + path
	}
	return u.String()
}

type refreshResponse struct {
	RefreshToken string `json:"refresh_token"`
}

type accessResponse struct {
	AccessToken string `json:"access_token"`
}

type catalogResponse struct {
	Repositories []string `json:"repositories"`
	Link         string   `json:"link"`
}

type repositoryResponse struct {
	Name        string `json:"name"`
	CreatedTime string `json:"createdTime"`
}

type manifestsResponse struct {
	Manifests []manifestResponse `json:"manifests"`
	Link      string             `json:"link"`
}

type manifestResponse struct {
	Digest         string   `json:"digest"`
	MediaType      string   `json:"mediaType"`
	ImageSize      int64    `json:"imageSize"`
	Tags           []string `json:"tags"`
	CreatedTime    string   `json:"createdTime"`
	LastUpdateTime string   `json:"lastUpdateTime"`
}

func image(m manifestResponse) (domain.Image, error) {
	if m.Digest == "" {
		return domain.Image{}, fmt.Errorf("acr manifest response missing digest: %w", domain.ErrInvalidInput)
	}
	img := domain.Image{
		Digest:    m.Digest,
		Tags:      append([]string(nil), m.Tags...),
		MediaType: m.MediaType,
		Size:      m.ImageSize,
	}
	sort.Strings(img.Tags)
	ts := m.CreatedTime
	if ts == "" {
		ts = m.LastUpdateTime
	}
	if ts != "" {
		t, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			return domain.Image{}, fmt.Errorf("acr manifest time %q: %w", ts, domain.ErrInvalidInput)
		}
		img.PushedAt = t
	}
	return img, nil
}

func scopeFor(method, path string) string {
	repo := repoFromV2Path(path)
	if repo == "" {
		return "registry:catalog:*"
	}
	actions := "pull"
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		actions = "pull,push"
	case http.MethodDelete:
		actions = "pull,delete"
	}
	return "repository:" + repo + ":" + actions
}

func repoFromV2Path(path string) string {
	rest := strings.TrimPrefix(path, "/v2/")
	if rest == path || rest == "" {
		return ""
	}
	for _, marker := range []string{"/blobs/uploads/", "/blobs/", "/manifests/", "/tags/list"} {
		if i := strings.Index(rest, marker); i >= 0 {
			return rest[:i]
		}
	}
	return ""
}

func escapePath(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func nextToken(link string) string {
	if link == "" {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	return u.Query().Get("last")
}

func mapHTTPError(resp *http.Response) error {
	var body struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	msg := resp.Status
	if len(body.Errors) > 0 && body.Errors[0].Message != "" {
		msg = body.Errors[0].Message
	} else if body.Error.Message != "" {
		msg = body.Error.Message
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("acr %s: %w", msg, domain.ErrNotFound)
	case http.StatusConflict:
		return fmt.Errorf("acr %s: %w", msg, domain.ErrAlreadyExists)
	case http.StatusBadRequest:
		return fmt.Errorf("acr %s: %w", msg, domain.ErrInvalidInput)
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return fmt.Errorf("acr %s: %w", msg, domain.ErrNotSupported)
	default:
		return fmt.Errorf("acr %s", msg)
	}
}

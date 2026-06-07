// Package aws_ecr implements domain.Registry against real Amazon ECR.
package aws_ecr

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	ecrtypes "github.com/aws/aws-sdk-go-v2/service/ecr/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/services/registry/backends/distribution"
)

// Config configures the real ECR backend.
type Config struct {
	// RegistryID scopes control-plane calls when set. When empty, ECR uses
	// the caller's default registry.
	RegistryID string
	// RegistryBaseURL overrides the proxyEndpoint returned by
	// GetAuthorizationToken for the OCI data-plane base URL. Real ECR
	// returns a reachable proxyEndpoint; simulators (e.g. sockerless) return
	// the canonical AWS URL shape which is unreachable in test environments.
	// Set this to the simulator's own base URL (e.g. "https://localhost:14599")
	// when running against a local simulator.
	RegistryBaseURL string
	// HTTPClient is used for the ECR Docker Registry /v2/ data plane.
	HTTPClient *http.Client
}

// Backend is a connected Amazon ECR registry backend.
type Backend struct {
	client *ecr.Client
	cfg    Config
}

var _ domain.Registry = (*Backend)(nil)

// New returns a backend using the provided AWS SDK ECR client.
func New(client *ecr.Client, cfg Config) (*Backend, error) {
	if client == nil {
		return nil, fmt.Errorf("ecr client is required: %w", domain.ErrInvalidInput)
	}
	return &Backend{client: client, cfg: cfg}, nil
}

func (b *Backend) CreateRepository(ctx context.Context, name string, opt domain.CreateRepoOptions) (domain.Repository, error) {
	in := &ecr.CreateRepositoryInput{
		RepositoryName: awsapi.String(name),
		RegistryId:     registryIDPtr(b.cfg.RegistryID),
		Tags:           awsTags(opt.Tags),
	}
	out, err := b.client.CreateRepository(ctx, in)
	if err != nil {
		return domain.Repository{}, wrap("CreateRepository", err)
	}
	if out.Repository == nil {
		return domain.Repository{}, fmt.Errorf("ecr CreateRepository returned no repository: %w", domain.ErrInvalidInput)
	}
	return b.repository(ctx, *out.Repository)
}

func (b *Backend) DeleteRepository(ctx context.Context, name string, force bool) error {
	_, err := b.client.DeleteRepository(ctx, &ecr.DeleteRepositoryInput{
		RepositoryName: awsapi.String(name),
		RegistryId:     registryIDPtr(b.cfg.RegistryID),
		Force:          force,
	})
	if err != nil {
		return wrap("DeleteRepository", err)
	}
	return nil
}

func (b *Backend) DescribeRepository(ctx context.Context, name string) (domain.Repository, error) {
	out, err := b.client.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{name},
		RegistryId:      registryIDPtr(b.cfg.RegistryID),
	})
	if err != nil {
		return domain.Repository{}, wrap("DescribeRepository", err)
	}
	if len(out.Repositories) == 0 {
		return domain.Repository{}, fmt.Errorf("ecr repository %q not found: %w", name, domain.ErrNotFound)
	}
	return b.repository(ctx, out.Repositories[0])
}

func (b *Backend) ListRepositories(ctx context.Context, opt domain.ListOptions) (domain.ListReposResult, error) {
	in := &ecr.DescribeRepositoriesInput{
		RegistryId: registryIDPtr(b.cfg.RegistryID),
		NextToken:  tokenPtr(opt.PageToken),
	}
	if opt.PageSize > 0 {
		in.MaxResults = awsapi.Int32(int32(opt.PageSize))
	}
	out, err := b.client.DescribeRepositories(ctx, in)
	if err != nil {
		return domain.ListReposResult{}, wrap("ListRepositories", err)
	}
	repos := make([]domain.Repository, 0, len(out.Repositories))
	for _, r := range out.Repositories {
		repo, err := b.repository(ctx, r)
		if err != nil {
			return domain.ListReposResult{}, err
		}
		repos = append(repos, repo)
	}
	return domain.ListReposResult{Repositories: repos, NextPageToken: awsapi.ToString(out.NextToken)}, nil
}

func (b *Backend) ListImages(ctx context.Context, repo string, opt domain.ListOptions) (domain.ListImagesResult, error) {
	in := &ecr.DescribeImagesInput{
		RepositoryName: awsapi.String(repo),
		RegistryId:     registryIDPtr(b.cfg.RegistryID),
		NextToken:      tokenPtr(opt.PageToken),
	}
	if opt.PageSize > 0 {
		in.MaxResults = awsapi.Int32(int32(opt.PageSize))
	}
	out, err := b.client.DescribeImages(ctx, in)
	if err != nil {
		return domain.ListImagesResult{}, wrap("ListImages", err)
	}
	images := make([]domain.Image, 0, len(out.ImageDetails))
	for _, detail := range out.ImageDetails {
		img, err := image(detail)
		if err != nil {
			return domain.ListImagesResult{}, err
		}
		images = append(images, img)
	}
	return domain.ListImagesResult{Images: images, NextPageToken: awsapi.ToString(out.NextToken)}, nil
}

func (b *Backend) DeleteImage(ctx context.Context, repo, reference string) error {
	id := imageIdentifier(reference)
	out, err := b.client.BatchDeleteImage(ctx, &ecr.BatchDeleteImageInput{
		RepositoryName: awsapi.String(repo),
		RegistryId:     registryIDPtr(b.cfg.RegistryID),
		ImageIds:       []ecrtypes.ImageIdentifier{id},
	})
	if err != nil {
		return wrap("DeleteImage", err)
	}
	if len(out.Failures) > 0 {
		return imageFailureError(out.Failures[0])
	}
	if len(out.ImageIds) == 0 {
		return fmt.Errorf("ecr DeleteImage returned no deleted image for %q: %w", reference, domain.ErrNotFound)
	}
	return nil
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
	in := &ecr.GetAuthorizationTokenInput{}
	if b.cfg.RegistryID != "" {
		in.RegistryIds = []string{b.cfg.RegistryID}
	}
	out, err := b.client.GetAuthorizationToken(ctx, in)
	if err != nil {
		return nil, wrap("GetAuthorizationToken", err)
	}
	auth, err := selectAuth(out.AuthorizationData, b.cfg.RegistryID)
	if err != nil {
		return nil, err
	}
	user, pass, err := decodeAuthorizationToken(awsapi.ToString(auth.AuthorizationToken))
	if err != nil {
		return nil, err
	}
	baseURL := awsapi.ToString(auth.ProxyEndpoint)
	if b.cfg.RegistryBaseURL != "" {
		baseURL = b.cfg.RegistryBaseURL
	}
	return distribution.NewWithOptions(distribution.Options{
		BaseURL:  baseURL,
		Client:   b.cfg.HTTPClient,
		Username: user,
		Password: pass,
	})
}

func (b *Backend) repository(ctx context.Context, r ecrtypes.Repository) (domain.Repository, error) {
	name := awsapi.ToString(r.RepositoryName)
	if name == "" {
		return domain.Repository{}, fmt.Errorf("ecr repository response missing repositoryName: %w", domain.ErrInvalidInput)
	}
	repo := domain.Repository{Name: name}
	if r.CreatedAt != nil {
		repo.CreatedAt = *r.CreatedAt
	}
	if r.RepositoryArn != nil {
		tags, err := b.tags(ctx, *r.RepositoryArn)
		if err != nil {
			return domain.Repository{}, err
		}
		repo.Tags = tags
	}
	return repo, nil
}

func (b *Backend) tags(ctx context.Context, arn string) (map[string]string, error) {
	out, err := b.client.ListTagsForResource(ctx, &ecr.ListTagsForResourceInput{ResourceArn: awsapi.String(arn)})
	if err != nil {
		return nil, wrap("ListTagsForResource", err)
	}
	return domainTags(out.Tags), nil
}

func awsTags(tags map[string]string) []ecrtypes.Tag {
	if len(tags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ecrtypes.Tag, 0, len(keys))
	for _, k := range keys {
		out = append(out, ecrtypes.Tag{Key: awsapi.String(k), Value: awsapi.String(tags[k])})
	}
	return out
}

func domainTags(tags []ecrtypes.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, tag := range tags {
		if tag.Key == nil {
			continue
		}
		out[*tag.Key] = awsapi.ToString(tag.Value)
	}
	return out
}

func image(detail ecrtypes.ImageDetail) (domain.Image, error) {
	digest := awsapi.ToString(detail.ImageDigest)
	if digest == "" {
		return domain.Image{}, fmt.Errorf("ecr image response missing imageDigest: %w", domain.ErrInvalidInput)
	}
	img := domain.Image{
		Digest:    digest,
		Tags:      append([]string(nil), detail.ImageTags...),
		MediaType: awsapi.ToString(detail.ImageManifestMediaType),
		Size:      awsapi.ToInt64(detail.ImageSizeInBytes),
	}
	if detail.ImagePushedAt != nil {
		img.PushedAt = *detail.ImagePushedAt
	}
	sort.Strings(img.Tags)
	return img, nil
}

func imageIdentifier(reference string) ecrtypes.ImageIdentifier {
	if strings.Contains(reference, ":") {
		return ecrtypes.ImageIdentifier{ImageDigest: awsapi.String(reference)}
	}
	return ecrtypes.ImageIdentifier{ImageTag: awsapi.String(reference)}
}

func imageFailureError(f ecrtypes.ImageFailure) error {
	reason := awsapi.ToString(f.FailureReason)
	if reason == "" {
		reason = string(f.FailureCode)
	}
	switch f.FailureCode {
	case ecrtypes.ImageFailureCodeImageNotFound:
		return fmt.Errorf("ecr image delete failed: %s: %w", reason, domain.ErrNotFound)
	case ecrtypes.ImageFailureCodeInvalidImageDigest, ecrtypes.ImageFailureCodeInvalidImageTag, ecrtypes.ImageFailureCodeMissingDigestAndTag:
		return fmt.Errorf("ecr image delete failed: %s: %w", reason, domain.ErrInvalidInput)
	default:
		return fmt.Errorf("ecr image delete failed: %s: %w", reason, domain.ErrNotSupported)
	}
}

func registryIDPtr(registryID string) *string {
	if registryID == "" {
		return nil
	}
	return awsapi.String(registryID)
}

func tokenPtr(token string) *string {
	if token == "" {
		return nil
	}
	return awsapi.String(token)
}

func selectAuth(data []ecrtypes.AuthorizationData, registryID string) (ecrtypes.AuthorizationData, error) {
	for _, auth := range data {
		endpoint := awsapi.ToString(auth.ProxyEndpoint)
		if awsapi.ToString(auth.AuthorizationToken) == "" || endpoint == "" {
			continue
		}
		if registryID == "" || strings.Contains(endpoint, registryID+".dkr.ecr.") {
			return auth, nil
		}
	}
	return ecrtypes.AuthorizationData{}, fmt.Errorf("ecr authorization data missing token or proxy endpoint: %w", domain.ErrInvalidInput)
}

func decodeAuthorizationToken(token string) (string, string, error) {
	if token == "" {
		return "", "", fmt.Errorf("ecr authorization token is empty: %w", domain.ErrInvalidInput)
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", fmt.Errorf("ecr authorization token is not base64: %w", domain.ErrInvalidInput)
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok || user == "" || pass == "" {
		return "", "", fmt.Errorf("ecr authorization token is not a Basic credential: %w", domain.ErrInvalidInput)
	}
	return user, pass, nil
}

func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("ecr %s: %w", op, mapError(err))
}

func mapError(err error) error {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.ErrorCode() {
	case "RepositoryNotFoundException", "ImageNotFoundException", "UploadNotFoundException":
		return fmt.Errorf("%s: %w", apiErr.ErrorMessage(), domain.ErrNotFound)
	case "RepositoryAlreadyExistsException", "ImageAlreadyExistsException", "ImageTagAlreadyExistsException", "LayerAlreadyExistsException":
		return fmt.Errorf("%s: %w", apiErr.ErrorMessage(), domain.ErrAlreadyExists)
	case "InvalidParameterException", "ValidationException", "RepositoryNotEmptyException", "InvalidTagParameterException",
		"InvalidLayerException", "InvalidLayerPartException", "ImageDigestDoesNotMatchException", "EmptyUploadException":
		return fmt.Errorf("%s: %w", apiErr.ErrorMessage(), domain.ErrInvalidInput)
	case "UnsupportedImageTypeException", "ImageStorageClassUpdateNotSupportedException", "UnsupportedUpstreamRegistryException":
		return fmt.Errorf("%s: %w", apiErr.ErrorMessage(), domain.ErrNotSupported)
	default:
		return err
	}
}

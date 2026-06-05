// Package aws_ecr is the AWS Elastic Container Registry frontend for
// shimanism's container-registry service (Phase 18). ECR has two planes:
//
//   - Control plane — awsJson1_1 (SigV4), generated from the ECR Smithy
//     model into services/registry/gen. The Adapter binds gen.ECRBackend
//     to domain.Registry.
//   - Data plane — the OCI Distribution /v2/ API authenticated with HTTP
//     Basic credentials minted by GetAuthorizationToken (N31). ECR
//     repository names are flat, so the control-plane repository and the
//     /v2/ repository share the same name.
package aws_ecr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/e6qu/shimanism/internal/awsjson"
	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/internal/registry/ocidistribution"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	gen "github.com/e6qu/shimanism/services/registry/gen"
)

const (
	registryID = "000000000000"
	region     = "us-east-1"
	registry   = registryID + ".dkr.ecr." + region + ".amazonaws.com"
)

// dataPlaneToken is the password half of the Basic credential
// GetAuthorizationToken mints and the /v2/ data plane verifies. It is a
// deterministic HMAC over a fixed label with the shim's test key, so the
// shim verifies it statelessly (no issued-token store).
func dataPlaneToken() string {
	m := hmac.New(sha256.New, []byte("test-key-do-not-use-in-prod"))
	m.Write([]byte("ecr-registry-data-plane"))
	return hex.EncodeToString(m.Sum(nil))
}

// Server routes ECR's two planes: awsJson1_1 control (SigV4) and the OCI
// /v2/ data plane (Basic auth).
type Server struct {
	control http.Handler
	oci     *ocidistribution.Router
}

// New returns the ECR frontend bound to the given backend.
func New(reg domain.Registry) http.Handler {
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "ecr", Region: region})
	control := sigv4verifier.Middleware(verifier, awsjson.WriteError)(gen.RegisterECRRoutes(&Adapter{reg: reg}))
	return &Server{control: control, oci: ocidistribution.New(reg)}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v2/") || r.URL.Path == "/v2" {
		if !validBasic(r) {
			s.challenge(w)
			return
		}
		s.oci.ServeHTTP(w, r)
		return
	}
	s.control.ServeHTTP(w, r)
}

// challenge emits the Basic auth challenge OCI clients use to discover
// the data-plane scheme (N31).
func (s *Server) challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="`+registry+`"`)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`))
}

// validBasic checks the request's Basic credential against the
// GetAuthorizationToken-minted token (username "AWS").
func validBasic(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok {
		return false
	}
	return user == "AWS" &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(dataPlaneToken())) == 1
}

// Adapter binds gen.ECRBackend to a domain.Registry.
type Adapter struct {
	reg domain.Registry
}

func epoch(t time.Time) *awsjson.EpochTime {
	e := awsjson.EpochTime(t)
	return &e
}

func strp(s string) *string { return &s }

func ecrRepository(r domain.Repository) *gen.Repository {
	out := &gen.Repository{
		RegistryId:     strp(registryID),
		RepositoryName: strp(r.Name),
		RepositoryArn:  strp("arn:aws:ecr:" + region + ":" + registryID + ":repository/" + r.Name),
		RepositoryUri:  strp(registry + "/" + r.Name),
	}
	if !r.CreatedAt.IsZero() {
		out.CreatedAt = epoch(r.CreatedAt)
	}
	return out
}

func (a *Adapter) CreateRepository(ctx context.Context, in *gen.CreateRepositoryRequest) (*gen.CreateRepositoryResponse, error) {
	repo, err := a.reg.CreateRepository(ctx, in.RepositoryName, domain.CreateRepoOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	return &gen.CreateRepositoryResponse{Repository: ecrRepository(repo)}, nil
}

func (a *Adapter) DescribeRepositories(ctx context.Context, in *gen.DescribeRepositoriesRequest) (*gen.DescribeRepositoriesResponse, error) {
	out := &gen.DescribeRepositoriesResponse{}
	if len(in.RepositoryNames) > 0 {
		for _, name := range in.RepositoryNames {
			repo, err := a.reg.DescribeRepository(ctx, name)
			if err != nil {
				return nil, mapErr(err)
			}
			out.Repositories = append(out.Repositories, *ecrRepository(repo))
		}
		return out, nil
	}
	res, err := a.reg.ListRepositories(ctx, domain.ListOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	for _, repo := range res.Repositories {
		out.Repositories = append(out.Repositories, *ecrRepository(repo))
	}
	return out, nil
}

func (a *Adapter) DeleteRepository(ctx context.Context, in *gen.DeleteRepositoryRequest) (*gen.DeleteRepositoryResponse, error) {
	force := in.Force != nil && *in.Force
	// Describe before delete so we can echo the deleted repository.
	repo, _ := a.reg.DescribeRepository(ctx, in.RepositoryName)
	if err := a.reg.DeleteRepository(ctx, in.RepositoryName, force); err != nil {
		return nil, mapErr(err)
	}
	if repo.Name == "" {
		repo = domain.Repository{Name: in.RepositoryName}
	}
	return &gen.DeleteRepositoryResponse{Repository: ecrRepository(repo)}, nil
}

func (a *Adapter) ListImages(ctx context.Context, in *gen.ListImagesRequest) (*gen.ListImagesResponse, error) {
	res, err := a.reg.ListImages(ctx, in.RepositoryName, domain.ListOptions{})
	if err != nil {
		return nil, mapErr(err)
	}
	out := &gen.ListImagesResponse{}
	for _, img := range res.Images {
		if len(img.Tags) == 0 {
			out.ImageIds = append(out.ImageIds, gen.ImageIdentifier{ImageDigest: strp(img.Digest)})
			continue
		}
		for _, tag := range img.Tags {
			d := img.Digest
			tg := tag
			out.ImageIds = append(out.ImageIds, gen.ImageIdentifier{ImageDigest: &d, ImageTag: &tg})
		}
	}
	return out, nil
}

func (a *Adapter) BatchDeleteImage(ctx context.Context, in *gen.BatchDeleteImageRequest) (*gen.BatchDeleteImageResponse, error) {
	out := &gen.BatchDeleteImageResponse{}
	for _, id := range in.ImageIds {
		ref := ""
		switch {
		case id.ImageDigest != nil && *id.ImageDigest != "":
			ref = *id.ImageDigest
		case id.ImageTag != nil:
			ref = *id.ImageTag
		}
		if ref == "" {
			continue
		}
		if err := a.reg.DeleteImage(ctx, in.RepositoryName, ref); err != nil {
			continue // best-effort; ECR reports per-image failures separately
		}
		out.ImageIds = append(out.ImageIds, id)
	}
	return out, nil
}

func (a *Adapter) GetAuthorizationToken(_ context.Context, _ *gen.GetAuthorizationTokenRequest) (*gen.GetAuthorizationTokenResponse, error) {
	token := base64.StdEncoding.EncodeToString([]byte("AWS:" + dataPlaneToken()))
	return &gen.GetAuthorizationTokenResponse{
		AuthorizationData: gen.AuthorizationDataList{{
			AuthorizationToken: strp(token),
			ProxyEndpoint:      strp("https://" + registry),
			ExpiresAt:          epoch(time.Now().Add(12 * time.Hour)),
		}},
	}, nil
}

// mapErr translates domain sentinels onto awsjson.BackendError with the
// ECR exception type, so the awsJson router emits the right error shape.
func mapErr(err error) error {
	switch {
	case domain.IsNotFound(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "RepositoryNotFoundException", Message: err.Error()}
	case domain.IsAlreadyExists(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "RepositoryAlreadyExistsException", Message: err.Error()}
	case domain.IsInvalidInput(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterException", Message: err.Error()}
	case domain.IsNotSupported(err):
		return &awsjson.BackendError{HTTPStatus: http.StatusBadRequest, Type: "InvalidParameterException", Message: err.Error()}
	default:
		return &awsjson.BackendError{HTTPStatus: http.StatusInternalServerError, Type: "ServerException", Message: err.Error()}
	}
}

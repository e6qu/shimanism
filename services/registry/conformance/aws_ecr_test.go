// Conformance: AWS ECR-shaped frontend exercised by the official
// aws-sdk-go-v2/service/ecr SDK (control plane, SigV4) and
// go-containerregistry (data plane). The data-plane push uses the exact
// ECR docker-login flow: GetAuthorizationToken returns base64("AWS:pw"),
// which becomes the HTTP Basic credential on /v2/.
package conformance_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e6qu/shimanism/internal/registry/frontends/aws_ecr"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func newECRClient(t *testing.T, endpoint string) *ecr.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	return ecr.NewFromConfig(cfg, func(o *ecr.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
}

func TestECRSDK_RepositoryLifecycle(t *testing.T) {
	srv := httptest.NewServer(aws_ecr.New(inmem.New()))
	defer srv.Close()
	cli := newECRClient(t, srv.URL)
	ctx := context.Background()

	const repo = "team/app"
	if _, err := cli.CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName: awsapi.String(repo),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	desc, err := cli.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repo},
	})
	if err != nil {
		t.Fatalf("DescribeRepositories: %v", err)
	}
	if len(desc.Repositories) != 1 || awsapi.ToString(desc.Repositories[0].RepositoryName) != repo {
		t.Fatalf("DescribeRepositories = %+v", desc.Repositories)
	}

	list, err := cli.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{})
	if err != nil {
		t.Fatalf("DescribeRepositories (all): %v", err)
	}
	if len(list.Repositories) != 1 {
		t.Errorf("list = %d repos, want 1", len(list.Repositories))
	}

	if _, err := cli.DeleteRepository(ctx, &ecr.DeleteRepositoryInput{
		RepositoryName: awsapi.String(repo),
		Force:          true,
	}); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}
	if _, err := cli.DescribeRepositories(ctx, &ecr.DescribeRepositoriesInput{
		RepositoryNames: []string{repo},
	}); err == nil {
		t.Error("DescribeRepositories after delete: want error, got nil")
	}
}

func TestECR_ImagePushPull(t *testing.T) {
	srv := httptest.NewServer(aws_ecr.New(inmem.New()))
	defer srv.Close()
	cli := newECRClient(t, srv.URL)
	ctx := context.Background()

	const repo = "myapp"
	if _, err := cli.CreateRepository(ctx, &ecr.CreateRepositoryInput{
		RepositoryName: awsapi.String(repo),
	}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}

	// docker-login flow: GetAuthorizationToken -> base64("AWS:pw") -> Basic.
	auth, err := cli.GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		t.Fatalf("GetAuthorizationToken: %v", err)
	}
	if len(auth.AuthorizationData) == 0 {
		t.Fatal("GetAuthorizationToken returned no authorization data")
	}
	raw, err := base64.StdEncoding.DecodeString(awsapi.ToString(auth.AuthorizationData[0].AuthorizationToken))
	if err != nil {
		t.Fatalf("decode authorization token: %v", err)
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		t.Fatalf("authorization token not user:pass, got %q", raw)
	}

	host := srv.Listener.Addr().String()
	ref, err := name.ParseReference(host+"/"+repo+":v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	opts := []remote.Option{
		remote.WithAuth(&authn.Basic{Username: user, Password: pass}),
		remote.WithTransport(http.DefaultTransport),
	}

	img, err := random.Image(2048, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	pushed, _ := img.Digest()
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("push (remote.Write through ECR shim): %v", err)
	}
	pulled, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if got, _ := pulled.Digest(); got != pushed {
		t.Errorf("pulled digest = %s, want %s", got, pushed)
	}

	// The pushed image is visible through the control-plane ListImages.
	imgs, err := cli.ListImages(ctx, &ecr.ListImagesInput{RepositoryName: awsapi.String(repo)})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(imgs.ImageIds) == 0 {
		t.Error("ListImages returned no images after push")
	}
}

func TestECR_DataPlaneUnauthenticatedChallenged(t *testing.T) {
	srv := httptest.NewServer(aws_ecr.New(inmem.New()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v2/")
	if err != nil {
		t.Fatalf("GET /v2/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v2/ = %d, want 401", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Basic ") {
		t.Errorf("WWW-Authenticate = %q, want Basic challenge", resp.Header.Get("WWW-Authenticate"))
	}
}

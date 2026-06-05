package conformance_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	"github.com/e6qu/shimanism/services/registry/backends/distribution"
)

func TestDistributionBackend_GCPAR_ImagePushPull(t *testing.T) {
	base := os.Getenv("SHIMANISM_DISTRIBUTION_URL")
	if base == "" {
		t.Skip("set SHIMANISM_DISTRIBUTION_URL to a live CNCF Distribution registry")
	}
	backend, err := distribution.New(base)
	if err != nil {
		t.Fatalf("distribution.New: %v", err)
	}

	srv := httptest.NewServer(gcp_artifactregistry.Handler(backend))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	repoID := "dist-" + strings.ToLower(t.Name())
	repoID = strings.ReplaceAll(repoID, "_", "-")
	repoName := "projects/shim/locations/us/repositories/" + repoID
	ref, err := name.ParseReference(host+"/"+repoName+":v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	opts := []remote.Option{
		remote.WithAuth(&authn.Bearer{Token: arBearerJWT()}),
		remote.WithTransport(http.DefaultTransport),
	}
	img, err := random.Image(2048, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	pushedDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("push through GCP AR shim into Distribution: %v", err)
	}
	t.Cleanup(func() {
		if err := backend.DeleteRepository(context.Background(), repoName, true); err != nil && !domain.IsNotSupported(err) {
			t.Logf("cleanup DeleteRepository(%q): %v", repoName, err)
		}
	})

	pulled, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("pull through GCP AR shim from Distribution: %v", err)
	}
	pulledDigest, err := pulled.Digest()
	if err != nil {
		t.Fatalf("pulled.Digest: %v", err)
	}
	if pulledDigest != pushedDigest {
		t.Fatalf("pulled digest = %s, want %s", pulledDigest, pushedDigest)
	}

	tags, err := remote.List(ref.Context(), opts...)
	if err != nil {
		t.Fatalf("remote.List tags: %v", err)
	}
	if !contains(tags, "v1") {
		t.Fatalf("tags = %v, want v1", tags)
	}

	images, err := backend.ListImages(context.Background(), repoName, domain.ListOptions{})
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(images.Images) != 1 || images.Images[0].Digest != pushedDigest.String() {
		t.Fatalf("ListImages = %+v, want digest %s", images.Images, pushedDigest)
	}
	if !images.Images[0].PushedAt.IsZero() {
		t.Fatalf("Distribution backend invented PushedAt = %s", images.Images[0].PushedAt)
	}
}

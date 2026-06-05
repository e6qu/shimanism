// Conformance: GCP Artifact Registry control plane exercised by the
// official google.golang.org/api/artifactregistry/v1 REST SDK —
// repository create / get / list / delete (LROs) + dockerImages list.
package conformance_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	arraw "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func newARService(t *testing.T, endpoint string) *arraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://artifactregistry.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := arraw.NewService(context.Background(),
		option.WithEndpoint(endpoint+"/"),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("artifactregistry service: %v", err)
	}
	return svc
}

func TestARSDK_RepositoryLifecycle(t *testing.T) {
	srv := httptest.NewServer(gcp_artifactregistry.Handler(inmem.New()))
	defer srv.Close()
	svc := newARService(t, srv.URL)
	ctx := context.Background()

	parent := "projects/shim-conformance/locations/us-central1"
	repoName := parent + "/repositories/myrepo"

	// Create (LRO; the shim returns a done operation).
	op, err := svc.Projects.Locations.Repositories.Create(parent, &arraw.Repository{
		Format: "DOCKER",
		Labels: map[string]string{"team": "registry"},
	}).RepositoryId("myrepo").Context(ctx).Do()
	if err != nil {
		t.Fatalf("repositories.create: %v", err)
	}
	if !op.Done {
		t.Fatalf("create operation not done: %+v", op)
	}

	// Get.
	repo, err := svc.Projects.Locations.Repositories.Get(repoName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("repositories.get: %v", err)
	}
	if repo.Name != repoName {
		t.Errorf("repo name = %q, want %q", repo.Name, repoName)
	}
	if repo.Format != "DOCKER" {
		t.Errorf("repo format = %q, want DOCKER", repo.Format)
	}
	if repo.Labels["team"] != "registry" {
		t.Errorf("repo labels = %v, want team=registry", repo.Labels)
	}

	// List contains it.
	list, err := svc.Projects.Locations.Repositories.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("repositories.list: %v", err)
	}
	found := false
	for _, r := range list.Repositories {
		if r.Name == repoName {
			found = true
		}
	}
	if !found {
		t.Errorf("list missing %q (%d repos)", repoName, len(list.Repositories))
	}

	// dockerImages.list (empty for a fresh repo) succeeds.
	if _, err := svc.Projects.Locations.Repositories.DockerImages.List(repoName).Context(ctx).Do(); err != nil {
		t.Fatalf("dockerImages.list: %v", err)
	}

	// Delete (LRO done), then Get 404s.
	delOp, err := svc.Projects.Locations.Repositories.Delete(repoName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("repositories.delete: %v", err)
	}
	if !delOp.Done {
		t.Fatalf("delete operation not done: %+v", delOp)
	}
	if _, err := svc.Projects.Locations.Repositories.Get(repoName).Context(ctx).Do(); err == nil {
		t.Error("get after delete: want error, got nil")
	} else if !strings.Contains(err.Error(), "404") {
		t.Errorf("get after delete error = %v, want 404", err)
	}
}

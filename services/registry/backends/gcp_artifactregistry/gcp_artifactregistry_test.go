package gcp_artifactregistry

import (
	"errors"
	"testing"

	arraw "google.golang.org/api/artifactregistry/v1"

	"github.com/e6qu/shimanism/internal/registry/domain"
)

func TestParseRepositoryName(t *testing.T) {
	parent, id, err := parseRepositoryName("projects/p/locations/us/repositories/repo")
	if err != nil {
		t.Fatalf("parseRepositoryName: %v", err)
	}
	if parent != "projects/p/locations/us" || id != "repo" {
		t.Fatalf("parseRepositoryName = %q/%q", parent, id)
	}
}

func TestParseRepositoryNameRejectsNonResourceName(t *testing.T) {
	for _, name := range []string{"repo", "projects/p/locations/us/repositories/", "projects/p/locations/us/repositories/a/b"} {
		if _, _, err := parseRepositoryName(name); !errors.Is(err, domain.ErrInvalidInput) {
			t.Fatalf("parseRepositoryName(%q): want ErrInvalidInput, got %v", name, err)
		}
	}
}

func TestDockerImageDigestFromURI(t *testing.T) {
	img, err := dockerImage(&arraw.DockerImage{
		Name:           "projects/p/locations/us/repositories/r/dockerImages/app@sha256:name",
		Uri:            "us-docker.pkg.dev/p/r/app@sha256:uri",
		Tags:           []string{"b", "a"},
		MediaType:      "application/vnd.oci.image.manifest.v1+json",
		ImageSizeBytes: 123,
	})
	if err != nil {
		t.Fatalf("dockerImage: %v", err)
	}
	if img.Digest != "sha256:uri" {
		t.Fatalf("Digest = %q, want sha256:uri", img.Digest)
	}
	if len(img.Tags) != 2 || img.Tags[0] != "a" || img.Tags[1] != "b" {
		t.Fatalf("Tags = %v, want sorted [a b]", img.Tags)
	}
}

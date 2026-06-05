package inmem_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func dg(b []byte) string {
	s := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(s[:])
}

func TestInmem_RepositoryLifecycle(t *testing.T) {
	b := inmem.New()
	ctx := context.Background()

	if _, err := b.CreateRepository(ctx, "team/app", domain.CreateRepoOptions{Tags: map[string]string{"env": "test"}}); err != nil {
		t.Fatalf("CreateRepository: %v", err)
	}
	// Re-create errors.
	if _, err := b.CreateRepository(ctx, "team/app", domain.CreateRepoOptions{}); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("re-create: want ErrAlreadyExists, got %v", err)
	}
	// Describe returns tags.
	r, err := b.DescribeRepository(ctx, "team/app")
	if err != nil {
		t.Fatalf("DescribeRepository: %v", err)
	}
	if r.Tags["env"] != "test" {
		t.Errorf("repo tags = %v, want env=test", r.Tags)
	}
	// Describe of an absent repo 404s.
	if _, err := b.DescribeRepository(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("describe absent: want ErrNotFound, got %v", err)
	}
	// List.
	list, err := b.ListRepositories(ctx, domain.ListOptions{})
	if err != nil {
		t.Fatalf("ListRepositories: %v", err)
	}
	if len(list.Repositories) != 1 || list.Repositories[0].Name != "team/app" {
		t.Fatalf("ListRepositories = %+v", list.Repositories)
	}
	// Delete.
	if err := b.DeleteRepository(ctx, "team/app", false); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}
	if _, err := b.DescribeRepository(ctx, "team/app"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("describe after delete: want ErrNotFound, got %v", err)
	}
}

func TestInmem_PushAutoCreatesRepoAndForceDelete(t *testing.T) {
	b := inmem.New()
	ctx := context.Background()
	repo := "auto/created"

	// A manifest push to a never-created repo materializes it (OCI base
	// behavior), then a non-force delete of the non-empty repo is rejected.
	manifest := []byte(`{"schemaVersion":2}`)
	if _, err := b.PutManifest(ctx, repo, "v1", "application/vnd.oci.image.manifest.v1+json", strings.NewReader(string(manifest))); err != nil {
		t.Fatalf("PutManifest (auto-create): %v", err)
	}
	if _, err := b.DescribeRepository(ctx, repo); err != nil {
		t.Fatalf("repo not materialized by push: %v", err)
	}
	if err := b.DeleteRepository(ctx, repo, false); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("non-force delete of non-empty repo: want ErrInvalidInput, got %v", err)
	}
	if err := b.DeleteRepository(ctx, repo, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
}

func TestInmem_BlobDigestVerified(t *testing.T) {
	b := inmem.New()
	ctx := context.Background()
	repo := "r"
	blob := []byte("the-bytes")

	sess, err := b.StartBlobUpload(ctx, repo)
	if err != nil {
		t.Fatalf("StartBlobUpload: %v", err)
	}
	// Completing with the wrong digest is rejected.
	if _, err := b.CompleteBlobUpload(ctx, repo, sess, dg([]byte("other")), strings.NewReader(string(blob))); !errors.Is(err, domain.ErrDigestMismatch) {
		t.Fatalf("wrong digest: want ErrDigestMismatch, got %v", err)
	}
	// A fresh session with the correct digest succeeds and is retrievable.
	sess, _ = b.StartBlobUpload(ctx, repo)
	desc, err := b.CompleteBlobUpload(ctx, repo, sess, dg(blob), strings.NewReader(string(blob)))
	if err != nil {
		t.Fatalf("CompleteBlobUpload: %v", err)
	}
	if desc.Digest != dg(blob) {
		t.Errorf("descriptor digest = %s, want %s", desc.Digest, dg(blob))
	}
	if _, err := b.BlobExists(ctx, repo, dg(blob)); err != nil {
		t.Fatalf("BlobExists after upload: %v", err)
	}
}

// Package conformance_test (non-test file): backend factories used
// by parameterised conformance tests. Each factory is named the same
// as the backend it produces; a per-PR conformance lane picks one
// factory at a time (controlled via env vars so CI can light up
// each backend in its own job without modifying the test source).
package conformance_test

import (
	"os"
	"testing"

	"github.com/e6qu/shimanism/internal/storage/domain"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	miniobackend "github.com/e6qu/shimanism/services/storage/backends/minio"
)

// backendFactory returns a Storage backend ready for use. Each
// factory may call t.Skip if its required infrastructure
// (Docker container, env var, …) isn't available.
type backendFactory struct {
	name string
	fn   func(t *testing.T) domain.Storage
}

// activeBackends returns the set of backend factories to drive the
// conformance suite against. Lists every backend; each factory
// internally decides whether to skip.
func activeBackends() []backendFactory {
	return []backendFactory{
		{name: "inmem", fn: newInmem},
		{name: "minio", fn: newMinIO},
	}
}

// newInmem is always available — no external dependencies.
func newInmem(t *testing.T) domain.Storage {
	t.Helper()
	return inmem.New()
}

// newMinIO connects to a MinIO server using MINIO_ENDPOINT /
// MINIO_ACCESS_KEY / MINIO_SECRET_KEY env vars. Skipped if
// MINIO_ENDPOINT is not set. CI starts a MinIO container in a
// pre-step and sets the env vars.
func newMinIO(t *testing.T) domain.Storage {
	t.Helper()
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		t.Skip("MINIO_ENDPOINT not set (MinIO backend conformance disabled)")
	}
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	b, err := miniobackend.New(miniobackend.Config{
		Endpoint:  endpoint,
		AccessKey: access,
		SecretKey: secret,
	})
	if err != nil {
		t.Fatalf("connect to MinIO at %s: %v", endpoint, err)
	}
	return b
}

package distribution_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/e6qu/shimanism/internal/registry/domain"
	"github.com/e6qu/shimanism/services/registry/backends/distribution"
)

func TestCreateRepositoryUnsupported(t *testing.T) {
	b, err := distribution.New("http://127.0.0.1:5000")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.CreateRepository(context.Background(), "empty/repo", domain.CreateRepoOptions{}); !errors.Is(err, domain.ErrNotSupported) {
		t.Fatalf("CreateRepository: want ErrNotSupported, got %v", err)
	}
}

func TestLiveDistributionCatalogRequiresRealBackend(t *testing.T) {
	base := os.Getenv("SHIMANISM_DISTRIBUTION_URL")
	if base == "" {
		t.Skip("set SHIMANISM_DISTRIBUTION_URL to a live CNCF Distribution registry")
	}
	b, err := distribution.New(base)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := b.ListRepositories(context.Background(), domain.ListOptions{PageSize: 1}); err != nil {
		t.Fatalf("ListRepositories against live Distribution: %v", err)
	}
}

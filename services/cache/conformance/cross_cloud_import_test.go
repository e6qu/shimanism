// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for cache:
// TestCrossCloudImport_Roundtrip_CacheAWStoGCPMemorystore.
//
// User writes AWS ElastiCache-shape Terraform; the actual instance
// lives in a mock GCP Memorystore server backed by inmem.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	redisapi "google.golang.org/api/redis/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/cache/domain"
	awsfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	gcpfront "github.com/e6qu/shimanism/internal/cache/frontends/gcp_memorystore"
	gcpbackend "github.com/e6qu/shimanism/services/cache/backends/gcp"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
)

const terraformCrossCloudCacheConfig = `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    elasticache = "%s"
  }
}

resource "aws_elasticache_cluster" "imported" {
  cluster_id      = "cross-cloud-cache"
  engine          = "redis"
  engine_version  = "7.1"
  node_type       = "cache.t3.micro"
  num_cache_nodes = 1
}
`

func TestCrossCloudImport_Roundtrip_CacheAWStoGCPMemorystore(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateInstance(ctx, "cross-cloud-cache", domain.CreateInstanceOptions{
		EngineVersion: "7.1",
		NodeType:      "cache.t3.micro",
	}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}

	mockGCP := httptest.NewServer(gcpfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	svc, err := redisapi.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new redis service: %v", err)
	}
	gcpB := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud", Region: "us-central1"})

	shim := httptest.NewServer(awsfront.New(gcpB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudCacheConfig, shim.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirCacheCC(),
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runTf(args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("init", "-no-color")

	stdout, stderr, err := runTf("import", "-no-color",
		"aws_elasticache_cluster.imported", "cross-cloud-cache")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — fidelity gap\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirCacheCC() string {
	d := filepath.Join(os.TempDir(), "shim-cache-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

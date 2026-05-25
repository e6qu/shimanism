// Phase 10 sub-phase 10.2: terraform apply drift audit for cache.
//
// Contract: services/cache/APPLY_INTERSECTION.md.
//
// GCP cell drives the full apply → plan -detailed-exitcode → destroy
// cycle through hashicorp/google google_redis_instance. BUG-5 closed
// in 10.1 (Operations.Get); the v1/v1beta1 path family alignment + a
// few canonical-default fields closed the apply path in 10.3.
//
// AWS cell: aws_elasticache_cluster reconciles via ModifyCacheCluster
// + waits on parameter-group / subnet-group metadata not in the
// intersection (same class as BUG-2; Phase 6 posture).
//
// Azure cell: azurerm_redis_cache polls Azure-AsyncOperation URLs
// the shim doesn't emit at this phase.
//
// Skipped if `terraform` isn't on PATH.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/cache/domain"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/cache/backends/inmem"
)

func TestTerraform_AWSCache_Apply_NoDrift(t *testing.T) {
	t.Skip("aws_elasticache_cluster reconciles via ModifyCacheCluster + needs parameter-group / subnet-group metadata (same class as BUG-2; Phase 6 posture)")
}

const terraformApplyCacheGCPConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project              = "shim-conformance"
  region               = "us-central1"
  access_token         = "%s"
  redis_custom_endpoint = "%s/v1/"
}

resource "google_redis_instance" "applied" {
  name           = "tf-applied-cache"
  region         = "us-central1"
  memory_size_gb = 1
  redis_version  = "REDIS_7_0"
  tier           = "BASIC"
}
`

func TestTerraform_GCPCache_Apply_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartCacheServerGCP(t, backend)

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://redis.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyCacheGCPConfig, jwt, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForWorkdir(dir),
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
	mustRun("apply", "-no-color", "-auto-approve")

	stdout, stderr, err := runTf("plan", "-no-color", "-detailed-exitcode")
	switch {
	case err == nil:
	case isExitCodeCacheApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	// inmem.DeleteInstance is async; poll briefly.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := backend.ListInstances(context.Background(), domain.ListInstancesOptions{})
		if err != nil {
			t.Fatalf("backend.ListInstances after destroy: %v", err)
		}
		if len(got.Instances) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := backend.ListInstances(context.Background(), domain.ListInstancesOptions{})
	names := make([]string, 0, len(got.Instances))
	for _, in := range got.Instances {
		names = append(names, in.Name)
	}
	t.Errorf("backend still has instances after destroy + poll: %v", names)
}

func TestTerraform_AzureCache_Apply_NoDrift(t *testing.T) {
	t.Skip("azurerm_redis_cache polls Azure-AsyncOperation URLs the shim doesn't emit at this phase")
}

func isExitCodeCacheApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

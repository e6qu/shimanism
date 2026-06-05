// Conformance: GCP Artifact Registry control plane exercised by the
// official `hashicorp/google` Terraform provider via
// `artifact_registry_custom_endpoint`. Skipped if `terraform` isn't on
// PATH.
package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

const terraformGCPARConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                           = "shim-conformance"
  region                            = "us-central1"
  access_token                      = "%s"
  artifact_registry_custom_endpoint = "%s/v1/"
}

resource "google_artifact_registry_repository" "tf" {
  location      = "us-central1"
  repository_id = "tf-repo"
  format        = "DOCKER"
}
`

func TestTerraform_GCPAR_RepositoryLifecycle(t *testing.T) {
	t.Parallel()
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	srv := httptest.NewServer(gcp_artifactregistry.Handler(inmem.New()))
	defer srv.Close()

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://artifactregistry.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPARConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	run := func(args ...string) ([]byte, []byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+cacheDir,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}

	if stdout, stderr, err := run("init", "-no-color"); err != nil {
		t.Fatalf("terraform init\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	stdout, stderr, err := run("apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Apply complete!") {
		t.Errorf("apply: missing 'Apply complete!':\n%s", stdout)
	}
	if !strings.Contains(string(stdout), "google_artifact_registry_repository.tf: Creation complete") {
		t.Errorf("apply: repository creation not confirmed:\n%s", stdout)
	}
	stdout, stderr, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("destroy: missing 'Destroy complete!':\n%s\nstderr: %s", stdout, stderr)
	}
}

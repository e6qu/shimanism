// Phase 2 conformance: GCP Secret Manager-shaped frontend exercised
// by the official `hashicorp/google` Terraform provider, configured
// with `secret_manager_custom_endpoint` pointing at the shim and a
// fake `access_token` to skip credential resolution.
package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

const terraformGCPSecretsConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                       = "shim-conformance"
  region                        = "us-central1"
  access_token                  = "%s"
  secret_manager_custom_endpoint = "%s/v1/"
}

resource "google_secret_manager_secret" "tf" {
  secret_id = "tf-gcp-driven"
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "tf" {
  secret      = google_secret_manager_secret.tf.id
  secret_data = "shimanism + google terraform"
}
`

func TestTerraform_GCPSecrets_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraform(t)
	srv := harness.StartSecretsServerGCP(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://secretmanager.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPSecretsConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	for _, step := range [][]string{
		{"init", "-no-color"},
		{"apply", "-auto-approve", "-no-color"},
		{"destroy", "-auto-approve", "-no-color"},
	} {
		stdout, stderr, err := runTerraform(t, dir, bin, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}

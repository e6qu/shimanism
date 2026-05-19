// Phase 1.14 conformance: GCS-shaped frontend exercised by the
// official `hashicorp/google` Terraform provider. The provider is
// configured with an `endpoints` block pointing at the shim plus
// `request_timeout`; credentials are minimal because the shim does
// not validate them at this phase.
package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// terraformGCSConfig configures the hashicorp/google provider with
// the shim as the storage endpoint. The `storage_custom_endpoint`
// block is what redirects buckets + objects API calls.
const terraformGCSConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                 = "shim-conformance"
  region                  = "us-central1"
  access_token            = "shim-fake-token"
  storage_custom_endpoint = "%s/storage/v1/"
}

resource "google_storage_bucket" "tf" {
  name          = "tf-gcs-driven"
  location      = "US"
  force_destroy = true
}

resource "google_storage_bucket_object" "obj" {
  name    = "from-terraform.txt"
  bucket  = google_storage_bucket.tf.name
  content = "shimanism + google terraform"
}
`

// TestTerraform_GCS_ResourceLifecycle runs init/apply/destroy with
// google_storage_bucket + google_storage_bucket_object resources
// against the shim's GCS frontend.
func TestTerraform_GCS_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraform(t)
	srv := harness.StartStorageServerGCS(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCSConfig, srv.URL)
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

// Conformance: GCP Cloud KMS-shaped frontend exercised by the official
// `hashicorp/google` Terraform provider, configured with
// `kms_custom_endpoint` pointing at the shim and a fake `access_token`
// so the provider doesn't reach real Cloud KMS. Covers the
// google_kms_key_ring + google_kms_crypto_key resource lifecycle.
// Skipped if the `terraform` binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

const terraformGCPKMSConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project             = "shim-conformance"
  region              = "us-central1"
  access_token        = "%s"
  kms_custom_endpoint = "%s/v1/"
}

resource "google_kms_key_ring" "tf" {
  name     = "tf-shim-ring"
  location = "us-central1"
}

resource "google_kms_crypto_key" "tf" {
  name     = "tf-shim-key"
  key_ring = google_kms_key_ring.tf.id
}
`

func TestTerraform_GCPKMS_KeyLifecycle(t *testing.T) {
	t.Parallel()
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	srv := harness.StartKMSServerGCP(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://cloudkms.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformGCPKMSConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)

	run := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tfBin, args...)
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
		t.Errorf("terraform apply: missing 'Apply complete!':\n%s", stdout)
	}
	if !strings.Contains(string(stdout), "google_kms_crypto_key.tf: Creation complete") {
		t.Errorf("terraform apply: crypto key creation not confirmed:\n%s", stdout)
	}
	// google_kms_key_ring / google_kms_crypto_key are not deletable in
	// Cloud KMS; the provider removes them from state on destroy without
	// a delete API call. Destroy should still complete cleanly.
	stdout, stderr, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("terraform destroy: missing 'Destroy complete!':\n%s\nstderr: %s", stdout, stderr)
	}
}

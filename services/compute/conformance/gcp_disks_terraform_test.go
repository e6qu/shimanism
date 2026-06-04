// Conformance: GCP Compute Engine block storage exercised by the official
// hashicorp/google Terraform provider. Covers Phase 17: google_compute_disk
// apply + destroy. HTTPS-only (hashicorp/google RemoveBasePathVersion regex);
// Linux-only via SSL_CERT_FILE.
package conformance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const terraformGCPDiskConfig = `
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
  zone                    = "us-central1-a"
  access_token            = "%s"
  compute_custom_endpoint = "%s/compute/v1/"
}

resource "google_compute_disk" "shim" {
  name = "shim-tf-disk"
  type = "pd-ssd"
  zone = "us-central1-a"
  size = 25
}
`

func TestTerraformGCP_Compute_DiskLifecycle(t *testing.T) {
	t.Parallel()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCA := findSystemCABundleForCompute()
	if systemCA == "" {
		t.Skip("no system CA bundle at known Unix paths — SSL_CERT_FILE workaround requires Linux")
	}

	srv := harness.StartComputeServerGCPTLS(t, inmem.New())
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPDiskConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	systemBytes, err := os.ReadFile(systemCA)
	if err != nil {
		t.Fatalf("read system CA %s: %v", systemCA, err)
	}
	combinedCA := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), srv.CertPEM...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	run := func(args ...string) ([]byte, []byte, error) {
		return runTerraformComputeGCPWithCA(t, dir, bin, combinedCA, args...)
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
	if !strings.Contains(string(stdout), "google_compute_disk.shim: Creation complete") {
		t.Errorf("terraform apply: disk creation not confirmed:\n%s", stdout)
	}
	stdout, stderr, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("terraform destroy: missing 'Destroy complete!':\n%s\nstderr: %s", stdout, stderr)
	}
}

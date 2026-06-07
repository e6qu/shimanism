// Conformance: GCP Compute Engine LB-shaped frontend exercised by the
// official `hashicorp/google` Terraform provider, configured with
// `compute_custom_endpoint` pointing at the shim and a fake JWT
// access_token to skip credential resolution.
//
// The shim must serve HTTPS for this test: hashicorp/google's
// RemoveBasePathVersion regex only matches https:// — HTTP endpoints
// corrupt the path. Linux-only because the workaround threads a
// self-signed cert through SSL_CERT_FILE, which Go's crypto/tls
// honors on Unix but not on macOS.
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
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

const terraformGCPLBConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                  = "shim-conformance"
  region                   = "us-central1"
  access_token             = "%s"
  compute_custom_endpoint  = "%s/compute/v1/"
}

resource "google_compute_backend_service" "shim" {
  name     = "tf-lb-be"
  protocol = "HTTP"
}
`

func runTerraformLBGCPWithCA(t *testing.T, dir, bin, caBundle string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// Per-workdir plugin cache for parallel-safe `terraform init`.
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+cacheDir,
		"SSL_CERT_FILE="+caBundle,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestTerraform_GCPLB_L7BackendServiceLifecycle creates a
// google_compute_backend_service resource via the hashicorp/google provider
// pointed at the shim (HTTPS), then destroys it.
func TestTerraform_GCPLB_L7BackendServiceLifecycle(t *testing.T) {
	t.Parallel()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCA := findSystemCABundleForLB()
	if systemCA == "" {
		t.Skip("no system CA bundle at known Unix paths — SSL_CERT_FILE workaround requires Linux")
	}

	srv := harness.StartLoadBalancerServerGCPTLS(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://compute.googleapis.com/",
		15*time.Minute,
	)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPLBConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	// Combined CA = system bundle + shim's self-signed cert.
	systemBytes, err := os.ReadFile(systemCA)
	if err != nil {
		t.Fatalf("read system CA %s: %v", systemCA, err)
	}
	combinedCA := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), srv.CertPEM...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	run := func(args ...string) ([]byte, []byte, error) {
		return runTerraformLBGCPWithCA(t, dir, bin, combinedCA, args...)
	}

	// init
	if stdout, stderr, err := run("init", "-no-color"); err != nil {
		t.Fatalf("terraform init\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	// apply
	stdout, stderr, err := run("apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	applyOut := string(stdout)
	if !strings.Contains(applyOut, "Apply complete!") {
		t.Errorf("terraform apply: missing 'Apply complete!' in output:\n%s", applyOut)
	}
	if !strings.Contains(applyOut, "google_compute_backend_service.shim: Creation complete") {
		t.Errorf("terraform apply: backend service creation not confirmed:\n%s", applyOut)
	}

	// destroy
	stdout, stderr, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("terraform destroy: missing 'Destroy complete!' in output:\n%s\nstderr: %s", stdout, stderr)
	}
}

func findSystemCABundleForLB() string {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/tls/cacert.pem",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

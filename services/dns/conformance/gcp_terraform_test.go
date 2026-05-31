// Conformance: GCP Cloud DNS-shaped frontend exercised by the official
// `hashicorp/google` Terraform provider, configured with
// `dns_custom_endpoint` pointing at the shim and a fake `access_token`
// to skip credential resolution.
//
// The shim must serve **HTTPS** for this test: hashicorp/google's
// `RemoveBasePathVersion` regex matches only `http[s]://` (literal
// `s`, not `[s]?`), so HTTP-scheme endpoints fall through the regex
// no-op and `strings.ReplaceAll("/dns/", "")` corrupts them into
// malformed URLs that panic in `googleapi.ResolveRelative`. HTTPS
// makes the regex match, the strip work correctly, and the SDK build
// the right path. Linux-only because the workaround threads a self-
// signed cert through `SSL_CERT_FILE`, which Go's `crypto/tls` honors
// on Unix but not on macOS.
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
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func runTerraformDNSWithCA(t *testing.T, dir, bin, caBundle string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForDNSWorkdir(dir),
		"SSL_CERT_FILE="+caBundle,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

const terraformGCPDNSConfig = `
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
  dns_custom_endpoint = "%s/dns/v1/"
}

resource "google_dns_managed_zone" "tf" {
  name        = "tf-example-com"
  dns_name    = "tf.example.com."
  description = "shimanism + google terraform"
  visibility  = "public"
}

resource "google_dns_record_set" "www" {
  managed_zone = google_dns_managed_zone.tf.name
  name         = "www.tf.example.com."
  type         = "A"
  ttl          = 300
  rrdatas      = ["1.2.3.4", "5.6.7.8"]
}
`

func TestTerraform_GCPCloudDNS_ZoneAndRecordLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraformAWSRoute53(t) // same helper — just looks for `terraform` on PATH
	systemCABundle := findSystemCABundleForDNS()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found at known Unix paths — SSL_CERT_FILE workaround requires Linux")
	}
	srv := harness.StartDNSServerGCPTLS(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://dns.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPDNSConfig, jwt, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	// Combined CA = system bundle + shim's self-signed cert.
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA bundle %s: %v", systemCABundle, err)
	}
	combinedCA := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), srv.CertPEM...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}
	for _, step := range [][]string{
		{"init", "-no-color"},
		{"apply", "-auto-approve", "-no-color"},
		{"destroy", "-auto-approve", "-no-color"},
	} {
		stdout, stderr, err := runTerraformDNSWithCA(t, dir, bin, combinedCA, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}

func findSystemCABundleForDNS() string {
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

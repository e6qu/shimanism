// Phase 4 conformance: GCP Pub/Sub-shaped fanout frontend
// exercised by the hashicorp/google Terraform provider via
// pubsub_custom_endpoint override.
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
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

const terraformGCPPubsubConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                = "shim-conformance"
  region                 = "us-central1"
  access_token           = "%s"
  pubsub_custom_endpoint = "%s/v1/"
}

resource "google_pubsub_topic" "tf" {
  name = "tf-pubsub-driven"
}

resource "google_pubsub_subscription" "a" {
  name                 = "tf-pubsub-driven-a"
  topic                = google_pubsub_topic.tf.id
  ack_deadline_seconds = 30
}

resource "google_pubsub_subscription" "b" {
  name                 = "tf-pubsub-driven-b"
  topic                = google_pubsub_topic.tf.id
  ack_deadline_seconds = 30
}
`

func requireTerraform(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runTerraform(t *testing.T, dir, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+terraformPubsubPluginCacheDir(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func terraformPubsubPluginCacheDir() string {
	d := filepath.Join(os.TempDir(), "shim-pubsub-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func TestTerraform_GCPPubsub_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraform(t)
	srv := harness.StartPubsubServerGCP(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://pubsub.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPPubsubConfig, jwt, srv.URL)
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

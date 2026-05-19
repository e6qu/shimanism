// Phase 3 conformance: GCP Pub/Sub-shaped frontend exercised by the
// official `hashicorp/google` Terraform provider via the
// `pubsub_custom_endpoint` override + fake access token so the
// provider doesn't reach real GCP.
package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

const terraformGCPQueueConfig = `
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
  access_token           = "shim-fake-token"
  pubsub_custom_endpoint = "%s/v1/"
}

resource "google_pubsub_topic" "tf" {
  name = "tf-gcp-driven"
}

resource "google_pubsub_subscription" "tf" {
  name                 = "tf-gcp-driven"
  topic                = google_pubsub_topic.tf.id
  ack_deadline_seconds = 30
}
`

func TestTerraform_GCPQueue_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraformQueue(t)
	srv := harness.StartQueueServerGCP(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformGCPQueueConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	for _, step := range [][]string{
		{"init", "-no-color"},
		{"apply", "-auto-approve", "-no-color"},
		{"destroy", "-auto-approve", "-no-color"},
	} {
		stdout, stderr, err := runTerraformQueue(t, dir, bin, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}

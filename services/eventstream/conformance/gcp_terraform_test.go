// Conformance: GCP Managed Service for Apache Kafka exercised by the official
// hashicorp/google Terraform provider. Uses managed_kafka_custom_endpoint
// pointing at the shim with a fake access_token. Covers cluster + topic
// lifecycle. Skipped if the `terraform` binary isn't on PATH.
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
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

const terraformGCPManagedKafkaConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project                       = "shim-conformance"
  region                        = "us-central1"
  access_token                  = "%s"
  managed_kafka_custom_endpoint = "%s/v1/"
}

resource "google_managed_kafka_cluster" "shim" {
  cluster_id = "shim-tf-cluster"
  location   = "us-central1"

  capacity_config {
    vcpu_count   = 3
    memory_bytes = 3221225472
  }

  gcp_config {
    access_config {
      network_configs {
        subnet = "projects/shim-conformance/regions/us-central1/subnetworks/default"
      }
    }
  }
}

resource "google_managed_kafka_topic" "shim" {
  topic_id   = "shim-tf-topic"
  cluster    = google_managed_kafka_cluster.shim.cluster_id
  location   = "us-central1"
  partition_count = 1
  replication_factor = 1
}
`

func TestTerraform_GCPManagedKafka_ClusterAndTopicLifecycle(t *testing.T) {
	t.Parallel()
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	srv := harness.StartEventStreamServerGCP(t, inmem.New())

	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://managedkafka.googleapis.com/",
		15*time.Minute,
	)
	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformGCPManagedKafkaConfig, jwt, srv.URL)
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
	if !strings.Contains(string(stdout), "google_managed_kafka_cluster.shim: Creation complete") {
		t.Errorf("terraform apply: cluster creation not confirmed:\n%s", stdout)
	}
	stdout, stderr, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("terraform destroy: missing 'Destroy complete!':\n%s\nstderr: %s", stdout, stderr)
	}
}

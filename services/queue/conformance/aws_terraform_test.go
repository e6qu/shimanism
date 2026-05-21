// Phase 3 conformance: AWS SQS-shaped frontend exercised by the
// official `hashicorp/aws` Terraform provider via `endpoints { sqs }`
// override + dummy credentials so the provider never reaches real
// AWS.
package conformance_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

const terraformAWSQueueConfig = `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    sqs = "%s"
  }
}

resource "aws_sqs_queue" "tf" {
  name                       = "tf-driven"
  visibility_timeout_seconds = 30
  message_retention_seconds  = 60
}
`

func requireTerraformQueue(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runTerraformQueue(t *testing.T, dir, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+terraformQueuePluginCacheDir(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func terraformQueuePluginCacheDir() string {
	d := filepath.Join(os.TempDir(), "shim-queue-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func TestTerraform_AWSQueue_ResourceLifecycle(t *testing.T) {
	// BUG-2 closed in Phase 10.3 — domain.Queues.SetQueueAttributes
	// is wired across all five backends, GetQueueAttributes surfaces
	// the canonical AWS attribute keys hashicorp/aws polls during
	// WaitForStateEqual, and the awsQueryCompatible legacy error
	// codes (notably AWS.SimpleQueueService.NonExistentQueue for the
	// post-destroy delete-confirmation wait) are wired via the
	// x-amzn-query-error header. This cell now runs end-to-end.
	t.Parallel()
	bin := requireTerraformQueue(t)
	srv := harness.StartQueueServerAWS(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAWSQueueConfig, srv.URL)
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

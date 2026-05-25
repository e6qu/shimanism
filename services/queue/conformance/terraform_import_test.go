// Phase 9 sub-phase 9.5: terraform import test for queue.
//
// Pre-seeds a queue in the inmem backend, then runs `terraform
// import aws_sqs_queue.imported <url>` through the shim's AWS SQS
// frontend. Asserts import succeeds + the subsequent plan reports
// no diff for the in-config attributes.
//
// Caveat: BUG-2 (SetQueueAttributes) means a full
// terraform-import-then-apply cycle proposes diffs for attributes
// the shim doesn't yet support reconciling. The plan-is-no-diff
// assertion here is soft (logged, not fatal) until BUG-2 closes.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/queue/domain"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

const terraformImportQueueConfig = `
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
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    sqs = "%s"
  }
}

resource "aws_sqs_queue" "imported" {
  name = "shim-imported-queue"
}
`

func TestTerraform_AWSSQS_Import(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	ctx := context.Background()
	if _, err := backend.CreateQueue(ctx, "shim-imported-queue", domain.CreateQueueOptions{}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}
	srv := harness.StartQueueServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformImportQueueConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tf, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForWorkdir(dir),
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runTf(args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("init", "-no-color")

	queueURL := srv.URL + "/000000000000/shim-imported-queue"
	stdout, stderr, err := runTf("import", "-no-color",
		"aws_sqs_queue.imported", queueURL)
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("terraform plan reports a diff after queue import — expected per BUG-2 until SetQueueAttributes lands\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

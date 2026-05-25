// Phase 10 sub-phase 10.2: terraform apply drift audit for functions.
//
// Contract: services/functions/APPLY_INTERSECTION.md.
//
// AWS cell drives the full apply → plan -detailed-exitcode → destroy
// cycle through hashicorp/aws aws_lambda_function. BUG-13 (Lambda
// role/publish/memory_size soft plan diffs) closed in 10.3 via domain
// extension (Role + Publish fields) + AWS frontend round-trip.
//
// GCP cell: BUG-5 closed in 10.1 but BUG-16 (v1 vs v1beta-style path
// alignment between shim and hashicorp/google) still applies.
// Diamond-skipped with pointer.
//
// Azure cell: azurerm_container_app polls Azure-AsyncOperation URLs
// the shim doesn't emit at this phase.
//
// Skipped if `terraform` isn't on PATH.
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
	"time"

	functionsdomain "github.com/e6qu/shimanism/internal/functions/domain"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
)

const terraformApplyFunctionsAWSConfig = `
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
    lambda = "%s"
  }
}

resource "aws_lambda_function" "applied" {
  function_name = "shim-applied-func"
  package_type  = "Image"
  image_uri     = "ghcr.io/example/api:v1"
  role          = "arn:aws:iam::000000000000:role/lambda"
  timeout       = 60
  memory_size   = 128
  publish       = false
}
`

func TestTerraform_AWSFunctions_Apply_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartFunctionsServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyFunctionsAWSConfig, srv.URL)
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
	mustRun("apply", "-no-color", "-auto-approve")

	stdout, stderr, err := runTf("plan", "-no-color", "-detailed-exitcode")
	switch {
	case err == nil:
	case isExitCodeFunctionsApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	// inmem.DeleteFunction is async (marks Deleting, goroutine
	// removes after a delay); poll briefly for the eventual empty
	// state. Real backends (AWS Lambda / Cloud Run / Container Apps /
	// Knative) are also async, so this matches production posture.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := backend.ListFunctions(context.Background(), functionsdomain.ListFunctionsOptions{})
		if err != nil {
			t.Fatalf("backend.ListFunctions after destroy: %v", err)
		}
		if len(got.Functions) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := backend.ListFunctions(context.Background(), functionsdomain.ListFunctionsOptions{})
	names := make([]string, 0, len(got.Functions))
	for _, fn := range got.Functions {
		names = append(names, fn.Name)
	}
	t.Errorf("backend still has functions after destroy + poll: %v", names)
}

func TestTerraform_GCPFunctions_Apply_NoDrift(t *testing.T) {
	t.Skip("BUG-5 closed in 10.1; BUG-16 family (hashicorp/google path-version alignment) pending")
}

func TestTerraform_AzureFunctions_Apply_NoDrift(t *testing.T) {
	t.Skip("azurerm_container_app polls Azure-AsyncOperation URLs the shim doesn't emit at this phase")
}

func isExitCodeFunctionsApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

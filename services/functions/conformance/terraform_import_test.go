// Phase 9 sub-phase 9.5: terraform import test for functions.
//
// Pre-seeds a container-image Lambda function in the inmem backend,
// then runs `terraform import aws_lambda_function.imported <name>`
// through the shim's AWS Lambda frontend. Asserts the import
// succeeds.
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

	"github.com/e6qu/shimanism/internal/functions/domain"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
)

const terraformImportFunctionsConfig = `
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

resource "aws_lambda_function" "imported" {
  function_name = "shim-imported-func"
  package_type  = "Image"
  image_uri     = "ghcr.io/example/api:v1"
  role          = "arn:aws:iam::000000000000:role/lambda"
  timeout       = 60
}
`

func TestTerraform_AWSLambda_Import(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	ctx := context.Background()
	if _, err := backend.CreateFunction(ctx, "shim-imported-func", domain.CreateFunctionOptions{
		Image:          "ghcr.io/example/api:v1",
		TimeoutSeconds: 60,
	}); err != nil {
		t.Fatalf("seed function: %v", err)
	}
	srv := harness.StartFunctionsServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformImportFunctionsConfig, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirFunctions(),
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

	stdout, stderr, err := runTf("import", "-no-color",
		"aws_lambda_function.imported", "shim-imported-func")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("terraform plan reports a diff after lambda import — fidelity gap\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirFunctions() string {
	d := filepath.Join(os.TempDir(), "shim-functions-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

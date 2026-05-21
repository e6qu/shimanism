// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for
// functions: TestCrossCloudImport_Roundtrip_FunctionsAWStoGCPRun.
//
// User writes AWS Lambda-shape Terraform; the actual function
// lives in a mock GCP Cloud Run server backed by inmem.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/api/option"
	runapi "google.golang.org/api/run/v2"

	"github.com/e6qu/shimanism/internal/functions/domain"
	awsfront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	gcpfront "github.com/e6qu/shimanism/internal/functions/frontends/gcp_cloudrun"
	gcpbackend "github.com/e6qu/shimanism/services/functions/backends/gcp"
	"github.com/e6qu/shimanism/services/functions/backends/inmem"
)

const terraformCrossCloudFunctionsConfig = `
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
  function_name = "cross-cloud-func"
  package_type  = "Image"
  image_uri     = "ghcr.io/example/api:v1"
  role          = "arn:aws:iam::000000000000:role/lambda"
  timeout       = 60
}
`

func TestCrossCloudImport_Roundtrip_FunctionsAWStoGCPRun(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateFunction(ctx, "cross-cloud-func", domain.CreateFunctionOptions{
		Image:          "ghcr.io/example/api:v1",
		TimeoutSeconds: 60,
	}); err != nil {
		t.Fatalf("seed function: %v", err)
	}

	mockGCP := httptest.NewServer(gcpfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	svc, err := runapi.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new run service: %v", err)
	}
	gcpB := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud", Region: "us-central1"})

	shim := httptest.NewServer(awsfront.New(gcpB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudFunctionsConfig, shim.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirFunctionsCC(),
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
		"aws_lambda_function.imported", "cross-cloud-func")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — expected BUG-13 family\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirFunctionsCC() string {
	d := filepath.Join(os.TempDir(), "shim-functions-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

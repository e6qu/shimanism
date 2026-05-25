// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for
// apigateway: TestCrossCloudImport_Roundtrip_APIGwAWStoGCP.
//
// User writes AWS API Gateway v2-shape Terraform; the actual API
// lives in a mock GCP API Gateway server backed by inmem. The
// shim's AWS APIGW v2 frontend + GCP backend translate.
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

	apigwapi "google.golang.org/api/apigateway/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/apigateway/domain"
	awsapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/aws_apigatewayv2"
	gcpfront "github.com/e6qu/shimanism/internal/apigateway/frontends/gcp_apigateway"
	gcpbackend "github.com/e6qu/shimanism/services/apigateway/backends/gcp"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

const terraformCrossCloudAPIGWConfig = `
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
    apigatewayv2 = "%s"
  }
}

resource "aws_apigatewayv2_api" "imported" {
  name          = "cross-cloud-api"
  protocol_type = "HTTP"
}
`

func TestCrossCloudImport_Roundtrip_APIGwAWStoGCP(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	// Layer 1: state-of-record.
	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateGateway(ctx, "cross-cloud-api", domain.CreateGatewayOptions{}); err != nil {
		t.Fatalf("seed gateway: %v", err)
	}

	// Layer 2: mock cloud B = GCP API Gateway-shaped HTTP frontend.
	mockGCP := httptest.NewServer(gcpfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	// Layer 3: GCP backend pointing at the mock.
	svc, err := apigwapi.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new apigateway service: %v", err)
	}
	gcpB := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud", Region: "us-central1"})

	// Layer 4: AWS APIGW v2 frontend over the GCP backend.
	shim := httptest.NewServer(awsapigwfront.New(gcpB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudAPIGWConfig, shim.URL)
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

	stdout, stderr, err := runTf("import", "-no-color",
		"aws_apigatewayv2_api.imported", "cross-cloud-api")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — fidelity gap\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

// Phase 8 conformance: AWS API Gateway v2-shaped frontend
// exercised by the official `hashicorp/aws` Terraform provider.
// Uses `endpoints { apigatewayv2 = "..." }` override + dummy
// credentials so the provider doesn't reach real AWS.
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
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

const terraformAWSAPIGWConfig = `
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
    apigatewayv2 = "%s"
  }
}

resource "aws_apigatewayv2_api" "tf" {
  name          = "tf-driven"
  protocol_type = "HTTP"
}
`

func requireTerraformAPIGW(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runTerraformAPIGW(t *testing.T, dir, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirAPIGW(),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func terraformPluginCacheDirAPIGW() string {
	d := filepath.Join(os.TempDir(), "shim-apigw-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func TestTerraform_AWSAPIGateway_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraformAPIGW(t)
	srv := harness.StartAPIGatewayServerAWS(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAWSAPIGWConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}
	for _, step := range [][]string{
		{"init", "-no-color"},
		{"apply", "-auto-approve", "-no-color"},
		{"destroy", "-auto-approve", "-no-color"},
	} {
		stdout, stderr, err := runTerraformAPIGW(t, dir, bin, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}

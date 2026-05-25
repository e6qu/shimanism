// Phase 2 conformance: AWS Secrets Manager-shaped frontend
// exercised by the official `hashicorp/aws` Terraform provider.
// Uses an `endpoints { secretsmanager = "..." }` override + dummy
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
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

const terraformAWSSecretsConfig = `
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
    secretsmanager = "%s"
  }
}

resource "aws_secretsmanager_secret" "tf" {
  name                    = "tf-driven"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "tf" {
  secret_id     = aws_secretsmanager_secret.tf.id
  secret_string = "shimanism + terraform"
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
		// Keep the provider cache scoped to this Terraform working
		// directory. The cache is not safe to share across parallel
		// terraform init calls.
		"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForWorkdir(dir),
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestTerraform_AWSSecrets_ResourceLifecycle(t *testing.T) {
	t.Parallel()
	bin := requireTerraform(t)
	srv := harness.StartSecretsServerAWS(t, inmem.New())

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAWSSecretsConfig, srv.URL)
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

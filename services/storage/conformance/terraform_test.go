package conformance_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/e6qu/shimanism/services/storage/gen"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// requireTerraform returns the path to `terraform` or skips the test.
// CI installs it; dev environments install ad hoc.
func requireTerraform(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

// terraformAWSConfig declares the AWS provider with endpoint override
// + path-style addressing, plus the resource block(s) under test.
const terraformAWSConfig = `
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
  s3_use_path_style           = true
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    s3 = "%s"
  }
}

resource "aws_s3_bucket" "tf" {
  bucket        = "tf-driven"
  force_destroy = true
}

resource "aws_s3_object" "obj" {
  bucket  = aws_s3_bucket.tf.id
  key     = "from-terraform.txt"
  content = "shimanism + terraform"
}
`

func runTerraform(t *testing.T, dir, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestTerraform_ResourceLifecycle runs `terraform init / apply / destroy`
// against the real `hashicorp/aws` provider with `aws_s3_bucket` +
// `aws_s3_object` resources. The provider's Read step calls many
// GetBucket* config operations; the shim's intersection manifest
// includes the universally-defaulted ones (`GetBucketVersioning`,
// `GetBucketTagging`, etc., each returning empty/disabled state) so
// every cloud's "freshly-created bucket" reads the same way through
// the shim.
//
// This is the load-bearing test: real customer Terraform workflows
// use resources, not data sources.
func TestTerraform_ResourceLifecycle(t *testing.T) {
	bin := requireTerraform(t)
	srv := harness.StartStorageServer(t, inmem.New())
	_ = context.Background
	_ = aws.String
	_ = gen.CreateBucketRequest{}

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAWSConfig, srv.URL)
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

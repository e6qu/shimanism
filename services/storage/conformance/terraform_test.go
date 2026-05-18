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

# Read an object that was seeded into the shim's in-mem backend
# before terraform ran. The data source exercises HeadObject and
# GetObject through the shim — both intersection operations.
data "aws_s3_object" "seeded" {
  bucket = "tf-data"
  key    = "hello.txt"
}

output "body" {
  value = data.aws_s3_object.seeded.body
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

// TestTerraform_DataSourceAgainstShim runs `terraform apply` against a
// `data "aws_s3_object"` block. The shim's in-mem backend is pre-seeded
// with a bucket + object before terraform runs, and the data source
// reads it via HeadObject + GetObject — both intersection operations.
//
// We deliberately do NOT exercise `resource "aws_s3_bucket"` here: the
// TF AWS provider's post-create read step calls many bucket-config
// operations (GetBucketLocation, GetBucketVersioning, GetBucketTagging,
// …) that are not in shimanism's object-storage intersection. Provider
// flows that require those land in their own follow-up phase once the
// shim's resource backends are wired (Phase 1.5+).
func TestTerraform_DataSourceAgainstShim(t *testing.T) {
	bin := requireTerraform(t)

	backend := inmem.New()
	// Seed via the backend interface directly — no need to drive a
	// real PutObject through the shim because the goal of this test
	// is the TF *read* path.
	ctx := context.Background()
	if _, err := backend.CreateBucket(ctx, &gen.CreateBucketRequest{Bucket: "tf-data"}); err != nil {
		t.Fatalf("seed CreateBucket: %v", err)
	}
	if _, err := backend.PutObject(ctx, &gen.PutObjectRequest{
		Bucket: "tf-data",
		Key:    "hello.txt",
		Body:   []byte("hello from shimanism"),
	}); err != nil {
		t.Fatalf("seed PutObject: %v", err)
	}
	_ = aws.String // keep imports used regardless of helper ordering

	srv := harness.StartStorageServer(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAWSConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	for _, step := range [][]string{
		{"init", "-no-color"},
		{"apply", "-auto-approve", "-no-color"},
	} {
		stdout, stderr, err := runTerraform(t, dir, bin, step...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(step, " "), stdout, stderr, err)
		}
	}
}

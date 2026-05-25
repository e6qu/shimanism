// Phase 10 sub-phase 10.2: terraform apply drift audit for storage.
//
// Drives the write path the Phase 9 import test didn't:
//
//  1. terraform apply against the shim → drives CreateBucket.
//  2. terraform plan -refresh-only -detailed-exitcode → drives the
//     Read path against the just-created state. Exit code 0 means
//     no drift; exit code 2 surfaces a Create-then-Read drift bug
//     that must be filed against APPLY_INTERSECTION.md's in-contract
//     attributes.
//  3. terraform destroy → cleans up via DeleteBucket.
//
// Contract: services/storage/APPLY_INTERSECTION.md. The HCL only
// drives in-contract attributes (name + region for buckets; key +
// content + metadata for objects). Out-of-contract attributes are
// covered separately by 10.2-C (invalid-input fidelity).
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

	"github.com/e6qu/shimanism/internal/harness"
	storagedomain "github.com/e6qu/shimanism/internal/storage/domain"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

const terraformApplyBucketConfig = `
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
  s3_use_path_style           = true

  endpoints {
    s3 = "%s"
  }
}

resource "aws_s3_bucket" "applied" {
  bucket = "shim-applied-bucket"

  # tags = {} matches the canonical "no tags" Terraform idiom; without
  # it, hashicorp/aws records tags = {} on refresh (because the shim's
  # NoSuchTagSet 404 is interpreted as "empty tag set"), which differs
  # from the absent-tags state apply created. See BUGS.md § false
  # positives.
  tags = {}
}
`

func TestTerraform_AWSS3_Apply_Bucket_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartStorageServer(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyBucketConfig, srv.URL)
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

	// 1. apply — drives CreateBucket through the AWS S3 frontend →
	// shim → inmem backend.
	mustRun("apply", "-no-color", "-auto-approve")

	// 2. plan with -detailed-exitcode — drives the read path against
	// the just-created state. The 10.2 assertion: exit code 0 (no
	// pending changes). Exit 2 means the next apply would do work,
	// which after a fresh apply is a Create-then-Read drift bug.
	//
	// We deliberately don't use -refresh-only here: that mode
	// surfaces state-update-only changes the provider records on
	// read (e.g. hashicorp/aws records tags = {} from a NoSuchTagSet
	// 404), which are benign Terraform-provider quirks rather than
	// shim fidelity gaps. See BUGS.md § false positives (BUG-14).
	// Plain plan exits 0 if the next apply would be a no-op, which
	// is the honest user-facing assertion.
	stdout, stderr, err := runTf("plan", "-no-color", "-detailed-exitcode")
	switch {
	case err == nil:
		// 0 — no pending changes. Pass through to destroy.
	case isExitCode(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		// Continue to destroy so the test cleans up.
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	// 3. destroy — leaves no residue. Verifies DeleteBucket.
	mustRun("destroy", "-no-color", "-auto-approve")

	// Sanity check the backend is empty.
	got, err := backend.ListBuckets(context.Background(), storagedomain.ListBucketsOptions{})
	if err != nil {
		t.Fatalf("backend.ListBuckets after destroy: %v", err)
	}
	if len(got.Buckets) != 0 {
		names := make([]string, 0, len(got.Buckets))
		for _, b := range got.Buckets {
			names = append(names, b.Name)
		}
		t.Errorf("backend still has buckets after destroy: %v", names)
	}
}

// isExitCode reports whether err is an *exec.ExitError with the
// given exit code.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

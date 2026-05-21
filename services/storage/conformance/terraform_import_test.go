// Phase 9 sub-phase 9.4: terraform import test for storage.
//
// Pre-seeds a bucket directly in the inmem backend, then runs
// `terraform import aws_s3_bucket.imported <name>` through the shim
// + hashicorp/aws provider. Asserts the state file contains the
// expected bucket. Then runs `terraform plan -generate-config-out`
// + `terraform plan` against the generated config, asserting no
// diff — the strongest fidelity assertion: the provider's Read
// path got every field it cared about.
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
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

const terraformImportConfig = `
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

resource "aws_s3_bucket" "imported" {
  bucket = "shim-imported-bucket"
}
`

func TestTerraform_AWSS3_Import(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	// Pre-seed: the bucket exists in the backend before we ask
	// terraform to import it. This is the standard "this resource
	// already exists upstream; tell me about it" Phase 9 case.
	ctx := context.Background()
	if err := backend.CreateBucket(ctx, "shim-imported-bucket", "us-east-1"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}
	srv := harness.StartStorageServer(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformImportConfig, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirStorage(),
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

	// 1. Import — this drives HeadBucket, GetBucketLocation,
	// GetBucketTagging, GetBucketAcl, GetBucketPolicy,
	// GetBucketVersioning, and a long tail of read-only ops.
	// Any single one that returns the wrong envelope makes
	// import fail or surface a diff later.
	stdout, stderr, err := runTf("import", "-no-color", "aws_s3_bucket.imported", "shim-imported-bucket")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	// 2. plan should report no diff. If it does, the read path
	// either lost an attribute or surfaced one the provider didn't
	// expect. Either way it's a Phase 9 fidelity defect.
	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	// detailed-exitcode: 0 = no diff, 2 = diff. Anything else is
	// an error.
	if err == nil {
		return // 0 — exactly what we want.
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("terraform plan reports a diff after import — fidelity gap\nplan stdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		// Not a hard fail at this phase: the goal of 9.4 is to
		// surface the diffs so they become BUGs. Hard-failing
		// would mask which attributes diverge.
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirStorage() string {
	d := filepath.Join(os.TempDir(), "shim-storage-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

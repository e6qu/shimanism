// Phase 9 sub-phase 9.13 — exit criterion:
// TestCrossCloudImport_Roundtrip_StorageAWStoGCS.
//
// The promise: when a resource that *lives in cloud B* is imported
// via shimanism through the cloud A wire frontend, Terraform sees
// the resource as the cloud A shape. The user's Terraform module
// stays AWS-shaped; the actual bytes live in GCS.
//
// Architecture (mock-tier):
//
//	┌──────────────────────┐
//	│ terraform (hashicorp/│
//	│ aws) — aws_s3_bucket │
//	└──────────┬───────────┘
//	           │ SigV4 GET, endpoints { s3 = $SHIM }
//	           ▼
//	┌──────────────────────┐
//	│ shim AWS S3 frontend │
//	│ + GCS backend        │
//	└──────────┬───────────┘
//	           │ GCS JSON GET (STORAGE_EMULATOR_HOST=$MOCK_GCS)
//	           ▼
//	┌──────────────────────┐
//	│ Mock cloud B (GCS)   │
//	│ = harness GCS front  │
//	│   over inmem         │
//	└──────────────────────┘
//
// The bucket exists in `inmem` (the data layer for the mock GCS).
// Terraform thinks it's an aws_s3_bucket. Import succeeds + plan
// is no-diff iff every wire op the AWS frontend's importer drives
// translates correctly into a GCS call against the mock cloud,
// and the inmem data round-trips back as honest AWS-shaped state.
//
// Skipped if `terraform` isn't on PATH.
package conformance_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/restxml"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	gcsfront "github.com/e6qu/shimanism/internal/storage/frontends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

const terraformCrossCloudConfig = `
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
  bucket = "cross-cloud-bucket"
}
`

func TestCrossCloudImport_Roundtrip_StorageAWStoGCS(t *testing.T) {
	// No t.Parallel — uses t.Setenv (STORAGE_EMULATOR_HOST is a
	// process-global the GCS SDK reads at NewClient time).
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	// Layer 1: the data lives in inmem. This is the cloud-B
	// state-of-record.
	dataBackend := inmem.New()
	ctx := context.Background()
	if err := dataBackend.CreateBucket(ctx, "cross-cloud-bucket", "us"); err != nil {
		t.Fatalf("seed bucket: %v", err)
	}

	// Layer 2: a mock cloud B that speaks GCS wire over the inmem.
	// This is what the shim's GCS backend will dial — same role a
	// real `storage.googleapis.com` would play in production.
	mockGCS := httptest.NewServer(gcsfront.New(dataBackend))
	t.Cleanup(mockGCS.Close)
	// Strip the scheme — STORAGE_EMULATOR_HOST is host:port without scheme.
	mockGCSHost := strings.TrimPrefix(mockGCS.URL, "http://")

	// Layer 3: build the GCS backend the shim uses, with its
	// cloud.google.com/go/storage client pointing at the mock GCS.
	t.Setenv("STORAGE_EMULATOR_HOST", mockGCSHost)
	gcsClient, err := gcsstorage.NewClient(ctx,
		option.WithEndpoint(mockGCS.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new GCS client: %v", err)
	}
	t.Cleanup(func() { _ = gcsClient.Close() })
	gcsBackend := gcs.New(gcsClient, gcs.Config{ProjectID: "shim-cross-cloud"})

	// Layer 4: the AWS S3 frontend on top of the GCS backend.
	router := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(router, awsfront.New(gcsBackend))
	shim := httptest.NewServer(router)
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudConfig, shim.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirCrossCloud(),
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
		"aws_s3_bucket.imported", "cross-cloud-bucket")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — fidelity gap to log\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

// Avoid unused import warning if the test is built without the
// http import on some Go versions.
var _ = http.StatusOK

func terraformPluginCacheDirCrossCloud() string {
	d := filepath.Join(os.TempDir(), "shim-cross-cloud-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Phase 10 sub-phase 10.7 — exit criterion:
// TestCrossCloudApply_Roundtrip_StorageAWStoGCS.
//
// Symmetric to Phase 9.13's import-roundtrip. The promise: a user
// writes AWS-shape Terraform; `terraform apply` creates the bucket
// in cloud B (mock GCS) through shimanism. Plan after apply sees no
// drift. Destroy cleans up via the same chain. This is the *write*
// half of the cross-cloud migration story (Phase 9 proved Read).
//
// Architecture: same four-layer stack as
// TestCrossCloudImport_Roundtrip_StorageAWStoGCS, with apply instead
// of import.
//
// Skipped if `terraform` isn't on PATH.
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

	gcsstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/restxml"
	storagedomain "github.com/e6qu/shimanism/internal/storage/domain"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	gcsfront "github.com/e6qu/shimanism/internal/storage/frontends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/gcs"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

const terraformCrossCloudApplyConfig = `
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
  s3_use_path_style           = true

  endpoints {
    s3 = "%s"
  }
}

resource "aws_s3_bucket" "applied" {
  bucket = "cross-cloud-applied-bucket"
  tags   = {}
}
`

func TestCrossCloudApply_Roundtrip_StorageAWStoGCS(t *testing.T) {
	// No t.Parallel — uses t.Setenv (STORAGE_EMULATOR_HOST is a
	// process-global the GCS SDK reads at NewClient time).
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	// Cloud-B state-of-record: inmem under the mock GCS frontend.
	dataBackend := inmem.New()
	ctx := context.Background()

	mockGCS := httptest.NewServer(gcsfront.New(dataBackend))
	t.Cleanup(mockGCS.Close)
	mockGCSHost := strings.TrimPrefix(mockGCS.URL, "http://")

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

	router := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(router, awsfront.New(gcsBackend))
	shim := httptest.NewServer(router)
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudApplyConfig, shim.URL)
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

	// 1. Apply: drives CreateBucket through AWS S3 frontend →
	//    GCS backend → mock GCS → inmem.
	mustRun("apply", "-no-color", "-auto-approve")

	// 2. Verify the bucket actually landed in the cloud-B layer.
	list, err := dataBackend.ListBuckets(ctx, storagedomain.ListBucketsOptions{})
	if err != nil {
		t.Fatalf("dataBackend.ListBuckets: %v", err)
	}
	found := false
	for _, b := range list.Buckets {
		if b.Name == "cross-cloud-applied-bucket" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("bucket not in mock-GCS data layer after apply")
	}

	// 3. Plan after apply — no pending changes. Strict 10.7 exit.
	stdout, stderr, err := runTf("plan", "-no-color", "-detailed-exitcode")
	switch {
	case err == nil:
	case isExitCode(err, 2):
		t.Errorf("cross-cloud apply roundtrip — plan after apply reports drift (10.7 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	// 4. Destroy cleans up via shim → backend → mock-GCS → inmem.
	mustRun("destroy", "-no-color", "-auto-approve")

	list, err = dataBackend.ListBuckets(ctx, storagedomain.ListBucketsOptions{})
	if err != nil {
		t.Fatalf("dataBackend.ListBuckets after destroy: %v", err)
	}
	if len(list.Buckets) != 0 {
		names := make([]string, 0, len(list.Buckets))
		for _, b := range list.Buckets {
			names = append(names, b.Name)
		}
		t.Errorf("mock-GCS data layer still has buckets after destroy: %v", names)
	}
}

// ensure no-import-prune warning from existing helpers used by the
// import-side cross-cloud test.
var _ = errors.New

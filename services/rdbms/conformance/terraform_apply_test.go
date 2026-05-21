// Phase 10 sub-phase 10.2: terraform apply drift audit for rdbms.
//
// Contract: services/rdbms/APPLY_INTERSECTION.md.
//
// AWS cell: diamond-skipped — aws_db_instance reconciles via
// ModifyDBInstance + waits on subnet-group / parameter-group /
// option-group / security-group metadata not in the intersection
// (same class as BUG-2; the Phase 5 TF test already documented this).
//
// GCP cell: BUG-5 was the gate (Operations.Get polling); closed in
// Phase 10.1. This test exercises the now-working apply lifecycle.
//
// Azure cell: ARM ProvisioningState long-polling has known
// provider-side requirements; deferred to Track A like Phase 5's TF
// cell.
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
	rdbmsdomain "github.com/e6qu/shimanism/internal/rdbms/domain"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

const terraformApplyRDBMSGCPConfig = `
terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 5.0"
    }
  }
}

provider "google" {
  project                  = "shim-conformance"
  region                   = "us-central1"
  access_token             = "shim-fake-token"
  sql_custom_endpoint      = "%s/"
}

resource "google_sql_database_instance" "applied" {
  name                = "tf-applied-rdbms"
  region              = "us-central1"
  database_version    = "POSTGRES_15"
  deletion_protection = false

  settings {
    tier = "db-custom-1-3840"
  }
}
`

func TestTerraform_AWSRDBMS_Apply_NoDrift(t *testing.T) {
	t.Skip("aws_db_instance reconciles via ModifyDBInstance + needs subnet-group/parameter-group/option-group metadata (same class as BUG-2; Phase 5 posture carried into Apply)")
}

func TestTerraform_GCPRDBMS_Apply_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartRDBMSServerGCP(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyRDBMSGCPConfig, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirRDBMS(),
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
	mustRun("apply", "-no-color", "-auto-approve")

	stdout, stderr, err := runTf("plan", "-no-color", "-detailed-exitcode")
	switch {
	case err == nil:
	case isExitCodeRDBMSApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	got, err := backend.ListInstances(context.Background(), rdbmsdomain.ListInstancesOptions{})
	if err != nil {
		t.Fatalf("backend.ListInstances after destroy: %v", err)
	}
	if len(got.Instances) != 0 {
		names := make([]string, 0, len(got.Instances))
		for _, in := range got.Instances {
			names = append(names, in.Name)
		}
		t.Errorf("backend still has instances after destroy: %v", names)
	}
}

func TestTerraform_AzureRDBMS_Apply_NoDrift(t *testing.T) {
	t.Skip("Azure ARM ProvisioningState long-polling deferred to Track A (matches Phase 5 posture)")
}

func isExitCodeRDBMSApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

// Phase 10 sub-phase 10.2: terraform apply drift audit for apigateway.
//
// Contract: services/apigateway/APPLY_INTERSECTION.md.
//
// AWS frontend cell: drives the apply → plan -detailed-exitcode →
// destroy cycle. The existing Phase 8 TF test (aws_terraform_test.go)
// runs init + apply + destroy without a drift assertion; this test
// adds the strict no-pending-changes check.
//
// GCP frontend cell: diamond-skipped per BUG-8 (Phase 8 baseline —
// API Gateway endpoint-override + OAuth-signed requests against a
// mock server).
//
// Azure frontend cell: diamond-skipped per BUG-6 (v3 SDK delete
// signature; APIClient.Delete returns InvalidArgument).
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
	"time"

	apigwdomain "github.com/e6qu/shimanism/internal/apigateway/domain"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

const terraformApplyAPIGWAWSConfig = `
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

resource "aws_apigatewayv2_api" "applied" {
  name          = "shim-applied-apigw"
  protocol_type = "HTTP"
}
`

func TestTerraform_AWSAPIGateway_Apply_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartAPIGatewayServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyAPIGWAWSConfig, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirAPIGW(),
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
	case isExitCodeAPIGWApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	// inmem.DeleteGateway is async (marks Deleting, then goroutine
	// removes after a delay) — poll briefly for the eventual empty
	// state. Real backends (AWS / GCP / Azure) are also async, so
	// this matches the production posture.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, err := backend.ListGateways(context.Background(), apigwdomain.ListGatewaysOptions{})
		if err != nil {
			t.Fatalf("backend.ListGateways after destroy: %v", err)
		}
		if len(got.Gateways) == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	got, _ := backend.ListGateways(context.Background(), apigwdomain.ListGatewaysOptions{})
	names := make([]string, 0, len(got.Gateways))
	for _, g := range got.Gateways {
		names = append(names, g.Name)
	}
	t.Errorf("backend still has gateways after destroy + poll: %v", names)
}

func TestTerraform_GCPAPIGateway_Apply_NoDrift(t *testing.T) {
	t.Skip("BUG-8: API Gateway endpoint-override + OAuth-signed requests against mock server (Phase 8 baseline; Track A coverage)")
}

func TestTerraform_AzureAPIGateway_Apply_NoDrift(t *testing.T) {
	// BUG-6 closed: armapimanagement/v3 APIClient.BeginDelete is
	// wired with ifMatch="*" + nil DeleteRevisions; poller awaited
	// to completion. Active drift assertion against the Azure
	// backend is gated on Track A (real Azure account); the inmem-
	// backend cell remains the active cross-cloud destination.
	t.Skip("real-Azure backend conformance gated on Track A; BUG-6 (delete signature) closed in the backend itself")
}

func isExitCodeAPIGWApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

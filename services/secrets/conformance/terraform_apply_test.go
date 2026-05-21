// Phase 10 sub-phase 10.2: terraform apply drift audit for secrets.
//
// Drives the write path:
//
//   1. terraform apply against the shim → drives CreateSecret +
//      PutSecretValue.
//   2. terraform plan -detailed-exitcode → drives the Read path
//      against the just-created state. Exit code 0 means no
//      pending changes; exit code 2 means the Create-then-Read
//      cycle produced a state the provider wants to modify, which
//      is a fidelity gap.
//   3. terraform destroy → cleans up via DeleteSecret.
//
// Contract: services/secrets/APPLY_INTERSECTION.md. The HCL only
// drives in-contract attributes (name + description). Tags are
// in-contract but omitted at this scaffolding stage because the AWS
// frontend's tag write path round-trips honestly via tags = {}; the
// dedicated tags-round-trip test is part of 10.5.
//
// recovery_window_in_days = 0 keeps Phase 10.2 isolated from
// soft-delete (which 10.4 owns).
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
	secretsdomain "github.com/e6qu/shimanism/internal/secrets/domain"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

const terraformApplySecretsConfig = `
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
    secretsmanager = "%s"
  }
}

resource "aws_secretsmanager_secret" "applied" {
  name                    = "shim-applied-secret"
  description             = "phase 10.2 apply drift audit"
  recovery_window_in_days = 0
}
`

func TestTerraform_AWSSecrets_Apply_NoDrift(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartSecretsServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplySecretsConfig, srv.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDir(),
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
	case isExitCodeSecretsApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	// Sanity check the backend is empty.
	got, err := backend.ListSecrets(context.Background(), secretsdomain.ListSecretsOptions{})
	if err != nil {
		t.Fatalf("backend.ListSecrets after destroy: %v", err)
	}
	if len(got.Secrets) != 0 {
		names := make([]string, 0, len(got.Secrets))
		for _, s := range got.Secrets {
			names = append(names, s.Name)
		}
		t.Errorf("backend still has secrets after destroy: %v", names)
	}
}

func isExitCodeSecretsApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

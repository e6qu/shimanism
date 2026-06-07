// Conformance: AWS ELBv2-shaped frontend exercised by the official
// hashicorp/aws Terraform provider. Uses an `endpoints { elbv2 = "..." }`
// override + dummy credentials so the provider doesn't reach real AWS.
// Skipped if the `terraform` binary isn't on PATH.
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
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

const terraformAWSELBv2Config = `
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
    elbv2 = "%s"
  }
}

resource "aws_lb" "shim" {
  name               = "tf-alb"
  load_balancer_type = "application"
  internal           = false

  # Dummy subnet — the shim doesn't validate VPC topology.
  subnets = ["subnet-00000000000000001"]
}

resource "aws_lb_target_group" "shim" {
  name        = "tf-alb-tg"
  port        = 80
  protocol    = "HTTP"
  target_type = "ip"
  vpc_id      = "vpc-00000000000000001"
}
`

func requireTerraformForELBv2(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	return bin
}

func runTerraformELBv2(t *testing.T, dir, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	// Per-workdir plugin cache: a shared package-level cache races when
	// tests run with t.Parallel() during concurrent `terraform init`.
	cacheDir := filepath.Join(dir, ".terraform-plugin-cache")
	_ = os.MkdirAll(cacheDir, 0o755)
	cmd.Env = append(os.Environ(),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"CHECKPOINT_DISABLE=1",
		"TF_PLUGIN_CACHE_DIR="+cacheDir,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestTerraform_ELBv2_ALBLifecycle creates an aws_lb (type=application) +
// aws_lb_target_group via the hashicorp/aws provider pointed at the shim,
// then destroys both resources.
func TestTerraform_ELBv2_ALBLifecycle(t *testing.T) {
	t.Parallel()
	tfBin := requireTerraformForELBv2(t)
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())

	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformAWSELBv2Config, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	mustRun := func(args ...string) {
		t.Helper()
		stdout, stderr, err := runTerraformELBv2(t, dir, tfBin, args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
	}

	mustRun("init", "-no-color")
	mustRun("apply", "-auto-approve", "-no-color")
	// plan -refresh=false verifies no structural drift after apply.
	mustRun("plan", "-no-color", "-refresh=false")
	mustRun("destroy", "-auto-approve", "-no-color")
}

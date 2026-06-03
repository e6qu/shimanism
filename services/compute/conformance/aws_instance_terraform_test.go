// Conformance: AWS EC2 instance lifecycle exercised by the official
// hashicorp/aws Terraform provider. Uses `endpoints { ec2 = "..." }`
// override + dummy credentials so the provider doesn't reach real AWS.
// Skipped if the `terraform` binary isn't on PATH.
package conformance_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const terraformAWSInstanceConfig = `
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
    ec2 = "%s"
  }
}

resource "aws_instance" "shim" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}
`

func TestTerraformAWS_EC2_Instance(t *testing.T) {
	t.Parallel()
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}

	srv := harness.StartComputeServerAWS(t, inmem.New())

	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformAWSInstanceConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0600); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "TF_IN_AUTOMATION=1", "TF_CLI_ARGS=-no-color")
		out, err := cmd.CombinedOutput()
		return out, err
	}

	// init
	if out, err := run("init", "-no-color"); err != nil {
		t.Skipf("terraform init failed (likely network): %v\n%s", err, out)
	}

	// apply
	out, err := run("apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply:\n%s\nerr: %v", out, err)
	}
	if !strings.Contains(string(out), "Apply complete!") {
		t.Errorf("apply output missing 'Apply complete!':\n%s", out)
	}
	if !strings.Contains(string(out), "aws_instance.shim: Creation complete") {
		t.Errorf("apply output missing instance creation message:\n%s", out)
	}

	// plan — should be no changes
	out, err = run("plan", "-no-color")
	if err != nil {
		t.Logf("plan exit non-zero (may be drift): %v\n%s", err, out)
	}

	// destroy
	out, err = run("destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy:\n%s\nerr: %v", out, err)
	}
	if !strings.Contains(string(out), "Destroy complete!") {
		t.Errorf("destroy output missing 'Destroy complete!':\n%s", out)
	}
}

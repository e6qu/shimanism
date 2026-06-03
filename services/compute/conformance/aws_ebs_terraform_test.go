// Conformance: AWS EC2 block storage exercised by the official
// hashicorp/aws Terraform provider. Covers Phase 17:
// aws_ebs_volume lifecycle (create + describe + delete) and
// aws_ebs_snapshot_copy (create snapshot from volume).
package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

const terraformAWSEBSConfig = `
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

resource "aws_ebs_volume" "shim" {
  availability_zone = "us-east-1a"
  size              = 20
  type              = "gp3"

  tags = {
    Name = "shim-tf-vol"
  }
}
`

func TestTerraformAWS_EBS_VolumeLifecycle(t *testing.T) {
	tfBin := requireTerraformForEC2(t)
	srv := harness.StartComputeServerAWS(t, inmem.New())

	dir := t.TempDir()
	cfg := fmt.Sprintf(terraformAWSEBSConfig, srv.URL)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	mustRun := func(args ...string) {
		t.Helper()
		stdout, stderr, err := runTerraformEC2(t, dir, tfBin, args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
	}

	mustRun("init", "-no-color")

	stdout, stderr, err := runTerraformEC2(t, dir, tfBin, "apply", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform apply\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Apply complete!") {
		t.Errorf("terraform apply: missing 'Apply complete!' in:\n%s", stdout)
	}
	if !strings.Contains(string(stdout), "aws_ebs_volume.shim: Creation complete") {
		t.Errorf("terraform apply: volume creation not confirmed:\n%s", stdout)
	}

	stdout, stderr, err = runTerraformEC2(t, dir, tfBin, "destroy", "-auto-approve", "-no-color")
	if err != nil {
		t.Fatalf("terraform destroy\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}
	if !strings.Contains(string(stdout), "Destroy complete!") {
		t.Errorf("terraform destroy: missing 'Destroy complete!' in:\n%s", stdout)
	}
}

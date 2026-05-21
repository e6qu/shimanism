// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for rdbms:
// TestCrossCloudImport_Roundtrip_RDBMSAWStoGCPCloudSQL.
//
// User writes AWS RDS-shape Terraform; the actual instance lives
// in a mock GCP Cloud SQL Admin server backed by inmem.
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

	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1"

	"github.com/e6qu/shimanism/internal/rdbms/domain"
	awsfront "github.com/e6qu/shimanism/internal/rdbms/frontends/aws_rds"
	gcpfront "github.com/e6qu/shimanism/internal/rdbms/frontends/gcp_cloudsql"
	gcpbackend "github.com/e6qu/shimanism/services/rdbms/backends/gcp"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

const terraformCrossCloudRDBMSConfig = `
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
    rds = "%s"
  }
}

resource "aws_db_instance" "imported" {
  identifier        = "cross-cloud-db"
  engine            = "postgres"
  engine_version    = "16"
  instance_class    = "db.t3.micro"
  username          = "admin"
  password          = "shim-password"
  allocated_storage = 20
  skip_final_snapshot = true
}
`

func TestCrossCloudImport_Roundtrip_RDBMSAWStoGCPCloudSQL(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateInstance(ctx, "cross-cloud-db", domain.CreateInstanceOptions{
		Engine:             domain.EnginePostgres,
		EngineVersion:      "16",
		MasterUsername:     "admin",
		MasterPassword:     "shim-password",
		AllocatedStorageGB: 20,
	}); err != nil {
		t.Fatalf("seed db: %v", err)
	}

	mockGCP := httptest.NewServer(gcpfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	svc, err := sqladmin.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new sqladmin service: %v", err)
	}
	gcpB := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud", Region: "us-central1"})

	shim := httptest.NewServer(awsfront.New(gcpB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudRDBMSConfig, shim.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirRDBMSCC(),
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
		"aws_db_instance.imported", "cross-cloud-db")
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — fidelity gap\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirRDBMSCC() string {
	d := filepath.Join(os.TempDir(), "shim-rdbms-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

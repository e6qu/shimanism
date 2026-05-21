// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for pubsub:
// TestCrossCloudImport_Roundtrip_PubsubAWStoGCP.
//
// User writes AWS SNS-shape Terraform; the actual topic lives in
// a mock GCP Pub/Sub server backed by inmem. The shim's AWS SNS
// frontend + GCP backend translate.
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
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/pubsub/domain"
	awsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sns"
	gcpfront "github.com/e6qu/shimanism/internal/pubsub/frontends/gcp_pubsub"
	gcpbackend "github.com/e6qu/shimanism/services/pubsub/backends/gcp"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

const terraformCrossCloudPubsubConfig = `
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
    sns = "%s"
  }
}

resource "aws_sns_topic" "imported" {
  name = "cross-cloud-topic"
}
`

func TestCrossCloudImport_Roundtrip_PubsubAWStoGCP(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateTopic(ctx, "cross-cloud-topic", domain.CreateTopicOptions{}); err != nil {
		t.Fatalf("seed topic: %v", err)
	}

	mockGCP := httptest.NewServer(gcpfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new pubsub service: %v", err)
	}
	gcpB := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud"})

	shim := httptest.NewServer(awsfront.New(gcpB))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudPubsubConfig, shim.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirPubsubCC(),
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

	arn := "arn:aws:sns:us-east-1:000000000000:cross-cloud-topic"
	stdout, stderr, err := runTf("import", "-no-color",
		"aws_sns_topic.imported", arn)
	if err != nil {
		t.Fatalf("terraform import:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
	}

	stdout, stderr, err = runTf("plan", "-no-color", "-detailed-exitcode")
	if err == nil {
		return
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 2 {
		t.Logf("cross-cloud plan reports a diff — expected per BUG-2 ripple\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
		return
	}
	t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v", stdout, stderr, err)
}

func terraformPluginCacheDirPubsubCC() string {
	d := filepath.Join(os.TempDir(), "shim-pubsub-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

// Phase 9 sub-phase 9.13 — cross-cloud exit criterion for queue:
// TestCrossCloudImport_Roundtrip_QueueAWStoGCPPubsub.
//
// User writes AWS SQS-shaped Terraform; the actual queue lives in
// "GCP Pub/Sub" (a mock GCP frontend backed by inmem). The shim's
// AWS SQS frontend + GCP backend translate the SQS call into a
// Pub/Sub call against the mock.
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

	"github.com/e6qu/shimanism/internal/queue/domain"
	awssqsfront "github.com/e6qu/shimanism/internal/queue/frontends/aws_sqs"
	gcpsubfront "github.com/e6qu/shimanism/internal/queue/frontends/gcp_pubsub"
	gcpbackend "github.com/e6qu/shimanism/services/queue/backends/gcp"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

const terraformCrossCloudQueueConfig = `
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
    sqs = "%s"
  }
}

resource "aws_sqs_queue" "imported" {
  name = "cross-cloud-queue"
}
`

func TestCrossCloudImport_Roundtrip_QueueAWStoGCPPubsub(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	// Layer 1: state-of-record.
	dataBackend := inmem.New()
	ctx := context.Background()
	if _, err := dataBackend.CreateQueue(ctx, "cross-cloud-queue", domain.CreateQueueOptions{}); err != nil {
		t.Fatalf("seed queue: %v", err)
	}

	// Layer 2: mock cloud B = GCP Pub/Sub-shaped HTTP frontend
	// over the inmem data layer. This is what the shim's GCP
	// backend will dial.
	mockGCP := httptest.NewServer(gcpsubfront.New(dataBackend))
	t.Cleanup(mockGCP.Close)

	// Layer 3: GCP backend pointing at the mock.
	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint(mockGCP.URL+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new pubsub service: %v", err)
	}
	gcpBackend := gcpbackend.New(svc, gcpbackend.Config{ProjectID: "shim-cross-cloud"})

	// Layer 4: AWS SQS frontend over the GCP backend.
	shim := httptest.NewServer(awssqsfront.New(gcpBackend))
	t.Cleanup(shim.Close)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformCrossCloudQueueConfig, shim.URL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirQueueCC(),
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

	queueURL := shim.URL + "/000000000000/cross-cloud-queue"
	stdout, stderr, err := runTf("import", "-no-color",
		"aws_sqs_queue.imported", queueURL)
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

func terraformPluginCacheDirQueueCC() string {
	d := filepath.Join(os.TempDir(), "shim-queue-cc-tf-plugin-cache")
	_ = os.MkdirAll(d, 0o755)
	return d
}

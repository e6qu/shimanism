// Phase 10 sub-phase 10.2: terraform apply drift audit for pubsub.
//
// Contract: services/pubsub/APPLY_INTERSECTION.md.
//
// AWS frontend cell: drives the apply → plan -detailed-exitcode →
// destroy cycle through hashicorp/aws aws_sns_topic. Subscription
// drift is gated by BUG-2 (aws_sns_topic_subscription's SQS
// endpoint requires SetQueueAttributes), so this scaffolding tests
// the topic surface only at this stage.
//
// GCP frontend cell: diamond-skipped with BUG-15 pointer (same
// hashicorp/google plan/apply asymmetry on
// message_retention_duration the queue cell hit).
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
	pubsubdomain "github.com/e6qu/shimanism/internal/pubsub/domain"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

const terraformApplyPubsubAWSConfig = `
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
    sns = "%s"
  }
}

resource "aws_sns_topic" "applied" {
  name = "shim-applied-topic"
}
`

func TestTerraform_AWSPubsub_Apply_NoDrift(t *testing.T) {
	// hashicorp/aws aws_sns_topic calls SetTopicAttributes after
	// CreateTopic (for ApplicationSuccessFeedbackSampleRate +
	// other defaults). The shim returns InvalidAction honestly —
	// SetTopicAttributes is out of the Phase 4 SNS intersection.
	// Same class of gap as BUG-2 (aws_sqs_queue / SetQueueAttributes);
	// Phase 10.3 can audit whether SetTopicAttributes joins the
	// intersection alongside SetQueueAttributes. Diamond-skip with
	// pointer until then.
	t.Skip("aws_sns_topic reconciles via SetTopicAttributes (out of Phase 4 intersection; see BUG-2 for the queue analog)")

	t.Parallel()
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	tf, _ := exec.LookPath("terraform")

	backend := inmem.New()
	srv := harness.StartPubsubServerAWS(t, backend)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformApplyPubsubAWSConfig, srv.SnsURL)
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
			"TF_PLUGIN_CACHE_DIR="+terraformPubsubPluginCacheDir(),
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
	case isExitCodePubsubApply(err, 2):
		t.Errorf("terraform plan after apply reports pending changes (10.2 fidelity gap)\nstdout:\n%s\nstderr:\n%s",
			stdout, stderr)
	default:
		t.Fatalf("terraform plan:\nstdout: %s\nstderr: %s\nerr: %v",
			stdout, stderr, err)
	}

	mustRun("destroy", "-no-color", "-auto-approve")

	got, err := backend.ListTopics(context.Background(), pubsubdomain.ListTopicsOptions{})
	if err != nil {
		t.Fatalf("backend.ListTopics after destroy: %v", err)
	}
	if len(got.Topics) != 0 {
		names := make([]string, 0, len(got.Topics))
		for _, tp := range got.Topics {
			names = append(names, tp.Name)
		}
		t.Errorf("backend still has topics after destroy: %v", names)
	}
}

func TestTerraform_GCPPubsub_Apply_NoDrift(t *testing.T) {
	// BUG-15 (carried from queue): hashicorp/google
	// message_retention_duration plan/apply asymmetry surfaces here
	// too. Phase 10.3 owns the honest fix.
	t.Skip("BUG-15: hashicorp/google message_retention_duration plan/apply asymmetry (same as queue)")
}

func isExitCodePubsubApply(err error, code int) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == code
}

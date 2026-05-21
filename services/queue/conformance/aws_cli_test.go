// Phase 3 conformance: AWS SQS-shaped frontend exercised by the
// official `aws sqs` CLI. Each command shells out against the shim's
// endpoint. Skipped if the `aws` binary isn't on PATH; CI's main lane
// has it preinstalled.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWSSQS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_DEFAULT_REGION=us-east-1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestAWSCLI_QueueLifecycle(t *testing.T) {
	aws := requireAWSCLI(t)
	srv := harness.StartQueueServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSSQS(t, srv.URL, aws, args...)
		if err != nil {
			t.Fatalf("aws %s failed: %v\nstderr: %s\nstdout: %s",
				strings.Join(args, " "), err, stderr, stdout)
		}
		return stdout
	}

	out := mustRun("sqs", "create-queue", "--queue-name", "cli-orders")
	var created struct {
		QueueUrl string `json:"QueueUrl"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode create-queue: %v\nbody: %s", err, out)
	}
	if created.QueueUrl == "" {
		t.Fatalf("create-queue: empty QueueUrl in %s", out)
	}

	mustRun("sqs", "send-message",
		"--queue-url", created.QueueUrl,
		"--message-body", "cli-payload")

	out = mustRun("sqs", "receive-message",
		"--queue-url", created.QueueUrl,
		"--max-number-of-messages", "1",
		"--wait-time-seconds", "5")
	var rcv struct {
		Messages []struct {
			Body          string `json:"Body"`
			ReceiptHandle string `json:"ReceiptHandle"`
		} `json:"Messages"`
	}
	if err := json.Unmarshal(out, &rcv); err != nil {
		t.Fatalf("decode receive-message: %v\nbody: %s", err, out)
	}
	if len(rcv.Messages) != 1 {
		t.Fatalf("receive-message: count=%d, want 1\nbody: %s", len(rcv.Messages), out)
	}
	if rcv.Messages[0].Body != "cli-payload" {
		t.Errorf("body = %q, want cli-payload", rcv.Messages[0].Body)
	}

	mustRun("sqs", "change-message-visibility",
		"--queue-url", created.QueueUrl,
		"--receipt-handle", rcv.Messages[0].ReceiptHandle,
		"--visibility-timeout", "60")

	mustRun("sqs", "delete-message",
		"--queue-url", created.QueueUrl,
		"--receipt-handle", rcv.Messages[0].ReceiptHandle)

	out = mustRun("sqs", "list-queues")
	if !bytes.Contains(out, []byte("cli-orders")) {
		t.Errorf("list-queues did not contain cli-orders\nbody: %s", out)
	}

	mustRun("sqs", "delete-queue", "--queue-url", created.QueueUrl)
}

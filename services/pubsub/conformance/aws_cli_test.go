// Phase 4 conformance: AWS SNS+SQS-shaped pubsub fanout exercised
// by the `aws sns` + `aws sqs` CLIs. Each command shells out
// against the shim's matching endpoint. Skipped if the `aws`
// binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWS(t *testing.T, endpoint, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + endpoint, "--no-cli-pager"}, args...)...)
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

func TestAWSCLI_PubsubFanout(t *testing.T) {
	bin := requireAWSCLI(t)
	srv := harness.StartPubsubServerAWS(t, inmem.New())

	mustSNS := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWS(t, srv.SnsURL, bin, args...)
		if err != nil {
			t.Fatalf("aws %s failed: %v\nstderr: %s", strings.Join(args, " "), err, stderr)
		}
		return stdout
	}
	mustSQS := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWS(t, srv.SqsURL, bin, args...)
		if err != nil {
			t.Fatalf("aws %s failed: %v\nstderr: %s", strings.Join(args, " "), err, stderr)
		}
		return stdout
	}

	out := mustSNS("sns", "create-topic", "--name", "cli-orders")
	var created struct {
		TopicArn string `json:"TopicArn"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("decode create-topic: %v", err)
	}

	queueArn := "arn:aws:sqs:us-east-1:000000000000:cli-orders-sub"
	mustSNS("sns", "subscribe",
		"--topic-arn", created.TopicArn,
		"--protocol", "sqs",
		"--notification-endpoint", queueArn)

	mustSNS("sns", "publish",
		"--topic-arn", created.TopicArn,
		"--message", "cli-fanout-payload")

	queueURL := srv.SqsURL + "/000000000000/cli-orders-sub"
	out = mustSQS("sqs", "receive-message",
		"--queue-url", queueURL,
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
		t.Fatalf("receive-message count = %d, want 1\nbody: %s", len(rcv.Messages), out)
	}
	if rcv.Messages[0].Body != "cli-fanout-payload" {
		t.Errorf("body = %q, want cli-fanout-payload", rcv.Messages[0].Body)
	}

	mustSQS("sqs", "delete-message",
		"--queue-url", queueURL,
		"--receipt-handle", rcv.Messages[0].ReceiptHandle)
	mustSQS("sqs", "delete-queue", "--queue-url", queueURL)
	mustSNS("sns", "delete-topic", "--topic-arn", created.TopicArn)
}

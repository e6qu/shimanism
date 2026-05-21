// Phase 4 conformance: GCP Pub/Sub-shaped fanout exercised by the
// `gcloud pubsub` CLI. Same endpoint-override mechanism as Phase 3.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/pubsub/backends/inmem"
)

func requireGcloud(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runGcloud(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://pubsub.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_PUBSUB="+full,
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+jwt,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
		"CLOUDSDK_CORE_PROJECT=shim-conformance",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_PubsubFanout(t *testing.T) {
	gcloud := requireGcloud(t)
	srv := harness.StartPubsubServerGCP(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloud(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("pubsub", "topics", "create", "cli-orders")
	for _, sub := range []string{"cli-orders-a", "cli-orders-b"} {
		mustRun("pubsub", "subscriptions", "create", sub,
			"--topic=cli-orders", "--ack-deadline=30")
	}
	mustRun("pubsub", "topics", "publish", "cli-orders",
		"--message=cli-fanout-payload")

	for _, sub := range []string{"cli-orders-a", "cli-orders-b"} {
		out := mustRun("pubsub", "subscriptions", "pull", sub,
			"--limit=1", "--auto-ack")
		if !bytes.Contains(out, []byte("cli-fanout-payload")) {
			t.Errorf("%s pull output did not contain cli-fanout-payload:\n%s", sub, out)
		}
	}

	for _, sub := range []string{"cli-orders-a", "cli-orders-b"} {
		mustRun("pubsub", "subscriptions", "delete", sub)
	}
	mustRun("pubsub", "topics", "delete", "cli-orders")
}

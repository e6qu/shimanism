// Phase 3 conformance: GCP Pub/Sub-shaped frontend exercised by the
// official `gcloud pubsub` CLI. Same endpoint-override mechanism as
// the Phase 2 secrets CLI test.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/queue/backends/inmem"
)

func requireGcloudQueue(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runGcloudPubsub(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_PUBSUB="+full,
		"CLOUDSDK_AUTH_DISABLE_CREDENTIALS=true",
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
		"CLOUDSDK_CORE_PROJECT=shim-conformance",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_QueueLifecycle(t *testing.T) {
	gcloud := requireGcloudQueue(t)
	srv := harness.StartQueueServerGCP(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudPubsub(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("pubsub", "topics", "create", "cli-orders")
	mustRun("pubsub", "subscriptions", "create", "cli-orders",
		"--topic=cli-orders", "--ack-deadline=30")

	mustRun("pubsub", "topics", "publish", "cli-orders",
		"--message=cli-payload")

	out := mustRun("pubsub", "subscriptions", "pull", "cli-orders",
		"--limit=1", "--auto-ack")
	if !bytes.Contains(out, []byte("cli-payload")) {
		t.Errorf("pull output did not contain cli-payload:\n%s", out)
	}

	mustRun("pubsub", "subscriptions", "delete", "cli-orders")
	mustRun("pubsub", "topics", "delete", "cli-orders")
}

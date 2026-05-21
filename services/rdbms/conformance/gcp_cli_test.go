// Phase 5 conformance: GCP Cloud SQL Admin-shaped frontend
// exercised by `gcloud sql instances`. Skipped if `gcloud` isn't
// on PATH.
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
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func requireGcloud(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed: %v", err)
	}
	return bin
}

func runGcloudSQL(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://sqladmin.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_SQL="+full,
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_SQLADMIN="+full,
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

func TestGCPCLI_RDBMSLifecycle(t *testing.T) {
	// gcloud sql instances create issues an `instances.insert` op +
	// polls operations.get until done. The shim returns a PENDING
	// Operation envelope without an operations endpoint, so the CLI
	// hangs waiting for polling. Document as deferred — SDK +
	// matrix cells cover the GCP frontend.
	t.Skip("gcloud sql instances create polls operations.get; the shim's frontend doesn't implement the Operations endpoint at this phase. SDK + matrix cells cover this combination.")

	gcloud := requireGcloud(t)
	srv := harness.StartRDBMSServerGCP(t, inmem.New())

	if _, _, err := runGcloudSQL(t, srv.URL, gcloud,
		"sql", "instances", "create", "cli-test",
		"--database-version", "POSTGRES_15",
		"--tier", "db-perf-optimized-N-2",
		"--region", "us-central1"); err != nil {
		t.Fatalf("gcloud sql instances create: %v", err)
	}
}

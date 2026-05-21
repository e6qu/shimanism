// Phase 2 conformance: GCP Secret Manager-shaped frontend exercised
// by the official `gcloud secrets` CLI.
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
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

func requireGcloud(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runGcloudSecrets(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://secretmanager.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_SECRETMANAGER="+full,
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

func TestGCPCLI_SecretLifecycle(t *testing.T) {
	gcloud := requireGcloud(t)
	srv := harness.StartSecretsServerGCP(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudSecrets(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// gcloud secrets create reads value from --data-file=- on stdin.
	// We write the value to a temp file and pass --data-file=...
	tmp := t.TempDir()
	srcFile := tmp + "/value"
	if err := os.WriteFile(srcFile, []byte("hello-gcloud"), 0o644); err != nil {
		t.Fatalf("write source value: %v", err)
	}
	mustRun("secrets", "create", "cli-token", "--data-file="+srcFile)

	out := mustRun("secrets", "versions", "access", "latest", "--secret=cli-token")
	if got := strings.TrimSpace(string(out)); got != "hello-gcloud" {
		t.Errorf("access output = %q, want hello-gcloud", got)
	}

	mustRun("secrets", "delete", "cli-token")
}

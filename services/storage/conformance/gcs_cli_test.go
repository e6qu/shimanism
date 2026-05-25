// Phase 1.14 conformance: GCS-shaped frontend exercised by the
// official `gcloud storage` CLI. The CLI is pointed at the shim via
// the CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE env var; credentials
// are disabled because the shim does not validate them at this
// phase.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

func requireGcloud(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

// runGcloud executes gcloud against the shim. CLOUDSDK_AUTH_ACCESS_TOKEN
// carries the JWT the gcpbearer verifier accepts (signed for the
// storage audience), so the bearer middleware lets requests through
// end-to-end. With BUG-18 closure, the shim requires real auth.
func runGcloud(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://storage.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{
		"--quiet",
	}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE="+full,
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

func TestGCS_CLI_BucketLifecycle(t *testing.T) {
	gcloud := requireGcloud(t)
	srv := harness.StartStorageServerGCS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloud(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("storage", "buckets", "create", "gs://alpha")
	mustRun("storage", "buckets", "create", "gs://beta")

	out := mustRun("storage", "buckets", "list", "--format=value(name)")
	names := strings.Fields(string(out))
	if len(names) != 2 {
		t.Fatalf("buckets list = %v, want 2", names)
	}

	mustRun("storage", "buckets", "describe", "gs://alpha", "--format=value(name)")
	mustRun("storage", "buckets", "delete", "gs://alpha")
	mustRun("storage", "buckets", "delete", "gs://beta")
}

func TestGCS_CLI_ObjectRoundTrip(t *testing.T) {
	gcloud := requireGcloud(t)
	srv := harness.StartStorageServerGCS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloud(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("storage", "buckets", "create", "gs://data")

	tmp := t.TempDir()
	src := filepath.Join(tmp, "in.txt")
	dst := filepath.Join(tmp, "out.txt")
	body := []byte("hello shimanism, via gcloud")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	mustRun("storage", "cp", src, "gs://data/file.txt")
	mustRun("storage", "cp", "gs://data/file.txt", dst)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("cli round-trip body = %q, want %q", got, body)
	}
	mustRun("storage", "rm", "--recursive", "gs://data")
}

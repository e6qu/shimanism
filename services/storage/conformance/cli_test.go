package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// requireAWS returns the path to the `aws` CLI binary, or skips the
// test if it isn't installed. CI installs it; dev environments can
// install ad hoc.
func requireAWS(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

// runAWS executes the AWS CLI against the shim with deterministic env
// (credentials and region pinned so the CLI does not pick up the
// developer's real shell credentials).
func runAWS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_DEFAULT_REGION=us-east-1",
		// Force path-style addressing; the harness lives on a random
		// localhost port and virtual-hosted style would resolve
		// "bucket.localhost".
		"AWS_S3_FORCE_PATH_STYLE=true",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestCLI_BucketLifecycle exercises CreateBucket / ListBuckets /
// HeadBucket / DeleteBucket through the official `aws` CLI. Each
// command is a real exec; failure modes (incompatible signing, wrong
// XML namespace, etc.) surface as non-zero exits with stderr text.
func TestCLI_BucketLifecycle(t *testing.T) {
	aws := requireAWS(t)
	srv := harness.StartStorageServer(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWS(t, srv.URL, aws, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("s3api", "create-bucket", "--bucket", "alpha")
	mustRun("s3api", "create-bucket", "--bucket", "beta")

	out := mustRun("s3api", "list-buckets")
	var lb struct {
		Buckets []struct{ Name string } `json:"Buckets"`
	}
	if err := json.Unmarshal(out, &lb); err != nil {
		t.Fatalf("list-buckets JSON: %v\n%s", err, out)
	}
	if got, want := len(lb.Buckets), 2; got != want {
		t.Fatalf("list-buckets count = %d, want %d", got, want)
	}

	mustRun("s3api", "head-bucket", "--bucket", "alpha")
	mustRun("s3api", "delete-bucket", "--bucket", "alpha")
	mustRun("s3api", "delete-bucket", "--bucket", "beta")
}

// TestCLI_ObjectRoundTrip uploads and downloads an object through the
// CLI. cp uses the higher-level transfer manager which under the hood
// may use multipart for larger files; we keep this small so it stays
// single-part and any multipart fallout is contained in
// TestSDK_Multipart.
func TestCLI_ObjectRoundTrip(t *testing.T) {
	aws := requireAWS(t)
	srv := harness.StartStorageServer(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWS(t, srv.URL, aws, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("s3api", "create-bucket", "--bucket", "data")

	tmp := t.TempDir()
	src := filepath.Join(tmp, "in.txt")
	dst := filepath.Join(tmp, "out.txt")
	body := []byte("hello shimanism, from the cli")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	mustRun("s3", "cp", src, "s3://data/file.txt")
	mustRun("s3", "cp", "s3://data/file.txt", dst)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("cli round-trip body = %q, want %q", got, body)
	}
}

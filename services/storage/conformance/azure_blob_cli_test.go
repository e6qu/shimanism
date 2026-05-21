// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `az storage blob` CLI. The CLI is pointed at the
// shim via the `--blob-endpoint` flag plus a synthetic
// account-name + account-key pair (the shim does not validate
// signatures at this phase).
package conformance_test

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// SharedKey credentials the azuresharedkey verifier accepts.
// Account name and raw key live in internal/harness/server.go's
// StartStorageServerAzureBlob; az takes a base64-encoded key.
var (
	azAccountName = "shimstorage"
	azAccountKey  = base64.StdEncoding.EncodeToString([]byte("test-key-do-not-use-in-prod-this-is-32-bytes-of-junk"))
)

func requireAZ(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAZ(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	endpoint := strings.TrimRight(srvURL, "/") + "/" + azAccountName
	// Subcommand first, global flags last. az is sensitive to flag
	// order: arg-order parsing can otherwise treat trailing tokens as
	// the subcommand and then complain about an unrecognized one.
	full := append([]string{}, args...)
	full = append(full,
		"--account-name", azAccountName,
		"--account-key", azAccountKey,
		"--blob-endpoint", endpoint,
		"--only-show-errors",
		"--output", "json",
	)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(),
		"AZURE_CORE_NO_COLOR=true",
		// Older az on GitHub-hosted runners declines without telemetry
		// settings being explicitly opted out — disable to keep CI
		// deterministic.
		"AZURE_CORE_COLLECT_TELEMETRY=no",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestAzureBlob_CLI_ContainerLifecycle(t *testing.T) {
	az := requireAZ(t)
	srv := harness.StartStorageServerAzureBlob(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAZ(t, srv.URL, az, args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("storage", "container", "create", "--name", "alpha")
	mustRun("storage", "container", "create", "--name", "beta")

	// `az` returns the list-containers result as a JSON array; ask for
	// length(@) so we don't have to disambiguate tsv vs json output.
	out := mustRun("storage", "container", "list", "--query", "length(@)")
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Errorf("container list count = %q, want 2", got)
	}

	mustRun("storage", "container", "delete", "--name", "alpha")
	mustRun("storage", "container", "delete", "--name", "beta")
}

func TestAzureBlob_CLI_BlobRoundTrip(t *testing.T) {
	az := requireAZ(t)
	srv := harness.StartStorageServerAzureBlob(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAZ(t, srv.URL, az, args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("storage", "container", "create", "--name", "data")

	tmp := t.TempDir()
	src := filepath.Join(tmp, "in.txt")
	dst := filepath.Join(tmp, "out.txt")
	body := []byte("hello shimanism, via az cli")
	if err := os.WriteFile(src, body, 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	mustRun("storage", "blob", "upload",
		"--container-name", "data",
		"--name", "file.txt",
		"--file", src,
	)
	mustRun("storage", "blob", "download",
		"--container-name", "data",
		"--name", "file.txt",
		"--file", dst,
	)

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("cli round-trip body = %q, want %q", got, body)
	}
}

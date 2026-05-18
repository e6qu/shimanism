// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `az storage blob` CLI. The CLI is pointed at the
// shim via the `--blob-endpoint` flag plus a synthetic
// account-name + account-key pair (the shim does not validate
// signatures at this phase).
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/storage/backends/inmem"
)

// az's well-known Azurite-style credentials. The shim accepts any
// SharedKey signature; these values give az's signer something to
// work with so it doesn't refuse to construct a request.
const (
	azAccountName = "devstoreaccount1"
	azAccountKey  = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
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
	full := append([]string{
		"--output", "json",
		"--only-show-errors",
	}, args...)
	full = append(full,
		"--account-name", azAccountName,
		"--account-key", azAccountKey,
		"--blob-endpoint", endpoint,
	)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(),
		"AZURE_CORE_NO_COLOR=true",
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

	out := mustRun("storage", "container", "list", "--query", "[].name", "-o", "tsv")
	names := strings.Fields(string(out))
	if len(names) != 2 {
		t.Errorf("container list = %v, want 2", names)
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

// Phase 2 conformance: Azure Key Vault-shaped frontend exercised by
// the official `az keyvault secret` CLI.
//
// Skipped if the `az` binary isn't on PATH; CI's main lane has it
// preinstalled. Also skipped locally if the harness runs the
// frontend without TLS (the Azure SDK / az CLI refuses to send
// bearer tokens over plain HTTP).
package conformance_test

import (
	"bytes"
	"crypto/tls"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

func requireAzCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAzSecret(t *testing.T, vaultURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := append([]string{}, args...)
	full = append(full,
		"--vault-name", "shim", // az parses the host out of --vault-name; we
		// override AZURE_KEYVAULT_RESOURCE for the auth challenge.
		"--only-show-errors",
		"--output", "json",
	)
	cmd := exec.Command(bin, full...)
	cmd.Env = append(os.Environ(),
		"AZURE_KEYVAULT_RESOURCE=https://vault.azure.net",
		// az pulls credentials from the standard chain; for the shim
		// (which doesn't validate the token) any non-empty token works.
		"AZURE_CLIENT_ID=shim-conformance",
		"AZURE_TENANT_ID=shim",
		"AZURE_CLIENT_SECRET=shim",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	_ = vaultURL // az resolves the URL from --vault-name only; per-host override is not exposed at this phase
	return stdout.Bytes(), stderr.Bytes(), err
}

// TestAzureCLI_SecretLifecycle wires the az keyvault secret CLI
// against the shim. The Azure CLI insists on resolving the vault
// URL from the storage-account / vault-name + a fixed Azure suffix
// (e.g. `<vault>.vault.azure.net`), which can't be redirected
// without `/etc/hosts` surgery or a DNS shim. We Skip with a
// documented reason — the SDK + Terraform rows cover the cell.
func TestAzureCLI_SecretLifecycle(t *testing.T) {
	t.Skip("az keyvault secret resolves the vault URL from --vault-name + a fixed Azure suffix (e.g. `<name>.vault.azure.net`); redirecting the data plane requires DNS-level hooks the test harness can't install. SDK + TF cells cover this driver-backend combination.")

	az := requireAzCLI(t)
	srv := harness.StartSecretsServerAzure(t, inmem.New())

	// Keep the unreferenced helpers below valid so the Skip case
	// still compiles when the test is re-enabled.
	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAzSecret(t, srv.URL, az, args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("keyvault", "secret", "set",
		"--name", "cli-token",
		"--value", "hello-az")

	// Avoid unused-import lint when the test is skipped.
	_ = &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec
}

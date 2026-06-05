// Conformance: Azure Key Vault keys data-plane frontend and the official
// `az keyvault key` CLI.
//
// **Known upstream constraint.** The `az` CLI resolves the Key Vault
// data-plane host from `--vault-name` plus a fixed Azure suffix
// (`<name>.vault.azure.net`); it exposes no per-host / data-plane
// endpoint override. Redirecting the data plane to the shim would need
// root-level DNS surgery (an `/etc/hosts` entry for the vault hostname +
// the shim on :443), which the test harness can't install. This is the
// same wall the secrets service hit (`az keyvault secret`). The SDK cell
// (`azure_sdk_test.go`) and the through-shim sockerless lane
// (`TestSockerless_AzureKVKeys_Through_Shim`) cover this frontend with
// real Azure tooling. Re-enable if `az` adds a data-plane endpoint
// override.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_KMS_KeyLifecycle(t *testing.T) {
	t.Skip("az keyvault key resolves the vault URL from --vault-name + the fixed `<name>.vault.azure.net` suffix with no data-plane endpoint override; redirecting it to the shim needs root-level DNS hooks the harness can't install. The azkeys SDK cell + the Azure KV-keys sockerless lane cover this driver-backend combination.")

	// Keep the binary lookup referenced so this compiles if re-enabled.
	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

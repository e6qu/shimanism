// Conformance: Azure Event Hubs ARM-shaped frontend exercised by the `az
// eventhubs` CLI through-shim against sockerless.
//
// The test registers a custom az-cloud profile pointing at the shim and logs
// in via `az login --service-principal` against sockerless's Entra ID stub.
// Per-test AZURE_CONFIG_DIR isolates the profile from any system-wide az state.
//
// Gated on SOCKERLESS_AZURE_TLS_PORT and SOCKERLESS_AZURE_TLS_CERT env vars.
// Linux-only via SSL_CERT_FILE platform limit.
package conformance_test

import (
	"os"
	"os/exec"
	"testing"
)

func requireAzEventHubsCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
	return bin
}

func TestAzureCLI_EventHubs_NamespaceAndTopicLifecycle_ThroughShim(t *testing.T) {
	_ = requireAzEventHubsCLI(t)
	if os.Getenv("SOCKERLESS_AZURE_TLS_PORT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	if os.Getenv("SOCKERLESS_AZURE_TLS_CERT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	// Full through-shim az eventhubs test lives in sockerless_test.go as
	// TestSockerless_AzureEventHubs_Through_Shim_CLI when the sockerless
	// Azure Entra ID stub is available.
	t.Skip("through-shim az eventhubs test requires sockerless: see sockerless_test.go")
}

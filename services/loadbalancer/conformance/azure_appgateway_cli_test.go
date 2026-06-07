// Conformance: Azure Application Gateway-shaped frontend exercised by
// the official `az network application-gateway` CLI.
//
// Full end-to-end Azure CLI tests require:
//   - az CLI binary on PATH
//   - SOCKERLESS_AZURE_TLS_PORT set (sockerless Entra stub reachable)
//   - SOCKERLESS_AZURE_TLS_CERT set (PEM path for sockerless's TLS cert)
//   - A system CA bundle present (Linux-only via SSL_CERT_FILE)
//
// Without sockerless the test is skipped with a clear diagnostic.
// The azure_lb frontend has no Config/passthrough variant yet
// (no HandlerWithConfig), so full end-to-end Terraform-style auth flow
// is deferred until that gap is closed.
package conformance_test

import (
	"os"
	"os/exec"
	"testing"
)

func requireAzCLIForLB(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
	return bin
}

// TestAzureCLI_LB_AppGatewayLifecycle exercises az network
// application-gateway commands against the shim. Skipped unless sockerless
// is available; the Azure az CLI requires a real Entra auth flow that
// depends on the sockerless Entra stub and a passthrough-capable shim
// frontend (the azure_lb HandlerWithConfig variant, not yet implemented).
func TestAzureCLI_LB_AppGatewayLifecycle(t *testing.T) {
	_ = requireAzCLIForLB(t)

	if os.Getenv("SOCKERLESS_AZURE_TLS_PORT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set — Azure CLI tests require sockerless Entra stub; skipping")
	}
	if os.Getenv("SOCKERLESS_AZURE_TLS_CERT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set — Azure CLI tests require sockerless TLS cert; skipping")
	}

	// The azure_lb frontend does not yet expose HandlerWithConfig
	// (passthrough + MetadataLoginURL + BearerOptions), which is required
	// to funnel the az CLI's Entra token acquisition through sockerless.
	// Once that variant is available this test can follow the same pattern
	// as azure_instances_cli_test.go in the compute conformance suite:
	//   1. Reverse-proxy to sockerless ARM for resource-group / auth paths.
	//   2. az cloud register --endpoint-resource-manager <shim>
	//   3. az login --service-principal
	//   4. az network application-gateway create / show / list / delete
	t.Skip("azure_lb HandlerWithConfig not yet implemented — test body deferred")
}

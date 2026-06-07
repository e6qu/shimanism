// Conformance: Azure Application Gateway lifecycle exercised by the
// official `hashicorp/azurerm` Terraform provider.
//
// Full end-to-end Terraform apply requires:
//   - terraform binary on PATH
//   - SOCKERLESS_AZURE_TLS_PORT set (sockerless Entra stub reachable)
//   - SOCKERLESS_AZURE_TLS_CERT set (PEM path for sockerless's TLS cert)
//   - A system CA bundle present (Linux-only via SSL_CERT_FILE)
//
// Without sockerless the test is skipped with a clear diagnostic.
// The azure_lb frontend has no Config/passthrough variant yet
// (no HandlerWithConfig), so the full azurerm provider auth flow
// through the shim is deferred until that gap is closed.
//
// Once HandlerWithConfig is available this test should follow the pattern
// from TestSockerless_AzureCompute_Through_Shim_Terraform_Apply in the
// compute sockerless_test.go: ARM passthrough to sockerless + bearer
// verifier + azurerm_application_gateway resource.
package conformance_test

import (
	"os"
	"os/exec"
	"testing"
)

// TestTerraform_AzureAppGateway_Lifecycle creates an
// azurerm_application_gateway resource via the hashicorp/azurerm provider
// pointed at the shim. Skipped unless sockerless is available; the azurerm
// provider requires a real Entra auth flow that depends on the sockerless
// Entra stub and a passthrough-capable shim frontend (the azure_lb
// HandlerWithConfig variant, not yet implemented).
func TestTerraform_AzureAppGateway_Lifecycle(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}

	if os.Getenv("SOCKERLESS_AZURE_TLS_PORT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set — Azure Terraform tests require sockerless Entra stub; skipping")
	}
	if os.Getenv("SOCKERLESS_AZURE_TLS_CERT") == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set — Azure Terraform tests require sockerless TLS cert; skipping")
	}

	// The azure_lb frontend does not yet expose HandlerWithConfig
	// (passthrough + MetadataLoginURL + BearerOptions), which is required
	// to funnel the azurerm provider's Entra token acquisition through
	// sockerless. Once that variant is available the test body should:
	//   1. Parse sockerless TLS cert + fetch JWKS.
	//   2. Start shim with HandlerWithConfig (passthrough → sockerless,
	//      MetadataLoginURL = sockerless ARM, JWKS = fetched above).
	//   3. Build combined CA bundle (system + sockerless cert + shim cert).
	//   4. Write main.tf with azurerm provider + azurerm_application_gateway.
	//   5. terraform init / apply -auto-approve / destroy -auto-approve.
	t.Skip("azure_lb HandlerWithConfig not yet implemented — test body deferred")
}

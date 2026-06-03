// Conformance: Azure Compute-shaped frontend for the official
// `hashicorp/azurerm` Terraform provider with `azurerm_linux_virtual_machine`.
//
// BLOCKED: The azurerm provider requires an ARM metadata endpoint
// (CloudShellToken / IMDS) and an Entra token issuer that signs JWTs
// with the tenant's JWKS. The shim's azure_compute frontend uses a fixed
// test-key verifier; feeding it a real Entra-signed token from azurerm
// requires either real Azure credentials (CI doesn't have them) or a
// local Entra stub (sockerless) with the shim configured to trust its
// JWKS. The latter mirrors BUG-44 (DNS Azure Terraform).
//
// BUG-56: implement azurerm_linux_virtual_machine Terraform conformance
// once the shim's azure_compute frontend supports configurable JWKS-based
// bearer verification (same prereq as BUG-44 for DNS).
package conformance_test

import "testing"

func TestTerraformAzure_Compute_VMLifecycle(t *testing.T) {
	t.Skip("blocked on BUG-56: azurerm provider needs configurable JWKS bearer verification in azure_compute frontend")
}

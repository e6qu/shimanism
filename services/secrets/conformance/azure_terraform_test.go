// Phase 2 conformance: Azure Key Vault-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Known upstream constraint.** hashicorp/azurerm reads the Key
// Vault data-plane URL from the ARM `azurerm_key_vault` resource's
// `vault_uri` attribute, which the provider discovers from the
// Azure Resource Manager control plane (which we do not shim at
// this phase). There is no `vault_custom_endpoint` or equivalent
// provider-level data-plane override.
//
// SDK + CLI cells cover this driver-backend combination already.
// Re-enable when azurerm exposes a data-plane endpoint override.
package conformance_test

import "testing"

func TestTerraform_AzureSecrets_ResourceLifecycle(t *testing.T) {
	t.Skip("blocked by hashicorp/azurerm: no provider-level override for the Key Vault data-plane endpoint (derived from azurerm_key_vault.vault_uri via ARM, which is not shimmed at this phase). SDK + CLI cells cover this driver-backend combination; enable when azurerm adds a vault_custom_endpoint or similar.")
}

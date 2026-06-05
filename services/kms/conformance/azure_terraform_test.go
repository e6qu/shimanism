// Conformance: Azure Key Vault keys data-plane frontend and the official
// `hashicorp/azurerm` Terraform provider (`azurerm_key_vault_key`).
//
// **Known upstream constraint.** hashicorp/azurerm reads the Key Vault
// data-plane URL from the ARM `azurerm_key_vault` resource's `vault_uri`
// attribute, which the provider discovers from the Azure Resource
// Manager control plane (not shimmed by this data-plane service). There
// is no `vault_custom_endpoint` or equivalent provider-level data-plane
// override, so the `azurerm_key_vault_key` data-plane calls can't be
// redirected to the shim. This is the identical wall the secrets service
// hit (`azurerm_key_vault_secret`). The azkeys SDK cell and the
// through-shim sockerless lane (`TestSockerless_AzureKVKeys_Through_Shim`)
// cover this frontend. Re-enable when azurerm exposes a data-plane
// endpoint override.
package conformance_test

import "testing"

func TestTerraform_AzureKMS_KeyLifecycle(t *testing.T) {
	t.Skip("blocked by hashicorp/azurerm: no provider-level override for the Key Vault data-plane endpoint (azurerm_key_vault_key derives vault_uri from azurerm_key_vault via ARM, which this data-plane service does not shim). The azkeys SDK cell + the Azure KV-keys sockerless lane cover this driver-backend combination; enable when azurerm adds a vault_custom_endpoint or similar.")
}

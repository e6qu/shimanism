// Phase 2 conformance: Azure Key Vault-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Unblocked 14.E.5 (2026-05-28).** The original constraint was
// that hashicorp/azurerm derives the data-plane URL from the ARM
// `azurerm_key_vault.vault_uri` attribute, which the shim couldn't
// shim. PR #52 added the Microsoft.KeyVault ARM frontend; PR #54
// added mock-Microsoft-Entra for the auth flow. The replacement
// test lives in azurerm_apply_test.go
// (TestCrossCloudApply_Roundtrip_KeyVaultAzureToAWS). This file
// stays as a redirect-stub so the original test name continues to
// exist (no orphaned skip messages in CI dashboards).
package conformance_test

import "testing"

func TestTerraform_AzureSecrets_ResourceLifecycle(t *testing.T) {
	t.Skip("superseded by TestCrossCloudApply_Roundtrip_KeyVaultAzureToAWS (azurerm_apply_test.go); 14.E.5 unblocked the through-shim Terraform Apply for Key Vault.")
}

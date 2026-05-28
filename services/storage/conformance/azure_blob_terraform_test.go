// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Mechanism in place (14.E.3, 2026-05-28).** The previous
// constraint was that azurerm 4.x derives the blob endpoint from
// `azurerm_storage_account.primary_blob_endpoint`, which is fetched
// from ARM — and the shim didn't speak ARM. PR #51 + this PR's
// blob-endpoint-propagation change close that gap: the shim's new
// Microsoft.Storage ARM frontend now returns the shim's blob
// frontend URL in `properties.primaryEndpoints.blob`, so an
// azurerm provider pointed at the shim's ARM endpoint discovers
// the blob endpoint correctly. The mechanism is verified by
// `TestSockerless_E2E_AzureARM_StorageAccount_Through_Shim`'s
// PrimaryEndpoints.Blob assertion.
//
// **Remaining gap: azurerm auth.** The provider needs a credential
// to exchange for an ARM bearer token. The standard modes (CLI,
// service principal with client secret, managed identity, OIDC)
// all hit Microsoft Entra (Azure AD), which the shim doesn't
// shim. To enable this test we either need (a) a mock AAD endpoint
// the shim serves and azurerm trusts, or (b) a provider option
// that accepts a static bearer token directly. The SDK row
// (TestAzureBlob_SDK_* — green) and CLI row (TestAzureBlob_CLI_* —
// when `az` is on PATH) cover this driver-backend combination via
// alternative auth.
package conformance_test

import (
	"testing"
)

func TestTerraform_AzureBlob_ResourceLifecycle(t *testing.T) {
	t.Skip("ARM endpoint discovery mechanism is now in place (PR #51 + the 14.E.3 blob-endpoint propagation) but azurerm auth still routes through Microsoft Entra which the shim doesn't currently shim. SDK + CLI cells cover this driver-backend combination via alternative auth; enable when a mock-AAD or static-bearer mode is wired up.")
}

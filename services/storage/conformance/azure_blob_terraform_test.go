// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Known upstream constraint.** azurerm 4.x reads the blob endpoint
// for `azurerm_storage_blob` (and friends) from the ARM
// `azurerm_storage_account` resource's `primary_blob_endpoint`
// attribute, which the provider discovers from the Azure Resource
// Manager control plane. There is no provider-level option to
// override the blob endpoint independently — it's derived from the
// storage account's location and the configured environment.
//
// We do not run the ARM control plane through the shim (storage is
// the data plane; account provisioning is out of scope for the
// storage shim). That means a pure-azurerm Terraform test cannot
// point at the shim's blob frontend without first running a full
// Azure-Resource-Manager-shaped service through the shim — which
// belongs to a future phase (account / IAM / control-plane shims).
//
// The driver-backend cell this would cover is exercised today via:
//   - the SDK row (TestAzureBlob_SDK_* — green)
//   - the CLI row (TestAzureBlob_CLI_* — runs when `az` is on PATH)
//
// When azurerm exposes a `storage_account_custom_endpoint` (or
// similar), enable this test.
package conformance_test

import (
	"testing"
)

func TestTerraform_AzureBlob_ResourceLifecycle(t *testing.T) {
	t.Skip("blocked by hashicorp/azurerm: no provider-level override for the blob data-plane endpoint (derived from azurerm_storage_account.primary_blob_endpoint via ARM). SDK + CLI cells cover this driver-backend combination; enable when azurerm adds storage_account_custom_endpoint.")
}

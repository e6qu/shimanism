// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Architecture note.** hashicorp/azurerm derives the blob endpoint
// from `azurerm_storage_account.primary_blob_endpoint`, which the
// provider fetches from the Azure Resource Manager control plane.
// shimanism deliberately does NOT shim ARM — that would require
// account/vault routing-fiction + in-process state in the shim,
// violating shimanism's "no fakes" and "stateless shim" rules
// (see AGENTS.md). The right home for Azure ARM is sockerless's
// Azure simulator, which models real ARM state and (post
// sockerless#259) emits configurable data-plane endpoint URLs
// pointing at the shim. The through-shim Apply path then composes:
// `azurerm → sockerless ARM → primaryEndpoints.blob pointing at the
// shim → shim's azure_blob frontend → backend`.
//
// This Terraform-driven test stays skipped until that path is wired
// (sockerless deployment configured via `SIM_AZURE_ARM_EXTERNAL_
// DATA_PLANE_URLS_JSON` to advertise the shim's URL). SDK + CLI
// cells cover this driver-backend combination today via direct
// endpoint overrides.
package conformance_test

import (
	"testing"
)

func TestTerraform_AzureBlob_ResourceLifecycle(t *testing.T) {
	t.Skip("Through-shim azurerm Apply requires sockerless ARM with configurable data-plane endpoint emission (sockerless#259, landed; wiring pending). SDK + CLI cells cover this driver-backend combination via direct endpoint overrides.")
}

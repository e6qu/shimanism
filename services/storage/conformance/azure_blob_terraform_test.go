// Phase 1.15 conformance: Azure Blob-shaped frontend exercised by
// the official `hashicorp/azurerm` Terraform provider.
//
// **Superseded by `TestSockerless_E2E_AzureBlob_Through_Shim_ApplyTF`**
// in sockerless_test.go (2026-05-28). That cell drives the honest
// cross-cloud Apply path end-to-end via sockerless's real Azure ARM
// (sockerless#259 + #260 + #262), no shim-side ARM fakes. This file
// stays as a redirect-stub so the original test name continues to
// exist (no orphaned skip messages in CI dashboards).
package conformance_test

import (
	"testing"
)

func TestTerraform_AzureBlob_ResourceLifecycle(t *testing.T) {
	t.Skip("superseded by TestSockerless_E2E_AzureBlob_Through_Shim_ApplyTF (sockerless_test.go); that cell exercises the honest azurerm-→-sockerless-ARM-→-shim path.")
}

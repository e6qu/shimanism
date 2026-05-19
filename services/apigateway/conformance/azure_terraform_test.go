// Phase 8 conformance: Azure APIM-shaped frontend exercised by
// `hashicorp/azurerm`. The azurerm provider doesn't expose a
// per-service endpoint override for APIM (it composes ARM URLs
// from the resource_manager_endpoint). This conformance lane is
// the matrix placeholder.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzureAPIGateway_Smoke(t *testing.T) {
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed (PATH lookup failed: %v)", err)
	}
	// hashicorp/azurerm doesn't currently expose a per-resource
	// endpoint override that targets only APIM. Tracked in BUGS.md
	// alongside the az-CLI gap.
	t.Skip("azurerm per-resource endpoint override for APIM not yet wired (see BUGS.md)")
}

// Phase 5 conformance: Azure DB Admin frontend — `hashicorp/azurerm`
// Terraform provider cell.
//
// **Documented skip.** `azurerm_postgresql_flexible_server` polls
// Azure-AsyncOperation URLs the shim's frontend doesn't emit at
// this phase. Same posture as the Azure cells in Phases 3 + 4.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzureRDBMS_ResourceLifecycle(t *testing.T) {
	t.Skip("azurerm_postgresql_flexible_server polls Azure-AsyncOperation URLs the shim doesn't emit at this phase. SDK + matrix cells cover this driver-backend combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

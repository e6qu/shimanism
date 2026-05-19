// Phase 7 conformance: hashicorp/azurerm azurerm_container_app — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzureFunctions_ResourceLifecycle(t *testing.T) {
	t.Skip("azurerm_container_app polls Azure-AsyncOperation URLs the shim doesn't emit at this phase.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

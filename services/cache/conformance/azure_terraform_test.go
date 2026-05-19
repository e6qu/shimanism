// Phase 6 conformance: hashicorp/azurerm azurerm_redis_cache — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzureCache_ResourceLifecycle(t *testing.T) {
	t.Skip("azurerm_redis_cache polls Azure-AsyncOperation URLs the shim doesn't emit at this phase.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

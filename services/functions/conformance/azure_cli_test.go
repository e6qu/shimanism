// Phase 7 conformance: az containerapp CLI cell — ◇ documented skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_FunctionsLifecycle(t *testing.T) {
	t.Skip("az containerapp create polls Azure-AsyncOperation URLs the shim doesn't emit at this phase. SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

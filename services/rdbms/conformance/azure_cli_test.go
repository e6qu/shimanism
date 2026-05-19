// Phase 5 conformance: Azure DB Admin REST frontend — `az postgres
// flexible-server` CLI cell.
//
// **Documented skip.** Same posture as the Azure cells in earlier
// phases — `az` drives provisioning via ARM with long-running
// operations indicated by the `Azure-AsyncOperation` header. The
// shim's frontend doesn't emit that header at this phase; `az` hangs
// waiting for polling. SDK + matrix cells cover this combination.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_RDBMSLifecycle(t *testing.T) {
	t.Skip("az postgres flexible-server polls Azure-AsyncOperation URLs the shim doesn't emit at this phase. SDK + matrix cells cover this driver-backend combination.")

	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

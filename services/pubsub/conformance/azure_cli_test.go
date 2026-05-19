// Phase 4 conformance: Azure Service Bus topics — `az servicebus`
// CLI cell.
//
// **Documented skip.** Same posture as Phase 3 — `az servicebus`
// drives topic/subscription admin through Azure Resource Manager
// (subscription / resource-group / namespace scopes — ARM, not the
// Service Bus data plane) and message operations through AMQP. The
// shim's Azure frontend exposes only the REST data plane; the CLI
// can't drive it without ARM stubbing plus an AMQP fidelity tier,
// both deferred per the PLAN.md Phase 3 open question.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_PubsubFanout(t *testing.T) {
	t.Skip("az servicebus drives topic + subscription admin via ARM (not the shim's REST data plane) and message ops via AMQP. SDK + raw-HTTP REST cells cover this driver-backend combination.")

	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

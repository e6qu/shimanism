// Phase 3 conformance: Azure Service Bus — `az servicebus` CLI cell.
//
// **Documented skip.** `az servicebus` drives queue admin through
// Azure Resource Manager (subscription / resource-group / namespace
// scopes — ARM, not the Service Bus data plane) and message ops
// through AMQP. The shim's Azure frontend exposes the REST
// data-plane surface only; the CLI therefore cannot drive it
// without ARM stubbing plus an AMQP fidelity tier, both deferred
// per the PLAN.md Phase 3 open question.
//
// The SDK + Terraform cells for this driver-backend combination
// (and the raw-HTTP REST matrix) cover correctness; the cell is
// marked skipped per the Phase 1+2 convention.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_QueueLifecycle(t *testing.T) {
	t.Skip("az servicebus drives queue admin via ARM (not the shim's REST data plane) and message ops via AMQP. The Azure frontend exposes the REST data plane only; AMQP tier is deferred per PLAN.md open question. SDK + raw-HTTP REST cells cover this driver-backend combination.")

	// Keep the binary lookup compiled so re-enabling the test only
	// requires removing the Skip above.
	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

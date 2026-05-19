// Phase 3 conformance: Azure Service Bus — `hashicorp/azurerm`
// Terraform provider cell.
//
// **Documented skip.** azurerm's `azurerm_servicebus_queue` resource
// is an ARM-driven control plane operation (queue admin happens
// under a Service Bus *namespace* scoped by subscription + resource
// group), not a Service Bus data-plane call. The shim's Azure
// frontend exposes the REST data plane only; the ARM management
// surface is out of scope for this phase. Same posture as the
// Phase 1+2 azurerm rows.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzureQueue_ResourceLifecycle(t *testing.T) {
	t.Skip("azurerm_servicebus_queue is ARM-control-plane (namespace-scoped, subscription/resource-group context). The shim's Azure frontend exposes Service Bus REST data plane only; ARM surface is out of scope for Phase 3. SDK + raw-HTTP REST cells cover the driver-backend combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

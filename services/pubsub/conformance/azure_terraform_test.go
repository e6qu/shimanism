// Phase 4 conformance: Azure Service Bus topics — `hashicorp/azurerm`
// Terraform provider cell.
//
// **Documented skip.** `azurerm_servicebus_topic` +
// `azurerm_servicebus_subscription` are ARM-driven control plane
// resources (namespace + resource group scoping). The shim's
// Azure pubsub frontend exposes only the REST data plane; ARM
// surface is out of scope for Phase 4. Same posture as the
// Phase 3 azurerm row.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AzurePubsub_ResourceLifecycle(t *testing.T) {
	t.Skip("azurerm_servicebus_topic + _subscription are ARM-control-plane resources (namespace + RG scoping). The shim's Azure frontend exposes Service Bus REST data plane only.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

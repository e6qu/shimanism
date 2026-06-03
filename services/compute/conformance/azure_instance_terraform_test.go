// Conformance: Azure Compute-shaped frontend for the official
// `hashicorp/azurerm` Terraform provider with `azurerm_linux_virtual_machine`.
//
// BLOCKED: `azurerm_linux_virtual_machine` requires network_interface_ids
// referencing an azurerm_network_interface, which requires azurerm_subnet /
// azurerm_virtual_network. These live under Microsoft.Network — a separate
// frontend (azure_network). A single-server Terraform test would need to
// combine azure_compute + azure_network on one TLS listener, or use a full
// ARM passthrough to sockerless for the network resources.
//
// The azure_compute frontend now has HandlerWithConfig + metadata endpoint
// (BUG-56 infrastructure is complete). The remaining gap is the combined
// compute+network server for the Terraform provider's full HCL lifecycle.
// This mirrors DNS BUG-44: azure_dns TF is also deferred for the same
// reason.
//
// BUG-56: implement azurerm_linux_virtual_machine Terraform conformance
// once a combined compute+network TLS server is available for Terraform.
package conformance_test

import "testing"

func TestTerraformAzure_Compute_VMLifecycle(t *testing.T) {
	t.Skip("BUG-56: azurerm_linux_virtual_machine requires network_interface (Microsoft.Network) resources not available on the azure_compute-only TLS server; needs combined compute+network server or full ARM passthrough")
}

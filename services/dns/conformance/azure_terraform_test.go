// Conformance: Azure DNS-shaped frontend exercised by the official
// `hashicorp/azurerm` Terraform provider.
//
// **Known upstream constraint.** `hashicorp/azurerm`'s
// `azurerm_dns_zone` requires an `azurerm_resource_group` and depends
// on ARM operations the shim doesn't stub at this phase (subscription
// Get, resource-group lifecycle, provider-registration polling). The
// SDK + sockerless cells cover this driver-backend combination
// already. Re-enable when the shim ships an ARM resource-group +
// subscription stub or sockerless's ARM surface is wired through.
package conformance_test

import "testing"

func TestTerraform_AzureDNS_ZoneLifecycle(t *testing.T) {
	t.Skip("blocked by hashicorp/azurerm: requires azurerm_resource_group + subscription/ARM operations the shim doesn't stub at this phase. SDK cell covers the same driver-backend combination.")
}

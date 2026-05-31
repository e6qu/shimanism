// Conformance: Azure DNS-shaped frontend exercised by the official
// `hashicorp/azurerm` Terraform provider.
//
// Tracked as BUG-44: shim needs ARM passthrough mode so `azurerm`'s
// `azurerm_resource_group` (and subscription operations) can route
// to sockerless's existing ARM mock while DNS-specific paths stay on
// the shim's Azure DNS frontend.
package conformance_test

import "testing"

func TestTerraform_AzureDNS_ZoneLifecycle(t *testing.T) {
	t.Skip("BUG-44: Azure DNS Terraform needs shim ARM passthrough so azurerm's resource-group + subscription calls reach sockerless's ARM mock while DNS-specific calls stay on the shim. Sockerless coverage exists; gap is shim test wiring.")
}

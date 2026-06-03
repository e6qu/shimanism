// Conformance: Azure DNS-shaped frontend for the official `hashicorp/azurerm`
// Terraform provider with `azurerm_dns_zone` and `azurerm_dns_a_record`.
//
// The full end-to-end Terraform test (apply + destroy) lives in
// sockerless_test.go as TestSockerless_AzureDNS_Through_Shim_Terraform_Apply.
// That test exercises the shim's azure_dns frontend with the azurerm provider
// in ARM passthrough mode — DNS paths handled locally, resource groups
// forwarded to sockerless. Closed BUG-46 (passthrough primitive) and
// BUG-44 (ARM passthrough mode for Terraform).
//
// This file is a placeholder for non-sockerless Terraform tests.
package conformance_test

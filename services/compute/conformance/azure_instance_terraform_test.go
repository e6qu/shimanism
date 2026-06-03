// Conformance: Azure Compute-shaped frontend for the official
// `hashicorp/azurerm` Terraform provider with `azurerm_linux_virtual_machine`.
//
// The full end-to-end Terraform test (apply + destroy) lives in
// sockerless_test.go as TestSockerless_AzureCompute_Through_Shim_Terraform_Apply.
// That test exercises the shim's azure_compute frontend with the azurerm
// provider in ARM passthrough mode — Microsoft.Compute paths handled locally
// (inmem), resource groups + Microsoft.Network (VNet/Subnet/NIC) forwarded
// to sockerless. Closes BUG-56.
//
// This file is a placeholder for non-sockerless Terraform tests (e.g. against
// real Azure, or against a future standalone fixture).
package conformance_test

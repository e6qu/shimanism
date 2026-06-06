// Conformance: Azure Event Hubs ARM-shaped frontend for the official
// hashicorp/azurerm Terraform provider.
//
// The full end-to-end Terraform test (apply + destroy) lives in
// sockerless_test.go as TestSockerless_AzureEventHubs_Through_Shim_Terraform_Apply.
// That test exercises the shim's azure_eventhubs frontend with the azurerm
// provider in ARM passthrough mode — Event Hubs paths handled locally,
// resource groups forwarded to sockerless.
//
// This file is a placeholder for non-sockerless Terraform tests.
package conformance_test

// Conformance: Azure Compute-shaped frontend exercised by the official
// `az vm` CLI. Blocked on BUG-57 (azure_compute frontend needs configurable
// JWKS-based bearer verification so sockerless's Entra stub tokens are
// accepted). Mirrors the DNS azure_cli_test.go pattern: az cloud register
// + az login --service-principal against sockerless, then az vm list/show.
// No sockerless-specific fallbacks; sockerless is treated as a real cloud
// service endpoint.
package conformance_test

import "testing"

func TestAzureCLI_Compute_VMList(t *testing.T) {
	t.Skip("blocked on BUG-57: azure_compute frontend needs configurable JWKS bearer verification (same prereq as BUG-44 for DNS)")
}

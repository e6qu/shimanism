// Phase 8 conformance: Azure APIM-shaped frontend driven by `az`.
// The `az` CLI doesn't expose a clean per-service endpoint
// override, so this lane is intentionally smoke-only —
// driver exists, skipped if `az` isn't on PATH.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzCLI_APIGateway_Smoke(t *testing.T) {
	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed (PATH lookup failed: %v)", err)
	}
	// The az CLI has no documented per-resource endpoint override.
	// Tracked in BUGS.md; this conformance test is the placeholder
	// that ensures the test surface exists for the matrix.
	t.Skip("az CLI per-resource endpoint override not yet wired (see BUGS.md)")
}

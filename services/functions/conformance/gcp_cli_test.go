// Phase 7 conformance: gcloud run CLI cell — ◇ documented skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestGCPCLI_FunctionsLifecycle(t *testing.T) {
	t.Skip("gcloud run deploy polls Operations.Get which the shim's frontend doesn't implement at this phase (same shape as BUG-5). SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("gcloud"); err != nil {
		t.Skipf("gcloud not installed: %v", err)
	}
}

// Phase 6 conformance: GCP Memorystore CLI cell — ◇ documented skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestGCPCLI_CacheLifecycle(t *testing.T) {
	t.Skip("gcloud redis instances create polls Operations.Get which the shim's frontend doesn't implement at this phase (same shape as BUG-5). SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("gcloud"); err != nil {
		t.Skipf("gcloud not installed: %v", err)
	}
}

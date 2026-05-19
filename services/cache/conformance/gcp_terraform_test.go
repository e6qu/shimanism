// Phase 6 conformance: hashicorp/google google_redis_instance — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_GCPCache_ResourceLifecycle(t *testing.T) {
	t.Skip("google_redis_instance polls Operations.Get; the shim's frontend doesn't implement the Operations endpoint (BUG-5 shape). SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

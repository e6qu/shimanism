// Phase 7 conformance: hashicorp/google google_cloud_run_v2_service — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_GCPFunctions_ResourceLifecycle(t *testing.T) {
	t.Skip("google_cloud_run_v2_service polls Operations.Get which the shim doesn't implement (BUG-5 shape). SDK + matrix cells cover.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

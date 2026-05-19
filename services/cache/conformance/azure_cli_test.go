// Phase 6 conformance: Azure Cache for Redis CLI cell — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestAzureCLI_CacheLifecycle(t *testing.T) {
	t.Skip("az redis create polls Azure-AsyncOperation URLs the shim doesn't emit at this phase. SDK + matrix cells cover this combination.")

	if _, err := exec.LookPath("az"); err != nil {
		t.Skipf("az CLI not installed: %v", err)
	}
}

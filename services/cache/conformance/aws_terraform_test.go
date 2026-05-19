// Phase 6 conformance: hashicorp/aws aws_elasticache_cluster — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSCache_ResourceLifecycle(t *testing.T) {
	t.Skip("aws_elasticache_cluster reconciles via ModifyCacheCluster + waits on parameter-group/subnet-group metadata the shim's intersection frontend doesn't expose (same shape as BUG-2). SDK + CLI cells cover this combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

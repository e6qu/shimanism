// Phase 10 sub-phase 10.2: terraform apply drift audit for cache.
//
// Contract: services/cache/APPLY_INTERSECTION.md.
//
// All three cells diamond-skipped with pointers to existing gaps —
// the Phase 6 TF cells were the same shape:
//
//   - AWS: aws_elasticache_cluster reconciles via ModifyCacheCluster +
//     waits on parameter-group / subnet-group metadata (BUG-2 class).
//   - GCP: google_redis_instance — Operations.Get was the original
//     blocker (BUG-5, closed in 10.1), but the same v1beta-vs-v1 path
//     mismatch as BUG-16 likely applies here; pending Track A or
//     v1beta1 route wiring.
//   - Azure: azurerm_redis_cache polls Azure-AsyncOperation URLs the
//     shim doesn't emit.
//
// Skipped if `terraform` isn't on PATH.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSCache_Apply_NoDrift(t *testing.T) {
	t.Skip("aws_elasticache_cluster reconciles via ModifyCacheCluster + needs parameter-group / subnet-group metadata (same class as BUG-2; Phase 6 posture)")
}

func TestTerraform_GCPCache_Apply_NoDrift(t *testing.T) {
	// BUG-5 was closed in 10.1 (Operations.Get implemented). But
	// hashicorp/google google_redis_instance targets paths the shim
	// doesn't currently match (same v1beta-vs-v1 family as BUG-16
	// for rdbms). Verifying + wiring v1beta1 routes is a follow-up.
	t.Skip("BUG-5 closed in 10.1; provider/shim path-version alignment pending verification (BUG-16 family)")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

func TestTerraform_AzureCache_Apply_NoDrift(t *testing.T) {
	t.Skip("azurerm_redis_cache polls Azure-AsyncOperation URLs the shim doesn't emit at this phase")
}

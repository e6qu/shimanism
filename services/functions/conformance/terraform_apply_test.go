// Phase 10 sub-phase 10.2: terraform apply drift audit for functions.
//
// Contract: services/functions/APPLY_INTERSECTION.md.
//
// All three cells diamond-skipped with pointers to existing Phase 7
// posture:
//
//   - AWS: aws_lambda_function requires IAM role + execution-environment
//     metadata the intersection frontend doesn't expose (BUG-13 partly
//     addresses this with role defaults; full close needs domain ext —
//     APPLY_INTERSECTION.md flags as 10.3 candidate).
//   - GCP: google_cloud_run_v2_service — Operations.Get was the blocker
//     (closed in 10.1), but the v1beta-vs-v1 path family applies here
//     too (BUG-16 family).
//   - Azure: azurerm_container_app polls Azure-AsyncOperation URLs
//     the shim doesn't emit at this phase.
//
// Skipped if `terraform` isn't on PATH.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSFunctions_Apply_NoDrift(t *testing.T) {
	t.Skip("aws_lambda_function requires IAM role + execution-environment metadata not in intersection (BUG-13 family; 10.3 candidate)")
}

func TestTerraform_GCPFunctions_Apply_NoDrift(t *testing.T) {
	t.Skip("BUG-5 closed in 10.1; provider/shim path-version alignment pending verification (BUG-16 family)")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

func TestTerraform_AzureFunctions_Apply_NoDrift(t *testing.T) {
	t.Skip("azurerm_container_app polls Azure-AsyncOperation URLs the shim doesn't emit at this phase")
}

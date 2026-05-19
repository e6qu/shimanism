// Phase 7 conformance: hashicorp/aws aws_lambda_function — ◇ skip.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSFunctions_ResourceLifecycle(t *testing.T) {
	t.Skip("aws_lambda_function requires IAM role + execution-environment metadata the shim's intersection frontend doesn't expose. SDK + CLI cells cover this combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

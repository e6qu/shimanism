// Phase 5 conformance: AWS RDS-shaped frontend — `hashicorp/aws`
// Terraform provider cell.
//
// **Documented skip.** `aws_db_instance` requires several
// stabilisation polls (DescribeDBInstances must include
// subnet-group, parameter-group, option-group, security-group
// metadata that the shim's intersection frontend doesn't surface).
// The provider also calls ModifyDBInstance immediately after
// CreateDBInstance to reconcile attributes — same SetAttribute-like
// pattern that ◇-skipped `aws_sqs_queue` in Phase 3 (BUG-2). SDK +
// CLI cells cover the AWS RDS driver-backend combination.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSRDBMS_ResourceLifecycle(t *testing.T) {
	t.Skip("aws_db_instance reconciles via ModifyDBInstance + waits on subnet-group/parameter-group/option-group/security-group metadata that the shim's intersection frontend doesn't expose. Same pattern as BUG-2. SDK + CLI cells cover this driver-backend combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

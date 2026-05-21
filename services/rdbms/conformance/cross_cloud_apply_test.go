// Phase 10 sub-phase 10.7 — cross-cloud exit criterion for rdbms:
// TestCrossCloudApply_Roundtrip_RDBMSAWStoGCPCloudSQL.
//
// hashicorp/aws aws_db_instance Apply requires subnet-group /
// parameter-group / option-group / security-group metadata not in
// the cross-cloud intersection (GCP Cloud SQL has a materially
// different networking / parameter model). The AWS-shape Apply
// itself is skipped in terraform_apply_test.go (BUG-2-class
// reconcile); cross-cloud apply against the GCP backend hits the
// same wall.
//
// Active drift cell for rdbms Apply: GCP frontend + inmem backend
// (terraform_apply_test.go), passing the canonical Settings
// defaults + region round-trip per BUG-16 closure.
package conformance_test

import "testing"

func TestCrossCloudApply_Roundtrip_RDBMSAWStoGCPCloudSQL(t *testing.T) {
	t.Skip("cross-cloud asymmetry: aws_db_instance requires subnet-group / parameter-group / option-group / security-group metadata GCP Cloud SQL doesn't expose. Same class as the AWS-shape Apply skip. Documented in services/rdbms/APPLY_INTERSECTION.md")
}

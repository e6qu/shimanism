// Phase 10 sub-phase 10.7 — cross-cloud exit criterion for functions:
// TestCrossCloudApply_Roundtrip_FunctionsAWStoGCPRun.
//
// hashicorp/aws aws_lambda_function post-create polls the function's
// State + LastUpdateStatus + LayerVersions + VpcConfig + 10+ other
// fields not in the cross-cloud intersection (GCP Cloud Run service
// model is materially different). The AWS-shape WaitFor doesn't
// match the GCP-shape function configuration. Cross-cloud apply for
// functions is gated on a deeper translation effort (a follow-on
// phase).
//
// Active drift cell for functions Apply: AWS frontend + inmem backend
// (terraform_apply_test.go), passing the role/publish round-trip
// per BUG-13 closure.
package conformance_test

import "testing"

func TestCrossCloudApply_Roundtrip_FunctionsAWStoGCPRun(t *testing.T) {
	t.Skip("cross-cloud asymmetry: AWS Lambda's post-create reconcile polls VpcConfig / LayerVersions / dead-letter / tracing config not in the cross-cloud intersection. Documented in services/functions/APPLY_INTERSECTION.md")
}

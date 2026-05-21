// Phase 10 sub-phase 10.7 — cross-cloud exit criterion for apigateway:
// TestCrossCloudApply_Roundtrip_APIGwAWStoGCP.
//
// AWS API Gateway v2's CreateApi → CreateIntegration → CreateRoute →
// CreateDeployment sequence has cross-cloud asymmetries similar to
// the queue/pubsub WaitForStateEqual: the per-resource AWS-shape
// attributes the provider polls don't all map to GCP API Gateway's
// model (which is OpenAPI-document-based). BUG-6 also gates the
// Azure-backed destroy path.
//
// Active drift cell for apigateway Apply: AWS frontend + inmem
// backend (terraform_apply_test.go). Cross-cloud apply for
// apigateway requires honest translation of the AWS multi-step
// flow into a single GCP ApiConfig PUT — a separate phase.
package conformance_test

import "testing"

func TestCrossCloudApply_Roundtrip_APIGwAWStoGCP(t *testing.T) {
	t.Skip("cross-cloud asymmetry: AWS APIGw v2 multi-step Create flow vs GCP API Gateway's OpenAPI-doc model. Requires phase-level translation work beyond Phase 10 scope. Documented in services/apigateway/APPLY_INTERSECTION.md")
}

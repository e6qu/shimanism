// Phase 4 conformance: AWS SNS-shaped pubsub frontend — `hashicorp/aws`
// Terraform provider cell.
//
// **Documented skip.** `aws_sns_topic_subscription` requires
// `endpoint_auto_confirms = true` for the sqs protocol and then
// silently reconciles the subscription state with the same
// `SetQueueAttributes`-shaped flow that makes `aws_sqs_queue`
// skip in Phase 3 (BUG-2). The provider also asks for the
// `aws_sqs_queue` resource (for the backing queue), which the
// shim's pubsub frontend doesn't expose — the data plane is
// SNS publish + the slim SQS-receive surface. Adding the full SQS
// admin surface to the pubsub frontend just to satisfy Terraform
// is out of scope for Phase 4 (it would create two paths to manage
// the backing queue and re-introduce state divergence).
//
// SDK + CLI cells cover the AWS frontend driver-backend combination.
package conformance_test

import (
	"os/exec"
	"testing"
)

func TestTerraform_AWSPubsub_ResourceLifecycle(t *testing.T) {
	t.Skip("aws_sns_topic_subscription needs aws_sqs_queue (backing queue) + SetQueueAttributes reconciliation (BUG-2). The pubsub frontend deliberately omits the full SQS admin surface; SDK + CLI cells cover this combination.")

	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
}

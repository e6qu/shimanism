// Phase 10 sub-phase 10.7 — cross-cloud exit criterion for cache:
// TestCrossCloudApply_Roundtrip_CacheAWStoGCPMemorystore.
//
// hashicorp/aws aws_elasticache_cluster's post-create reconcile
// (ModifyCacheCluster + WaitForState on the cluster status) doesn't
// translate cleanly onto GCP Memorystore's google_redis_instance
// shape. The AWS frontend would need to coalesce the multi-step
// reconcile against the GCP backend's single-resource model. Both
// the AWS frontend's Apply skip (BUG-2-class) and the cross-cloud
// shape are gated on a Phase-after-10 effort.
//
// Active drift cell for cache Apply: GCP frontend + inmem backend
// (terraform_apply_test.go).
package conformance_test

import "testing"

func TestCrossCloudApply_Roundtrip_CacheAWStoGCPMemorystore(t *testing.T) {
	t.Skip("cross-cloud asymmetry: aws_elasticache_cluster reconciles via ModifyCacheCluster + parameter-group / subnet-group metadata GCP Memorystore doesn't have. Same class as queue/pubsub cross-cloud apply skips. Documented in services/cache/APPLY_INTERSECTION.md")
}

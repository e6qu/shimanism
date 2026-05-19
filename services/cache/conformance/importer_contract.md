# Cache (managed Redis) importer-read contract

> Phase 9 sub-phase 9.2 — captured from `terraform import aws_elasticache_cluster` against the shim's AWS ElastiCache frontend.

## aws_elasticache_cluster import — observed wire ops

awsQuery / XML.

| Action | Category | Status |
|---|---|---|
| `DescribeCacheClusters` | 1 | ✅ |
| `ListTagsForResource` (afterwards) | 2 | ✅ (existing) |

Import succeeds without further frontend fixes — the response shape happens to include all attributes the provider's importer needs. Plan reports a soft diff for two attributes:

- `num_cache_nodes = 0 -> 1` (similar to Lambda's `memory_size = 0 -> 128`: domain state doesn't track this, so the shim returns 0).
- `auto_minor_version_upgrade = false -> true` (real ElastiCache defaults to true; shim defaults to false).

Both filed alongside BUG-13 family — soft plan diffs not blocking import.

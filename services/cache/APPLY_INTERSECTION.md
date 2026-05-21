# Cache (Redis) — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md).

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_elasticache_cluster` (single-node Redis) | AWS ElastiCache `CreateCacheCluster` / `DescribeCacheClusters` / `ModifyCacheCluster` / `DeleteCacheCluster` | `CreateInstance` / `DescribeInstance` / `ModifyInstance` / `DeleteInstance` |
| `google_redis_instance` | GCP Memorystore `instances.create/get/patch/delete` | same |
| `azurerm_redis_cache` | Azure `Redis.CreateOrUpdate` / `Redis.Get` / `Redis.Update` / `Redis.Delete` | same |

`aws_elasticache_replication_group` (multi-node, cluster mode) is **out of intersection** — cross-cloud multi-shard topology has no honest mapping.

## Apply contract — Redis instance resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `cluster_id` / `name` | ✅ | All backends. |
| `engine` | ⚠ | Domain implicitly Redis (no Engine enum like RDBMS). `engine = "redis"` is the only honored value; anything else returns `InvalidParameterValue`. ElastiCache also supports Memcached — out of intersection; non-redis returns the source cloud's `InvalidParameterValue`. |
| `engine_version` / `redis_version` | ✅ | `domain.Instance.EngineVersion`. Per-cloud version-string fidelity: AWS `7.0` / GCP `REDIS_7_0` / Azure `6` — translation per backend; HCL matches source-cloud format. |
| `node_type` / `tier` + `memory_size_gb` (GCP) / `sku_name` (Azure) | ✅ | `domain.CreateInstanceOptions.NodeType`. Per-cloud sizing-tier name. Phase 9.5 surfaced `num_cache_nodes` drift — addressed; single-node Redis is the intersection. |
| `num_cache_nodes` (AWS) | ⚠ | AWS-specific. **In-contract for AWS-to-AWS only with value=1** (intersection is single-node). Other values or non-AWS backends return `InvalidParameterValue` / `OperationNotSupported`. |
| `port` | ⚠ | All backends use 6379 by default. AWS allows configuring; GCP / Azure fixed. **In-contract for AWS-to-AWS only when non-default**; GCP / Azure / Redis Operator backends return `InvalidParameterValue` if `port` is set non-default. |
| `auth_token` (AWS) / `auth_enabled` (GCP) / `redis_configuration` Auth (Azure) | ⚠ | Cross-cloud auth-token semantics differ. **Domain-level token is in-contract: token round-trips through `domain.Connection.AuthToken`.** Caller-supplied or generated. AWS / GCP / Azure surface `<redacted>` on Read (per source-cloud convention); Redis Operator surfaces from the Kubernetes Secret. Provider plan-diff on `auth_token` is suppressed via `ignore_changes` or sensitive-by-default. |
| `transit_encryption_enabled` / `at_rest_encryption_enabled` | ◇ | Per-cloud encryption config. Out of contract. |
| `subnet_group_name`, `security_group_ids`, `vpc_security_group_ids`, `authorized_network` (GCP), `subnet_id` (Azure) | ◇ | Networking. Out of contract. |
| `parameter_group_name` (AWS), `redis_configs` (GCP), `redis_configuration` (Azure) | ◇ | Engine tuning per backend. Out of contract. |
| `snapshot_retention_limit`, `snapshot_window` (AWS) / `persistence_iam_identity` (GCP) / `rdb_backup_enabled` (Azure) | ◇ | Backup config differs per cloud. Out of contract. |
| `availability_zone`, `multi_az_enabled`, `replicas_per_node_group` | ◇ | HA semantics differ materially. Out of contract. |
| `tags` / `labels` | ◇ | Same domain gap as rdbms / queue / pubsub. Out of contract. |
| `apply_immediately` (AWS) | ⚠ | Provider-side; the shim sees the resulting modify call. `false` puts changes in a pending-modify state on AWS; cross-cloud, only AWS honors. Against GCP / Azure / Redis Operator the shim applies immediately (their native semantics); shim returns the source-cloud's `apply_immediately = false` in the Read response to keep no-drift, but documents the timing-fidelity edge. |

### Async semantics

Per `domain.go`: every backend provisions asynchronously. **Operations.Get polling closed BUG-5 in Phase 10.1** (`/v1/projects/{p}/locations/{l}/operations/{op}` for Memorystore). Apply against GCP frontends no longer hangs.

### Update (`ModifyInstance`)

`domain.ModifyInstanceOptions` supports:

- `NodeType` — in-place on AWS / GCP / Azure (results in restart). Redis Operator: in-place by patching the StatefulSet template (results in pod recreation).
- `AuthToken` — in-place on AWS / GCP / Azure (token rotation). Redis Operator: in-place by updating the K8s Secret.

ForceNew across all backends:
- `cluster_id` / `name`
- `engine` (cannot change engine in place — but Redis is the only intersection)
- `engine_version` (Redis major-version upgrade is destructive cross-cloud; minor in-place varies — **conservative ForceNew across all cells** for Phase 10)

### Delete (`DeleteInstance`)

Async on every backend. Enters `Status=Deleting`; Terraform polls until `NoSuchInstance`.

`final_snapshot_identifier` (AWS) — same posture as RDBMS: AWS-to-AWS only; cross-cloud, shim returns `OperationNotSupportedException` if HCL declares a final snapshot against non-AWS.

## Out of contract

Per-cloud encryption + networking + parameter tuning + multi-AZ + cluster-mode replication + backup config + tags. Each returns source-cloud's `OperationNotSupportedException` envelope.

## What this contract commits the shim to

1. Accept the in-contract Create attributes; round-trip through Read with no drift on all four backend cells.
2. Honor `num_cache_nodes = 1` only; reject other values across the intersection.
3. Reject non-Redis engine with `InvalidParameterValue`.
4. Honor `ModifyInstance` for node-type change + auth-token rotation in-place; `ForceNew` for cluster_id / engine_version changes.
5. Honor async semantics via `Operations.Get` polling.
6. Document the `apply_immediately = false` timing-fidelity edge against non-AWS backends (no fake; just a known asymmetry).

## Known open BUGs gating this contract

None. BUG-5 was the canonical gate — closed in Phase 10.1.

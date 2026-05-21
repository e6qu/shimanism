# Managed Redis (control plane)

Provision and manage Redis instances across clouds. **Control-plane only** — the data plane is RESP; connect directly to the returned host.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS ElastiCache | awsQuery XML | Same wire family as RDS. |
| GCP Memorystore | REST + JSON | Routes accept both `/v1/...` (Go SDK) and `/v1beta1/...` (hashicorp/google) — Phase 10.3. |
| Azure Cache for Redis | REST + ARM | Long-polling async-operation URLs. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS ElastiCache | Passthrough. |
| `gcp` | Real GCP Memorystore | Passthrough. |
| `azure` | Real Azure Cache for Redis | Passthrough. |
| `redisop` | Redis Operator for Kubernetes | K8s peer. Dynamic client + StatefulSet template. |
| `inmem` | Process-local | Tests + local dev. |

## Intersection: single-node Redis only

The cross-cloud intersection is single-node Redis. ElastiCache replication groups (multi-node, cluster mode) are out of intersection. ElastiCache Memcached is out of intersection.

## Async semantics

Same shape as [rdbms](rdbms.md): all backends provision asynchronously; `Operations.Get` polling for GCP (Phase 10.1 BUG-5).

## Memorystore Operation name canonicalization

GCP Memorystore returns Operation names in the full canonical form `projects/{p}/locations/{l}/operations/{op-id}`. The shim emits this on both Create and Get so hashicorp/google's poll-by-Name pattern works against the v1beta1 path family.

## Read-side Instance round-trip

`instanceToGCPWithPath` surfaces `Name` (full resource path), `LocationId`, `MemorySizeGb`, plus the canonical Memorystore Instance defaults hashicorp/google's schema expects (`ConnectMode=DIRECT_PEERING`, `TransitEncryptionMode=DISABLED`, `AuthEnabled=false`, `ReadReplicasMode=READ_REPLICAS_DISABLED`, `ReplicaCount=0`). Honest defaults — they describe "an instance with the default network / encryption posture."

## Intersection contracts

- **[`services/cache/OPERATIONS.md`](../../services/cache/OPERATIONS.md)** — operation list.
- **[`services/cache/INTERSECTION.md`](../../services/cache/INTERSECTION.md)** — per-frontend classification.
- **[`services/cache/APPLY_INTERSECTION.md`](../../services/cache/APPLY_INTERSECTION.md)** — Apply contract:
  - In-contract Create: `cluster_id`/`name`, `engine_version`, `node_type` (or `memory_size_gb`).
  - `num_cache_nodes=1` only; non-1 values return `InvalidParameterValue`.
  - Out-of-contract: encryption, networking, parameter tuning, multi-AZ, cluster-mode replication, backups, tags.

## Update intersection

In-place: `NodeType`, `AuthToken`. `ForceNew`: `cluster_id`, `engine` (Redis-only), `engine_version`.

## Conformance

- `TestCacheMatrix_*` — (frontend × backend × driver) cells.
- `TestTerraform_GCPCache_Apply_NoDrift` — GCP frontend Apply through inmem.
- `TestCrossCloudApply_Roundtrip_CacheAWStoGCPMemorystore` (Phase 10.7) — documented-skip (AWS ElastiCache parameter-group / subnet-group reconcile not in intersection).

## Known gaps

- AWS-shape Apply requires post-create reconcile metadata not in intersection (BUG-2-class) — skipped with pointer.
- Azure ARM ProvisioningState long-polling deferred to Track A.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/cache/MIGRATION.md](../../services/cache/MIGRATION.md)
- Related: [docs/services/rdbms.md](rdbms.md) (same control-plane shape).

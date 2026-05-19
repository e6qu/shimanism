# Cache — operation and feature mapping

> The intersection footprint shimanism's `cache` service can cover, across the four backends in scope:
> **AWS ElastiCache (Redis OSS)**, **GCP Memorystore for Redis**, **Azure Cache for Redis**, **Redis Operator** (K8s) as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle).
>
> The shim itself is stateless — instance metadata and auth tokens live in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## Same shape as Phase 5

Phase 6 carries forward the Phase 5 architecture verbatim:

- **Control plane only.** The shim provisions Redis instances and returns connection metadata (host, port, auth token). Clients connect *directly* via RESP — the shim plays no role on the data path.
- **Async semantics surfaced explicitly.** All four backends provision asynchronously. The domain models this with the same `Status` enum (Creating, Available, Modifying, Rebooting, Deleting).
- **Stateless credentials.** Auth token returned exactly once at `CreateInstance`. The Redis Operator backend stores it in a Kubernetes `Secret`; the shim re-reads on each `DescribeInstance`. No shim-side credential cache.

The Phase-6 exit criterion mirrors Phase-5's: **`redis-cli PING` returns `PONG` through the shim-returned Connection block, against a Redis-Operator-provisioned instance.**

## The intersection — 6 operations

Phase 6's intersection is smaller than Phase 5's because cross-cloud snapshot semantics for Redis are notoriously inconsistent (AWS ElastiCache snapshots go to S3, GCP exports to GCS, Azure uses backup containers, Redis Operator uses BackupRestore CRs). Snapshot/restore is deferred to a follow-on.

| Domain op | AWS ElastiCache | GCP Memorystore | Azure Cache for Redis | Redis Operator |
|---|---|---|---|---|
| **CreateInstance**(name, version, node_type, auth?) | `CreateCacheCluster` | `instances.create` | `Servers_Create` | `Redis` CR + auth `Secret` |
| **DeleteInstance**(name) | `DeleteCacheCluster` | `instances.delete` | `Servers_Delete` | `Redis` CR delete |
| **DescribeInstance**(name) | `DescribeCacheClusters` (filter) | `instances.get` | `Servers_Get` | `Redis` CR get + read auth `Secret` |
| **ListInstances**(prefix?) | `DescribeCacheClusters` (no filter) | `instances.list` | `Servers_List` | `Redis` CR list |
| **ModifyInstance**(name, node_type?) | `ModifyCacheCluster` | `instances.patch` | `Servers_Update` | `Redis` CR patch |
| **RebootInstance**(name) | `RebootCacheCluster` | `instances.failover` | `Servers_ForceReboot` | rollout-restart of the Redis StatefulSet |

## Instance status mapping

| Domain status | AWS ElastiCache | GCP Memorystore | Azure Cache for Redis | Redis Operator |
|---|---|---|---|---|
| `Creating`  | `creating`           | `CREATING`       | `Creating` | Redis condition `Ready=False, Reason=Bootstrap` |
| `Available` | `available`          | `READY`          | `Succeeded` | Redis condition `Ready=True` |
| `Modifying` | `modifying`          | `UPDATING`       | `Updating` | Redis condition `Ready=False, Reason=Rolling` |
| `Rebooting` | `rebooting cluster nodes`/`snapshotting` | `MAINTENANCE` | `Restarting` | rollout-restart in progress |
| `Deleting`  | `deleting`           | `DELETING`       | `Disabled` | Redis CR has `deletionTimestamp` |

## Connection metadata

`DescribeInstance` returns the same `Connection` block shape as Phase 5, adapted to RESP:

```go
type Connection struct {
    Host           string   // Redis primary endpoint
    Port           int      // 6379 default
    AuthToken      string   // empty when no auth configured
    EngineVersion  string
}
```

Auth token is **returned only at `CreateInstance` time** (or set by caller). Subsequent `DescribeInstance` calls re-read it from the backend's Secret (Redis Operator) or surface it as `<redacted>` (AWS / GCP / Azure don't re-emit the auth token after create, matching their published API).

## What's emphatically out of intersection

- **AWS ElastiCache:** cluster mode, replication groups, parameter groups, security groups, snapshot windows, log delivery configurations, ElastiCache Serverless.
- **GCP Memorystore:** transit encryption modes beyond a single bool, persistence configurations (RDB, AOF), maintenance policies, read replicas.
- **Azure Cache for Redis:** premium-tier features (clustering, persistence, geo-replication, virtual networks), Enterprise tier.
- **Redis Operator-specific:** Sentinel deployments, custom Redis configurations, exporter sidecars, scheduled backup CRs.
- **All:** Snapshot/Restore. Cross-cloud semantics differ too much; deferred to a follow-on phase.

## Sub-phase plan (Phase 6)

| Sub | Headline |
|---|---|
| 6.0 | Scope + intersection mapping (this doc) + sub-phase plan. |
| 6.1 | Vendor AWS ElastiCache Smithy spec. GCP + Azure specs reused via official SDKs' wire-type packages. |
| 6.2 | Domain interface (`internal/cache/domain/`): `Cache` interface, `Instance`, `Connection`, `Status` reused from Phase 5 patterns. |
| 6.3 | inmem backend + AWS ElastiCache frontend (awsQuery wire protocol) + SDK conformance. |
| 6.4 | Redis Operator backend (K8s peer) via the Kubernetes Go client + dynamic CRs. |
| 6.5 | AWS ElastiCache passthrough backend via `aws-sdk-go-v2/service/elasticache`. |
| 6.6 | GCP Memorystore Admin backend via `cloud.google.com/go/redis/apiv1` or `google.golang.org/api/redis/v1`. |
| 6.7 | Azure Cache for Redis backend via `armredis`. |
| 6.8 | GCP Memorystore Admin frontend (REST/JSON). |
| 6.9 | Azure Cache for Redis REST frontend (ARM URL shape). |
| 6.10 | Matrix conformance (`TestCacheMatrix_*`). |
| 6.11 | CLI conformance — `aws elasticache`, `gcloud redis instances`, `az redis`. |
| 6.12 | Terraform conformance — `hashicorp/aws aws_elasticache_cluster`, `hashicorp/google google_redis_instance`. |
| 6.13 | `cmd/shim cache` subcommand (default `:9500`). |
| 6.14 | CI lane `conformance-redisop`: kind + Redis Operator + `TestCacheMatrix_*` against the Redis-Operator backend. |
| 6.15 | **redis-cli PING connectivity test** against the Redis-Operator-provisioned instance. Phase-6 exit criterion. |
| 6.16 | Phase 6 closer. |

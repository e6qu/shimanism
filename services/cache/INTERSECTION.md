# Cache (Managed Redis) — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).
>
> Control-plane only. Data plane is RESP; clients connect direct to the returned endpoint.

## AWS ElastiCache frontend (awsQuery / XML)

| Op | Category | Status |
|---|---|---|
| CreateCacheCluster, DescribeCacheClusters, ModifyCacheCluster, DeleteCacheCluster | 1 | ✅ |
| AddTagsToResource, RemoveTagsFromResource, ListTagsForResource | 1 | ✅ |
| Snapshot ops, replication-group ops | 3 — out (cluster-mode + cross-region replication are advanced) | ◇ |
| CreateCacheSubnetGroup, parameter groups | 3 — out | ◇ |

## GCP Memorystore frontend (REST JSON)

| Op | Category | Status |
|---|---|---|
| Instances.{create,get,delete,list,patch} | 1 | ✅ |
| Operations.{get,list} | 1 — same async-poll requirement as rdbms | ⚠ same BUG-5 family |
| ExportInstance, ImportInstance | 1 — migration-critical (export to GCS) | ⚠ partial |

## Azure Cache for Redis frontend (armredis)

| Op | Category | Status |
|---|---|---|
| Redis.{CreateOrUpdate,Get,Delete,ListByResourceGroup} | 1 | ✅ |
| Redis.ExportData, Redis.ImportData | 1 — migration-critical | ⚠ partial |
| Patch schedules, firewall rules, linked servers | 3 — out | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS | GCP | Azure | Redis Operator | Status |
|---|---|---|---|---|---|
| Provision a Redis cluster | CreateCacheCluster | Instances.create | Redis.CreateOrUpdate | RedisFailover CR | ✅ |
| Get the connection endpoint | DescribeCacheClusters | Instances.get | Redis.Get | (status) | ✅ |
| Scale up/out | ModifyCacheCluster | Instances.patch | Redis.Update | spec edit | ✅ |
| Drop | DeleteCacheCluster | Instances.delete | Redis.Delete | CR delete | ✅ |
| Export RDB for migration | (snapshot to S3) | ExportInstance | ExportData | (volume snapshot) | ⚠ Phase 9 fold-in |
| Verify reachability (RESP PING) | (direct) | (direct) | (direct) | (direct) | ✅ Phase 6 exit criterion |

## Known gaps

- Async-op polling for GCP same BUG-5 family.
- ExportInstance / ExportData / SnapshotToS3 are the cross-cloud data-migration paths — Phase 9 (or a Phase 6.x follow-on) closes these.

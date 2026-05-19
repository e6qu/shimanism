# Cache (Managed Redis) — migration walkthroughs

> Phase 9 sub-phase 9.2-B. Control plane only. See [INTERSECTION.md](INTERSECTION.md).

## AWS ElastiCache → Azure Cache for Redis

```bash
shim cache --addr=:9600 \
  --frontend=aws_elasticache \
  --backend=azure --azure-subscription=$AZURE_SUB --azure-resource-group=$RG &
eval "$(shimctl env --frontend=aws --service=cache --endpoint=http://localhost:9600)"

aws elasticache create-cache-cluster --cache-cluster-id cache \
  --engine redis --cache-node-type cache.t3.small --num-cache-nodes 1
aws elasticache describe-cache-clusters --cache-cluster-id cache
# Connection string -> redis-cli ping goes direct.
aws elasticache modify-cache-cluster --cache-cluster-id cache --cache-node-type cache.t3.medium
aws elasticache delete-cache-cluster --cache-cluster-id cache
```

**Walkthrough holds for provisioning.** Data-plane PING verified by Phase 6's exit criterion.

## Cloud → Redis Operator (K8s peer)

```bash
shim cache --addr=:9600 \
  --frontend=aws_elasticache \
  --backend=redisop --kubeconfig=$HOME/.kube/config &
```

`AWS CacheCluster` ↔ `RedisFailover CR`. The shim creates the CR; the operator provisions sentinel + master + replicas.

## Data migration (incomplete)

`ExportInstance` (GCP) / `ExportData` (Azure) / `snapshot-to-S3` (AWS) are the cross-cloud data-migration paths. INTERSECTION.md flags these as partial; Phase 9 (or a 6.x follow-on) is the closer.

## Coverage

Control-plane intersection green. Data-migration ops are the gap.

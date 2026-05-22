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

## Terraform walkthrough (AWS-shaped provider against a non-AWS backend)

The `hashicorp/aws` provider exposes a per-service `endpoints` block that points at any HTTP endpoint. Pointing it at the shim lets existing AWS-shaped `aws_elasticache_cluster` resources provision a real Redis on whichever backend the shim is fronting (Azure / GCP / Redis-operator).

```hcl
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    elasticache = "http://localhost:9600"
  }
}

resource "aws_elasticache_cluster" "shim_cache" {
  cluster_id      = "shim-cache"
  engine          = "redis"
  engine_version  = "7.1"
  node_type       = "cache.t3.micro"
  num_cache_nodes = 1
}
```

```bash
# Start the shim against your chosen backend.
shim cache --addr=:9600 --frontend=aws_elasticache --backend=gcp --gcp-project=$GCP_PROJECT &

terraform init
terraform apply -auto-approve
# `aws_elasticache_cluster.shim_cache` is now a real Redis on GCP Memorystore.

terraform import aws_elasticache_cluster.existing shim-cache    # import a prior shim-provisioned cluster
terraform plan -refresh-only -detailed-exitcode                  # exit 0 means no drift
```

Conformance test reference: `services/cache/conformance/{terraform_import_test.go, cross_cloud_import_test.go}`.

## Data migration (incomplete)

`ExportInstance` (GCP) / `ExportData` (Azure) / `snapshot-to-S3` (AWS) are the cross-cloud data-migration paths. INTERSECTION.md flags these as partial; Phase 9 (or a 6.x follow-on) is the closer.

## Coverage

Control-plane intersection green. Data-migration ops are the gap.

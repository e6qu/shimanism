# Functions — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).
>
> Container-image deploys only — ZIP-package is AWS-specific and out of intersection. HTTP-trigger only — event sources are out of intersection too (cross-cloud event shapes differ).

## AWS Lambda frontend (restJson1)

| Op | Category | Status |
|---|---|---|
| CreateFunction (PackageType=Image), GetFunction, UpdateFunctionCode, UpdateFunctionConfiguration, DeleteFunction, ListFunctions | 1 | ✅ |
| Invoke (sync HTTP) | 1 | ✅ |
| TagResource, UntagResource, ListTags | 1 | ✅ |
| CreateFunctionUrlConfig, GetFunctionUrlConfig, DeleteFunctionUrlConfig | 1 — needed for the HTTP-trigger URL surface | ⚠ partial (returned via DescribeFunction.Endpoint at this phase; FunctionUrlConfig op not yet wired) |
| Aliases, versions, layers, provisioned concurrency | 3 — out | ◇ |
| Event source mappings | 3 — out (cross-cloud events are their own phase) | ◇ |
| ZIP package deploys | 3 — out (vendor-specific) | ◇ |

## GCP Cloud Run frontend (REST JSON v2)

| Op | Category | Status |
|---|---|---|
| Services.{create,get,delete,list,patch} | 1 | ✅ |
| Operations.{get,list} | 1 — async-poll | ⚠ BUG-5 family |
| Revisions, traffic-splits | 3 — out (vendor-specific concurrent-revision model) | ◇ |
| IAM (`services/{n}:setIamPolicy`) | 3 — out (cross-cloud IAM separate) | ◇ |

## Azure Container Apps frontend (armappcontainers)

| Op | Category | Status |
|---|---|---|
| ContainerApps.{BeginCreateOrUpdate,Get,BeginDelete,ListByResourceGroup,BeginUpdate} | 1 | ✅ |
| ContainerAppsRevisions.{Get,List} | 3 — out (vendor-specific) | ◇ |
| Dapr, Auth config, scaling rules | 3 — out | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS Lambda | GCP Cloud Run | Azure CA | Knative | Status |
|---|---|---|---|---|---|
| Deploy a container image | CreateFunction(Image) | Services.create | ContainerApps.CreateOrUpdate | Service CR | ✅ |
| Get the HTTP URL | GetFunction → Endpoint | Services.get → status.url | ContainerApps.Get → fqdn | Service status.url | ✅ |
| Update image | UpdateFunctionCode | Services.patch | ContainerApps.Update | spec edit | ✅ |
| Drop | DeleteFunction | Services.delete | ContainerApps.Delete | CR delete | ✅ |
| Set tags / labels | TagResource | metadata.labels | tags | (k8s labels) | ✅ |
| HTTP-invoke (exit criterion) | (direct GET on URL) | (direct) | (direct) | (direct) | ✅ Phase 7 |

## Known gaps

- FunctionUrlConfig CRUD not wired (only synthesized at DescribeFunction time today). Migration users who Terraform `aws_lambda_function_url` separately need this.
- Async-op polling for GCP same BUG-5 family.

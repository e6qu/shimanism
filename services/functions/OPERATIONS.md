# Functions — operation and feature mapping

> The intersection footprint shimanism's `functions` service can cover, across the four backends in scope:
> **AWS Lambda (container image path)**, **GCP Cloud Run**, **Azure Container Apps**, **Knative Serving** (K8s) as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle).
>
> The shim itself is stateless — function metadata and image references live in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## Same shape as Phases 5+6 — different data plane

Phase 7 is control-plane only (provision, return endpoint URL), like Phases 5+6. The data plane is **HTTP** — clients invoke the deployed function via the returned URL; the shim plays no role on the invocation path. Trigger / event-source normalization (the most complex part of cross-cloud Functions) is **out of scope at this phase** — the intersection covers the deployment + HTTP-trigger surface only.

The exit criterion: **`curl <returned-url>` returns the function's HTTP response, end-to-end, against the Knative backend.**

## Engine choice: container image only

AWS Lambda has two deployment paths (ZIP package + container image); only the **container image path** is in scope. GCP Cloud Run is container-only natively. Azure Container Apps is container-only natively. Knative `Service` is container-only natively. The shim's domain accepts a container image reference; ZIP-style source uploads are out of intersection.

This is a meaningful narrowing: cross-cloud function deployments via the shim ship a registry image, not a source bundle.

## The intersection — 5 operations

| Domain op | AWS Lambda | GCP Cloud Run | Azure Container Apps | Knative |
|---|---|---|---|---|
| **CreateFunction**(name, image, opts) | `CreateFunction` (PackageType=Image, Code.ImageUri=...) | `services.create` | `ContainerApps_CreateOrUpdate` | `Service` CR (kn-serving) |
| **DeleteFunction**(name) | `DeleteFunction` | `services.delete` | `ContainerApps_Delete` | `Service` CR delete |
| **DescribeFunction**(name) | `GetFunction` + `GetFunctionConfiguration` | `services.get` | `ContainerApps_Get` | `Service` CR get + status.url |
| **ListFunctions**(prefix?) | `ListFunctions` | `services.list` | `ContainerApps_ListByResourceGroup` | `Service` CR list |
| **UpdateFunction**(name, image?, env?) | `UpdateFunctionCode` + `UpdateFunctionConfiguration` | `services.replaceService` | `ContainerApps_Update` | `Service` CR patch |

## Function status mapping

| Domain status | AWS Lambda | GCP Cloud Run | Azure Container Apps | Knative |
|---|---|---|---|---|
| `Creating` | `Pending` | `CONTAINER_HEALTH_CHECK_FAILED`/initial revision condition | `InProgress` | `Service` condition `Ready=False, Reason=RevisionMissing` |
| `Available` | `Active` | `READY` (`status.conditions[Ready]=True`) | `Succeeded` (with `latestRevisionFqdn`) | `Service` condition `Ready=True` |
| `Updating` | `InProgress` (LastUpdateStatus) | revision in progress | `Updating` | new revision rolling out |
| `Deleting` | `Pending` with deletion marker | `DELETING` | `Disabled` | CR has `deletionTimestamp` |

## Connection metadata

`DescribeFunction` returns:

```go
type Endpoint struct {
    URL string // https://<function>.<cloud-suffix>/ — HTTP invocation entry point
}
```

Authentication is **out of intersection** at this phase — public-HTTP functions only. IAM-gated invocation (`aws lambda invoke`, GCP IAM, Azure auth) requires per-cloud credential flows that don't translate cleanly. Document as deferred.

## Environment variables + memory limits

Function configuration is the largest common denominator:

- **Environment variables**: `map[string]string`. Every backend supports.
- **Memory limit**: bytes. AWS uses MB (128-10240), GCP uses Mi notation, Azure uses GiB, Knative uses K8s `resources.limits.memory`. Domain uses bytes; backends translate.
- **CPU**: AWS scales CPU with memory (no separate knob); GCP / Azure / Knative each accept CPU in cores. Domain uses millicores; the AWS backend ignores the knob (CPU is derived from memory).
- **Timeout** (request): seconds. Floor 1, ceiling 900 (Lambda's max).

Everything else (cold-start tuning, concurrency limits, network attachment, secrets references, IAM bindings) is **out of intersection**.

## What's emphatically out of intersection

- **AWS Lambda:** ZIP package deployment, layers, provisioned concurrency, VPC config, EFS mounts, IAM execution role beyond defaults, event-source mappings (SQS/Kinesis/DynamoDB), function URLs IAM gating, X-Ray tracing config.
- **GCP Cloud Run:** Cloud SQL connections, secret manager mounts, VPC connector, custom IAM bindings, multi-region, BLD/CMEK encryption.
- **Azure Container Apps:** Dapr integration, scaling rules (KEDA), revision traffic split, custom domains, managed identity.
- **Knative:** Activator-specific tuning, autoscaling KPA/HPA knobs, traffic split between revisions, CRD-level concurrency targets.
- **Events / triggers (all backends):** Cross-cloud event payload normalization is the hard part PLAN.md flags as a translation challenge. Deferred to a follow-on phase. The shim ships HTTP-trigger functions only at this phase.

## Sub-phase plan (Phase 7)

| Sub | Headline |
|---|---|
| 7.0 | Scope + intersection mapping (this doc) + sub-phase plan. |
| 7.1 | Vendor AWS Lambda Smithy spec. GCP Cloud Run + Azure Container Apps via official SDKs. |
| 7.2 | Domain interface (`internal/functions/domain/`): `Functions`, `Function`, `Endpoint`, `Status` enum. |
| 7.3 | inmem backend + AWS Lambda frontend (awsJson1_0, modernized) + SDK conformance. |
| 7.4 | Knative Serving backend (K8s peer) via dynamic client + unstructured `Service` CRs. |
| 7.5 | AWS Lambda passthrough backend via `aws-sdk-go-v2/service/lambda`. |
| 7.6 | GCP Cloud Run Admin backend via `google.golang.org/api/run/v2`. |
| 7.7 | Azure Container Apps backend via `armappcontainers`. |
| 7.8 | GCP Cloud Run frontend (REST/JSON). |
| 7.9 | Azure Container Apps REST frontend (ARM URL shape). |
| 7.10 | Matrix conformance. |
| 7.11 | CLI conformance — `aws lambda`, `gcloud run`, `az containerapp`. |
| 7.12 | Terraform conformance. |
| 7.13 | `cmd/shim functions` subcommand. Default `:9600`. |
| 7.14 | CI lane `conformance-knative` — kind + Knative Serving operator. |
| 7.15 | **HTTP-invoke connectivity test** — `curl <endpoint>` returns the deployed function's response. Phase-7 exit criterion. |
| 7.16 | Phase 7 closer. |

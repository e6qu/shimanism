# Functions (control plane)

Deploy container-image functions across clouds. **Control plane only** — clients invoke the deployed function via the returned HTTP URL; the shim isn't on the invocation path.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS Lambda | restJson1 | Container-image Lambda only (ZIP packages out of intersection). |
| GCP Cloud Run v2 | REST + JSON | v2 service surface; auth-on-invoke deferred. |
| Azure Container Apps | REST + ARM | Container Apps environments + apps. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS Lambda | Passthrough. Caller-supplied Role honored; falls back to backend-configured default. |
| `gcp` | Real GCP Cloud Run | Passthrough. |
| `azure` | Real Azure Container Apps | Passthrough. |
| `knative` | Knative Serving | K8s peer. Dynamic client + unstructured `Service` CRs. kourier-internal port-forward for HTTP-invoke. |
| `inmem` | Process-local | Tests + local dev. |

## Container-image only

ZIP-package Lambda is out of intersection. AWS frontend rejects non-Image package types with `InvalidParameterValueException`. All four destinations natively support container images.

## Role + Publish (AWS-only)

`domain.Function.Role` (Lambda execution-role ARN) + `domain.Function.Publish` (publish-new-version flag) are in-contract for AWS-to-AWS passthrough. Cross-cloud, non-AWS backends accept-but-don't-apply: the destination's identity model (Cloud Run service-account, Container Apps managed identity, Knative pod identity) replaces the function-level execution role; real cross-cloud migration tools rebind identity on the destination (follow-on phase). Same posture for Publish — Cloud Run revisions are atomic, no separate "published" concept.

## Async semantics

All backends provision asynchronously. `Operations.Get` polling for GCP (Phase 10.1 BUG-5).

## Intersection contracts

- **[`services/functions/OPERATIONS.md`](../../services/functions/OPERATIONS.md)** — operation list.
- **[`services/functions/INTERSECTION.md`](../../services/functions/INTERSECTION.md)** — per-frontend classification.
- **[`services/functions/APPLY_INTERSECTION.md`](../../services/functions/APPLY_INTERSECTION.md)** — Apply contract:
  - In-contract Create: `function_name`, `image_uri`, `memory_size`, `timeout`, `environment`.
  - `cpu` in-contract on GCP / Azure / Knative; AWS Lambda derives CPU from memory.
  - `role` / `publish` / `architectures` AWS-to-AWS only; cross-cloud accept-but-don't-apply.
  - Out-of-contract: VPC, DLQ, tracing, EFS, KMS, layers, autoscale, ingress, managed identity, service account, custom domains, event-driven triggers.

## Update intersection

In-place across all backends: `Image`, `Environment`, `MemoryBytes`, `TimeoutSeconds`. `CPUMilliCores` in-place on GCP / Azure / Knative; against AWS returns `OperationNotSupportedException`. `ForceNew`: `function_name`.

## Conformance

- `TestFunctionsMatrix_*` — (frontend × backend × driver) cells.
- `TestInvokeConnectivity_Knative` — kind + Knative + kourier-internal port-forward HTTP invoke.
- `TestTerraform_AWSFunctions_Apply_NoDrift` — AWS frontend Apply through inmem (active drift cell after BUG-13 closure).
- `TestCrossCloudImport_Roundtrip_FunctionsAWStoGCPRun` (Phase 9.13).
- `TestCrossCloudApply_Roundtrip_FunctionsAWStoGCPRun` (Phase 10.7) — documented-skip.

## Known gaps

- BUG-13 closed in Phase 10.3 (Role + Publish round-trip through domain).
- Cross-cloud AWS Lambda → non-AWS apply needs WaitForState compatibility on Lambda-specific attributes (VpcConfig, LayerVersions) not in intersection — Track A follow-on.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/functions/MIGRATION.md](../../services/functions/MIGRATION.md)

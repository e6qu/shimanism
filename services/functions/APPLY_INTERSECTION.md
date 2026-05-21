# Functions — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md).

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_lambda_function` (container-image package) | AWS Lambda `CreateFunction` / `GetFunction` / `UpdateFunctionCode` + `UpdateFunctionConfiguration` / `DeleteFunction` | `CreateFunction` / `DescribeFunction` / `UpdateFunction` / `DeleteFunction` |
| `google_cloud_run_v2_service` (container) | GCP Cloud Run `Services.Create` / `Services.Get` / `Services.Patch` / `Services.Delete` | same |
| `azurerm_container_app` | Azure Container Apps `ContainerApps.CreateOrUpdate` / `ContainerApps.Get` / `ContainerApps.Update` / `ContainerApps.Delete` | same |

## Apply contract — function resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `function_name` / `name` | ✅ | All backends. |
| `image_uri` / `image` / container image | ✅ | `domain.Function.Image`. Container image is the only package type. ZIP / inline-source out of intersection — non-image package returns `InvalidParameterValue`. |
| `package_type` (AWS) | ⚠ | AWS-specific. `Image` is the only honored value; non-Image returns `InvalidParameterValue` across all backends (cross-cloud only image is supported). |
| `memory_size` (AWS, MB) / `memory_limit` (GCP, e.g. `512Mi`) / `memory` (Azure, e.g. `1.0Gi`) | ✅ | `domain.Function.MemoryBytes`. Per-cloud unit conversion in each adapter. **Phase 9.5 surfaced this as a drift (BUG-13 also lists it)** — Phase 10 must keep the Create-then-Read round-trip honest. |
| `timeout` / `timeout_seconds` | ✅ | `domain.Function.TimeoutSeconds`. 1-900s intersection (AWS Lambda hard cap). Backends supporting longer ignore the upper bound when the source-cloud HCL is within it. |
| `environment` / `environment_variables` | ✅ | `domain.Function.Environment` (`map[string]string`). Round-trips. |
| `cpu` / CPU sizing | ⚠ | `domain.Function.CPUMilliCores`. AWS Lambda derives CPU from memory (not a separately configurable attribute); HCL declaring `cpu` against AWS-shape frontend → AWS backend returns `OperationNotSupportedException`. GCP Cloud Run + Azure Container Apps + Knative honor. |
| `role` / IAM role (AWS) | ⚠ | **BUG-13 partly addressed in Phase 9.5 by emitting a default; Phase 10 must surface it through the domain.** AWS role is not currently in `domain.Function`. Pending decision (Phase 10.3): either extend the domain or surface as backend-config that's not round-tripped through Read. **For 10.0-A: `role` is in-contract for AWS-to-AWS passthrough only; cross-cloud, shim returns `OperationNotSupportedException`** since other backends don't have a role concept. |
| `publish` (AWS) | ⚠ | AWS-version-publishing. **BUG-13 surfaces as a drift on AWS-to-AWS** because shim doesn't surface published-version state. AWS-to-AWS only; cross-cloud returns `OperationNotSupported`. **In-contract for Phase 10 with default=false explicit in HCL** to avoid drift; non-default needs domain extension. |
| `architectures` (AWS, x86_64 / arm64) | ⚠ | AWS-specific. Image manifests carry architecture; honoring this cross-cloud requires the chosen image's manifest to match. **In-contract for AWS-to-AWS passthrough**; cross-cloud the shim ignores (image manifest determines) and returns the source-cloud value at Read to keep no-drift. |
| `vpc_config`, `dead_letter_config`, `tracing_config`, `file_system_config`, `kms_key_arn`, `code_signing_config_arn`, `image_config`, `ephemeral_storage` | ◇ | AWS-specific config. Out of contract. |
| `ingress` (GCP / Azure), `vpc_access` (GCP) | ◇ | Per-backend networking. Out of contract. |
| `service_account` (GCP), `identity` (Azure) | ◇ | Identity binding per backend. Cross-cloud different semantics. Out of contract. |
| `min_scale` / `max_scale` / `concurrency` / `min_instances` / `max_instances` | ◇ | Cross-cloud autoscale semantics differ. AWS Lambda has reserved/provisioned-concurrency; Cloud Run has min/max instances; Container Apps has replicas. Out of contract. |
| `tags` / `labels` | ◇ | Same domain gap. Out of contract. |

### Async semantics

Per `domain.go`: every backend provisions asynchronously. **Operations.Get polling closed BUG-5 in Phase 10.1** (`/v2/projects/{p}/locations/{l}/operations/{op}` for Cloud Run). Apply against GCP frontends no longer hangs.

### Update (`UpdateFunction`)

`domain.UpdateFunctionOptions` supports:

- `Image` — in-place on all backends. Image rolls out as a new revision (Cloud Run / Container Apps native; Lambda updates the function-version pointer).
- `Environment` — in-place on all backends.
- `MemoryBytes` — in-place on all backends.
- `CPUMilliCores` — in-place on GCP / Azure / Knative; **`OperationNotSupportedException` against AWS** (Lambda derives CPU from memory).
- `TimeoutSeconds` — in-place on all backends.

ForceNew across all backends:
- `function_name` / `name`

### Delete (`DeleteFunction`)

Async on every backend. Enters `Status=Deleting`; Terraform polls until `NoSuchFunction`.

## Out of contract

AWS-specific: VPC, DLQ, tracing, EFS, KMS, code-signing, image-config (entrypoint/cmd/workdir overrides), ephemeral-storage, layers, reserved/provisioned concurrency.

GCP-specific: VPC access, ingress, service account.

Azure-specific: managed identity, ingress, revision suffix, custom domains, registries (auth-pull is provider-side).

Cross-cloud: autoscale, tags, custom domains, event-driven triggers (the whole `lambda_event_source_mapping` / `cloud_run_trigger` family).

## What this contract commits the shim to

1. Accept the in-contract Create attributes; round-trip through Read with no drift on all four backend cells, *especially `memory_size`* (BUG-13).
2. Reject non-image package type with `InvalidParameterValue`.
3. Honor `UpdateFunction` for image / env / memory / timeout in-place across all backends; cpu in-place against GCP / Azure / Knative; cpu against AWS returns `OperationNotSupportedException`.
4. Honor async semantics via `Operations.Get` polling.
5. AWS-shape `role` + `publish` + `architectures` are in-contract for AWS-to-AWS passthrough only; cross-cloud returns `OperationNotSupportedException` (no fake; honest because other backends don't have these concepts).

## Known open BUGs gating this contract

- [BUG-13](../../BUGS.md): Lambda `memory_size` / `role` / `publish` soft plan diffs. Phase 10.2 drift audit will close most of this; the `role` part requires domain extension (deferred decision to Phase 10.3).

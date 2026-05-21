# API Gateway — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md). Operations classified as:
>
> 1. **CRUD-in (real work)** — must dispatch to a real `domain.APIGateway` call against a real backend.
> 2. **Soft-fail (cloud's "not supported")** — feature/attribute has no honest cross-cloud counterpart; shim returns the source cloud's real "not supported" envelope.
> 3. **Out-of-scope (provider attribute not in this contract)** — same envelope category as soft-fail, but flagged separately so the matrix tests don't drive these attributes in the in-contract path.

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_apigatewayv2_api` + `aws_apigatewayv2_integration` + `aws_apigatewayv2_route` + `aws_apigatewayv2_deployment` (stack) | AWS APIGW v2 `CreateApi` / `GetApi` / `DeleteApi` + per-route Create/Update + `CreateDeployment` | `CreateGateway` + per-request route accumulator → `DeployGateway` / `DescribeGateway` / `DeleteGateway` |
| `google_api_gateway_gateway` + `google_api_gateway_api_config` + `google_api_gateway_api` | GCP `Gateways.Create` / `ApiConfigs.Create` / `Apis.Create` (OpenAPI-driven) | `CreateGateway` / `DeployGateway` (OpenAPI parsed → `[]Route`) / `DescribeGateway` / `DeleteGateway` |
| `azurerm_api_management_api` + `azurerm_api_management_api_operation` | Azure APIM `Api.CreateOrUpdate` + per-operation CreateOrUpdate | `CreateGateway` / `DeployGateway` (Operations merged → `[]Route`) / `DescribeGateway` / `DeleteGateway` |

## Apply contract — gateway resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `name` | ✅ | All backends. |
| `protocol_type` (AWS, must be `HTTP`) | ✅ | AWS-only attribute. HTTP-only intersection — `WEBSOCKET` returns `OperationNotSupportedException`. |
| `api_id` (output) | ✅ | Backend assigns; shim returns. |
| `target` (AWS quick-create one-shot) | ◇ | AWS-shape convenience for "create API + single route". Out of contract (the multi-resource stack is the canonical shape). |
| `route_selection_expression` / `api_key_selection_expression` (AWS) | ⚠ | **Phase 9.5 surfaced this as a Create-then-Read drift bug fixed inline** — defaults `$request.method $request.path` / `$request.header.x-api-key` round-trip. In-contract for AWS-shape. |
| `disable_execute_api_endpoint`, `cors_configuration`, `body` (OpenAPI inline), `fail_on_warnings` | ◇ | AWS-specific. Out of contract. |
| GCP `display_name`, `labels` | ◇ | Out of contract (label gap, GCP-only display attribute). |
| Azure `display_name`, `description`, `path`, `protocols`, `service_url`, `subscription_required`, `version`, `revision` | ⚠ | `display_name` + `description` + `path` round-trip honest. `protocols` constrained to `["https"]` (HTTP-only intersection). The rest are out of contract. |

### Routes (per-route attributes within the `routes` block)

The per-route attributes the provider sends through the various `CreateIntegration` / `CreateRoute` / Operation / OpenAPI-`paths` calls map to `domain.Route`:

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `route_key` (AWS, e.g. `"GET /users/{id}"`) / OpenAPI path + method (GCP) / Operation `urlTemplate` + `method` (Azure) | ✅ | `domain.Route.Method` + `domain.Route.Path`. |
| `target` (AWS, `integrations/{integrationId}`) | ⚠ | AWS-shape indirection — per-request accumulator pairs the route with the integration's `connection_id` / `uri`. The shim resolves to `domain.Route.Backend` at `CreateDeployment` time. |
| Integration `connection_type=INTERNET` / `integration_type=HTTP_PROXY` / `integration_method=ANY` / `integration_uri` | ✅ | HTTP_PROXY → `domain.Route.Backend`. ANY → method preserved. INTERNET → no special handling (it's the default for the intersection). |
| Integration `aws_proxy` (Lambda integration) | ◇ | Out of contract (cross-cloud Lambda↔CloudRun↔ContainerApps integration is its own phase). |
| Integration `payload_format_version`, `template_selection_expression`, `passthrough_behavior`, `cache_*`, `content_handling_strategy`, `credentials_arn`, `request_*`, `response_*`, `tls_config`, `timeout_milliseconds` | ◇ | Per-cloud transform / cache / TLS / IAM config. Out of contract. |
| Per-route auth (`authorization_type`, `authorizer_id`, `api_key_required`) | ◇ | Per-route auth out of intersection (Phase 8 baseline). Out of contract. |
| Per-route throttling (`throttling_burst_limit`, `throttling_rate_limit`) | ◇ | Out of contract. |
| Per-route CORS | ◇ | Per-route CORS out of intersection. Gateway-level CORS is also out of contract. |

### Async semantics

Per `domain.go`: every backend provisions asynchronously. **Operations.Get polling closed BUG-5 in Phase 10.1** (`/v1/projects/{p}/locations/{l}/operations/{op}` for GCP API Gateway). Apply against GCP-shape frontends no longer hangs.

### Update — gateway-level

`name` is `ForceNew` everywhere. Most gateway-level attributes are immutable across the intersection. Provider HCL changes to:

- `routes` — `DeployGateway` atomic swap. Honored across all backends.
- AWS `route_selection_expression` / `api_key_selection_expression` — AWS-to-AWS only; cross-cloud `OperationNotSupportedException`.
- Anything out of contract — `OperationNotSupportedException`.

### Update — route-level

Per `domain.go`, `domain.Route.ID` lets a frontend correlate per-route mutations through stateless reads. The Azure frontend uses this to map per-operation updates back through `DeployGateway`. The AWS frontend uses the per-request route accumulator → `CreateDeployment` to publish the new full table.

Net effect: route Update is **delete-and-recreate at the gateway level** for AWS / GCP; **per-operation diff** for Azure. The provider's plan output may show different shapes per cell, but the final state matches.

### Delete

- AWS: `DeleteApi` (or `DeleteGateway`). Synchronous.
- GCP: `DeleteGateway` + `DeleteApiConfig` + `DeleteApi`. Async (via Operations); polled to DONE.
- Azure: `DeleteApi` via `armapimanagement/v3 APIClient.BeginDelete` with `ifMatch = "*"` (unconditional update — the canonical migration-tool choice) and `DeleteRevisions = nil` (preserve revisions). The poller is awaited until completion. BUG-6 closed.

## Out of contract

- Custom domains, base path mappings, DNS records.
- Per-route auth / authorizer resources.
- Per-route throttling, CORS, transforms.
- Per-stage variables, throttling, deployments-as-resources (the AWS `aws_apigatewayv2_stage` resource).
- Resource policies / IAM bindings.
- Lambda / Cloud Run / Container Apps integrations (cross-cloud invoke is a separate phase).
- WAF, observability, custom metrics.

## What this contract commits the shim to

1. Accept the in-contract Create attributes; round-trip through Read with no drift, *including the AWS `*_selection_expression` defaults* (Phase 9.5 fixed; keep green).
2. Reject out-of-contract attributes with the source cloud's real error envelope.
3. Honor `DeployGateway` atomic swap when `routes` changes; per-cloud Update shape can differ but final state matches.
4. Honor async semantics via `Operations.Get` polling.
5. Azure-backed destroy paths use v3 SDK's `BeginDelete` with `ifMatch = "*"`; the poller is awaited until completion. BUG-6 closed.

## Known open BUGs gating this contract

- [BUG-7](../../BUGS.md), [BUG-8](../../BUGS.md): Azure CLI + GCP TF frontend smoke-skips carried from Phase 8. Apply matrix carries the same skip-with-pointer posture (no new bugs filed; the existing ones cover the path).

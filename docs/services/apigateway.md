# API gateway

Deploy HTTP gateways with route → backend dispatch across clouds.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS API Gateway v2 | restJson1 (HTTP API) | `aws_apigatewayv2_api` + `_integration` + `_route` + `_deployment` multi-step apply. |
| GCP API Gateway | REST + JSON | OpenAPI-document driven (`ApiConfig` resource). |
| Azure API Management | ARM REST | Operation subresource per route. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS APIGW v2 | Passthrough. |
| `gcp` | Real GCP API Gateway | Passthrough. |
| `azure` | Real Azure APIM | Passthrough via `armapimanagement/v3`. Delete uses `BeginDelete(... ifMatch = "*", DeleteRevisions = nil)` + awaits the poller (BUG-6 closed). |
| `envoy` | Envoy Gateway | K8s peer. Dynamic client + unstructured `Gateway` + `HTTPRoute` CRs. |
| `inmem` | Process-local | Tests + local dev. |

## Declarative-replace

`DeployGateway(spec)` atomically swaps the entire routing table. Cross-cloud "patch one route" semantics are impossible (Azure's APIM has versioning baked in; GCP requires a full ApiConfig). The intersection is **publish a full routing table atomically**.

The AWS frontend's per-request route accumulator bridges AWS's multi-step `CreateRoute` → `CreateDeployment` to the domain's atomic shape (the `Routes` map is keyed off the request flow and flushed by `CreateDeployment`).

## Route shape (minimal)

Method + path + backend URL only. Per-route auth, throttling, transforms, CORS, custom domain mapping all deferred — the exit criterion is "routes dispatch HTTP to backends correctly."

## HTTP data plane

Same posture as functions: the shim provisions and returns the gateway URL; clients HTTP-request the URL; the shim plays no role on the request path.

## Intersection contracts

- **[`services/apigateway/OPERATIONS.md`](../../services/apigateway/OPERATIONS.md)** — 5 operations covering Create/Delete/Describe/List/Deploy Gateway.
- **[`services/apigateway/INTERSECTION.md`](../../services/apigateway/INTERSECTION.md)** — per-frontend classification.
- **[`services/apigateway/APPLY_INTERSECTION.md`](../../services/apigateway/APPLY_INTERSECTION.md)** — Apply contract:
  - In-contract Create: `name`, `protocol_type` (HTTP-only).
  - AWS-shape `*_selection_expression` defaults round-trip (Phase 9.5 inline fix kept green).
  - Per-route auth / throttling / CORS / transforms all out-of-contract.

## Conformance

- `TestRouteServes_Envoy` — exit criterion for Phase 8. Deploys a gateway with one route → echo backend; HTTP-GETs through Envoy via port-forward; asserts response.
- `TestTerraform_AWSAPIGateway_Apply_NoDrift` — AWS frontend Apply through inmem.
- `TestCrossCloudImport_Roundtrip_APIGwAWStoGCP` (Phase 9.13).
- `TestCrossCloudApply_Roundtrip_APIGwAWStoGCP` (Phase 10.7) — documented-skip (multi-step Create vs single-doc OpenAPI mismatch).

## Known gaps

- BUG-7 — `az` CLI per-resource endpoint override gap (no documented way to target only APIM through the shim). Not a code bug; `az` limitation.
- BUG-8 — `hashicorp/google` API Gateway resource lifecycle requires real OAuth-signed requests the mock httptest server can't sign. Gated on Track A real-cloud accounts.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/apigateway/MIGRATION.md](../../services/apigateway/MIGRATION.md)

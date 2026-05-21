# API Gateway — intersection inventory

> Per Phase 9 sub-phase 9.2-A. Each wire-level operation the **CLI / SDK / Terraform provider** calls (when targeting the shim) is classified as:
>
> 1. **In intersection (real work)** — must dispatch to a real `domain.APIGateway` call against a real backend. No placeholders.
> 2. **Feature genuinely unset** — the resource doesn't have that feature configured; the shim returns the source cloud's *real* "unset" envelope (e.g. `NOT_FOUND` for an absent sub-resource).
> 3. **Out of intersection** — feature has no honest cross-cloud counterpart; the shim returns the source cloud's *real* "not supported" envelope.
>
> A fourth category — "returns something that looks plausible without doing real work" — is a **fake** and must be removed or filed as a BUG.

Service status legend: ✅ implemented & exercised · ⚠ implemented but coverage gap · ◇ deliberately deferred · ◻ pending Phase 9 work · ❌ fake (filed BUG)

## AWS API Gateway v2 frontend (`/v2/...`, restJson1)

| Wire op | Frontend handler | Category | Status |
|---|---|---|---|
| `POST /v2/apis` (CreateApi) | `internal/apigateway/frontends/aws_apigatewayv2.handleCreateApi` | 1 — real | ✅ |
| `GET /v2/apis` (GetApis) | `handleGetApis` | 1 — real | ✅ |
| `GET /v2/apis/{apiId}` (GetApi) | `handleGetApi` | 1 — real | ✅ |
| `DELETE /v2/apis/{apiId}` (DeleteApi) | `handleDeleteApi` | 1 — real | ✅ |
| `POST /v2/apis/{apiId}/integrations` (CreateIntegration) | `handleCreateIntegration` | 1 — real (stored in per-request route accumulator → flushed by CreateDeployment) | ✅ |
| `POST /v2/apis/{apiId}/routes` (CreateRoute) | `handleCreateRoute` | 1 — real (same accumulator) | ✅ |
| `POST /v2/apis/{apiId}/deployments` (CreateDeployment) | `handleCreateDeployment` (flushes accumulator → `domain.DeployGateway`) | 1 — real | ✅ |
| `GET /v2/apis/{apiId}/routes` (GetRoutes) | `handleGetRoutes` | 1 — real | ✅ |
| `GET /v2/apis/{apiId}/integrations` (GetIntegrations) | `handleGetIntegrations` | 1 — real | ✅ |
| `GET /v2/apis/{apiId}/stages` (GetStages) | — | 3 — out of intersection (per-stage deploys/throttling defer) | ◇ |
| `POST /v2/apis/{apiId}/authorizers` (CreateAuthorizer) | — | 3 — out (per-route auth out of Phase 8) | ◇ |
| `POST /v2/apis/{apiId}/cors` | — | 3 — out (per-route CORS deferred) | ◇ |
| Any other (e.g. domain mappings, API mappings) | — | 3 — out | ◇ |

**AWS frontend intersection ops are all category-1 implemented.** No fakes. Out-of-intersection ops 404 today (because no router entry); Phase 9 should switch those to AWS's real `NotFoundException` / `BadRequestException` envelope per BUG-? (filed below).

## GCP API Gateway frontend (`/v1/projects/.../locations/.../gateways/...`)

| Wire op | Frontend handler | Category | Status |
|---|---|---|---|
| `POST /v1/projects/{p}/locations/{l}/gateways?gatewayId=<n>` | `internal/apigateway/frontends/gcp_apigateway.createGateway` | 1 — real | ✅ |
| `GET /v1/projects/{p}/locations/{l}/gateways/{name}` | `getGateway` | 1 — real | ✅ |
| `GET /v1/projects/{p}/locations/{l}/gateways` | `listGateways` | 1 — real | ✅ |
| `DELETE /v1/projects/{p}/locations/{l}/gateways/{name}` | `deleteGateway` | 1 — real | ✅ |
| `POST /v1/projects/{p}/locations/global/apis?apiId=<n>` (Apis.Create) | `createApi` | 1 — real | ✅ |
| `POST /v1/projects/{p}/locations/global/apis/{a}/configs?apiConfigId=<n>` (ApiConfigs.Create) | `createApiConfig` (parses OpenAPI → `domain.DeployGateway`) | 1 — real | ✅ |
| `GET /v1/projects/{p}/locations/global/apis/{a}/configs/{c}` (ApiConfigs.Get) | `getApiConfig` | 1 — real | ✅ |
| `GET /v1/projects/{p}/locations/global/apis/{a}/configs` (ApiConfigs.List) | `listApiConfigs` | 1 — real | ✅ |
| `DELETE /v1/projects/{p}/locations/global/apis/{a}/configs/{c}` (ApiConfigs.Delete) | `deleteApiConfig` | 1 — real | ✅ |
| `DELETE /v1/projects/{p}/locations/global/apis/{a}` (Apis.Delete) | `deleteApi` | 1 — real | ✅ |
| `GET /v1/operations/{op}` (long-running operation polling) | — | 1 — real (intersection includes Operation polling — every cloud's async ops need it) | ◻ Phase 9.4 |
| IAM policy ops (`gateways/{g}/iamPolicy`) | — | 3 — out (cross-cloud IAM is its own phase) | ◇ |
| Stages/auth/CORS analogues | — | 3 — out | ◇ |

**Gap:** the GCP frontend is missing the Api + ApiConfig endpoint families entirely, so `gcloud api-gateway api-configs create` and the equivalent SDK call silently 404. Per the no-fakes rule, this is a real fidelity bug — filed as BUG-9; Phase 9.4-equivalent picks it up.

## Azure APIM frontend (`/subscriptions/.../Microsoft.ApiManagement/service/.../apis/...`)

| Wire op | Frontend handler | Category | Status |
|---|---|---|---|
| `PUT /subscriptions/{s}/.../service/{svc}/apis/{api}` (Api CreateOrUpdate) | `internal/apigateway/frontends/azure_apim.create` | 1 — real | ✅ |
| `GET /...service/{svc}/apis/{api}` | `get` | 1 — real | ✅ |
| `GET /...service/{svc}/apis` | `list` | 1 — real | ✅ |
| `DELETE /...service/{svc}/apis/{api}` | `delete` | 1 — real | ✅ |
| `PUT /...service/{svc}/apis/{api}/operations/{op}` (Operation CreateOrUpdate) | `createOrUpdateOp` (merges → `domain.DeployGateway`) | 1 — real | ✅ |
| `GET /...service/{svc}/apis/{api}/operations/{op}` | `getOp` | 1 — real | ✅ |
| `GET /...service/{svc}/apis/{api}/operations` | `listOps` | 1 — real | ✅ |
| `DELETE /...service/{svc}/apis/{api}/operations/{op}` | `deleteOp` | 1 — real | ✅ |
| `PUT /...service/{svc}/apis/{api}/policies/{policy}` (per-Api policy XML) | — | 3 — out (vendor-specific XML transform DSL) | ◇ |
| Subscriptions, Products, Tags, Groups | — | 3 — out (not in Phase 8 intersection) | ◇ |

**Gaps:**
- Operations subresource missing entirely (BUG-10).

## Out-of-intersection envelope checklist (for Phase 9.2-A audit)

For each frontend, the catch-all "no route matched" today returns:

| Frontend | Current 404 envelope | Source cloud's real envelope | Fidelity |
|---|---|---|---|
| AWS APIGW v2 | `restJson1 { "Message": "..." }` 404 | `{ "Message": "...", "__type": "NotFoundException" }` 404 | ⚠ missing `__type` |
| GCP APIGW | `gcpErrorResponse{ Code: 404, Status: "NOT_FOUND" }` | `gcpErrorResponse{ Code: 404, Status: "NOT_FOUND" }` | ✅ |
| Azure APIM | `armErrorResponse{ Code: "ResourceNotFound" }` 404 | `armErrorResponse{ Code: "ResourceNotFound" }` 404 | ✅ |

**Action item:** Add `__type` to AWS restJson1 404 envelope. Tracked in Phase 9.2-A follow-up (will file as BUG-11 if not already).

## What this inventory commits us to

Phase 8 closed with the matrix tests proving (frontend → DeployGateway → backend → end-to-end HTTP) works for the AWS frontend's `Create*` flow. The inventory above shows the GCP and Azure frontends ship **fewer route-deployment ops than their official tooling expects** — Phase 9 must close BUG-9 and BUG-10 so that:

- `gcloud api-gateway api-configs create` works against the shim, with route deployment dispatched to `domain.DeployGateway`.
- `armapimanagement APIOperationClient.CreateOrUpdate` works against the shim, same dispatch.

After those land, the cross-cloud migration story for API gateways is: user writes AWS-shaped Terraform → shim points at GCP backend → backend deploys to real GCP API Gateway (with OpenAPI doc generated from routes). Or any other combination. Each frontend in scope has to actually carry the route-deployment surface; that's what makes "common intersection" real.

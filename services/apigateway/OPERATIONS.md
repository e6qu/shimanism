# API Gateway — operation and feature mapping

> The intersection footprint shimanism's `apigateway` service can cover, across the four backends in scope:
> **AWS API Gateway HTTP API v2**, **GCP API Gateway**, **Azure API Management** (Consumption tier), **Envoy Gateway** (K8s) as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error.
>
> The shim itself is stateless — the gateway spec lives in the backend, not in shimanism.

## Declarative-replace model

API Gateway is structurally different from earlier phases: the deployable unit isn't a single resource with a lifecycle (like a DB instance or function) but a **full routing configuration** — routes, backends, methods, paths, integrations. PLAN.md flags this as the load-bearing design choice:

> `deploy(gateway_spec)` swaps the entire routing table atomically.

The shim accepts a `Gateway` resource with embedded `Routes`; `DeployGateway` replaces the entire route set in one call. Partial route mutations (add/remove a single route on a live gateway) are out of intersection — the cross-cloud semantics differ too much (AWS does PATCH-style updates; GCP requires re-publishing a new API config; Azure ARM lets you template; Envoy Gateway is `Gateway` + `HTTPRoute` CRs).

This is "control plane only with HTTP data plane" same as Phases 5–7 — `deploy()` returns an endpoint URL; clients send HTTP requests; the gateway dispatches per the configured routes.

## The intersection — 5 operations

| Domain op | AWS API Gateway v2 | GCP API Gateway | Azure API Management | Envoy Gateway |
|---|---|---|---|---|
| **CreateGateway**(name, spec) | `CreateApi` + `CreateRoute` × N + `CreateIntegration` × N (one CreateDeployment to publish) | `apis.create` + `apiConfigs.create` (config-as-document) + `gateways.create` | `Api_CreateOrUpdate` + `ApiOperation_CreateOrUpdate` × N + publish via `ApiPolicy` | `Gateway` CR + `HTTPRoute` CR × N |
| **DeleteGateway**(name) | `DeleteApi` (cascades to routes/integrations) | `gateways.delete` + `apiConfigs.delete` + `apis.delete` | `Api_Delete` | `Gateway` CR delete (cascades) |
| **DescribeGateway**(name) | `GetApi` + paginate `GetRoutes`/`GetIntegrations` | `gateways.get` + `apiConfigs.get` | `Api_Get` + paginate `ApiOperation_ListByApi` | `Gateway` CR get + `HTTPRoute` CR list-by-parent |
| **ListGateways**(prefix?) | `GetApis` | `gateways.list` | `Api_ListByService` | `Gateway` CR list |
| **DeployGateway**(name, spec) | wipe routes/integrations + recreate from spec + `CreateDeployment` | replace `apiConfigs` + `gateways.patch` | replace operations under `Api` | replace `HTTPRoute` CRs for the parent `Gateway` |

`DeployGateway` is what makes the declarative-replace model honest: a full spec goes in, the routing table comes out matching the spec atomically (each backend handles "atomically" differently but the visible behaviour is consistent — all-or-nothing route swap).

## Gateway spec

```go
type GatewaySpec struct {
    Name   string  // gateway resource name
    Routes []Route // ordered list — first match wins
}

type Route struct {
    Method    string // "GET" | "POST" | "PUT" | "DELETE" | "PATCH" | "ANY"
    Path      string // "/users/{id}" — path templates with {var} segments
    Backend   string // an HTTPS URL the gateway proxies to
}
```

Intersection-only fields. Per-route timeouts, auth, throttling, request transformations, response transformations, CORS, custom domain mapping — all **out of intersection** at this phase. The exit criterion is "routes dispatch HTTP to backends correctly"; richer semantics are deferred.

## Endpoint metadata

`DescribeGateway` returns:

```go
type Endpoint struct {
    URL string // base URL clients prefix their requests with
}
```

URL forms across the backends:

- **AWS**: `https://<api-id>.execute-api.<region>.amazonaws.com`
- **GCP**: `https://<gateway-id>-<hash>.gateway.dev`
- **Azure**: `https://<service>.azure-api.net`
- **Envoy Gateway**: the `Gateway` CR's `status.addresses[0].value` — usually a service IP/DNS the gateway listens on

## What's emphatically out of intersection

- **AWS**: REST API (v1), WebSocket APIs, custom authorizers, Lambda authorizers, mutual TLS, throttling, usage plans, API keys, X-Ray.
- **GCP**: API config history / rollback, CORS via gateway config, IAM-gated invocation, Cloud Endpoints (ESPv2-based), telemetry exporters.
- **Azure**: Developer portal, products, subscriptions/quotas, identity providers, named values vault, policies (rate limiting, transforms), revisions, named groups, gateways (self-hosted).
- **Envoy Gateway**: BackendRef weighting beyond a single backend, TLS policies, retries/timeouts, request mirroring.

Per-route auth, throttling, transforms — out of intersection. Honor the request method/path/backend; reject everything else with the source cloud's `InvalidParameter` / `BAD_REQUEST` / `400 Bad Request`.

## Sub-phase plan (Phase 8)

| Sub | Headline |
|---|---|
| 8.0 | Scope + intersection mapping (this doc) + sub-phase plan. |
| 8.1 | Vendor AWS API Gateway v2 Smithy spec. GCP via `google.golang.org/api/apigateway/v1`; Azure via `armapimanagement`. |
| 8.2 | Domain interface (`internal/apigateway/domain/`): `APIGateway`, `Gateway`, `Route`, `Endpoint`. |
| 8.3 | inmem backend + AWS API Gateway v2 frontend (restJson1) + SDK conformance. |
| 8.4 | Envoy Gateway backend (K8s peer) via dynamic client + unstructured `Gateway` / `HTTPRoute` CRs. |
| 8.5 | AWS API Gateway v2 passthrough backend. |
| 8.6 | GCP API Gateway backend. |
| 8.7 | Azure API Management backend. |
| 8.8 | GCP API Gateway frontend (REST/JSON). |
| 8.9 | Azure API Management REST frontend (ARM URL shape). |
| 8.10 | Matrix conformance. |
| 8.11 | CLI conformance — `aws apigatewayv2`, `gcloud api-gateway`, `az apim`. |
| 8.12 | Terraform conformance — `aws_apigatewayv2_api` + routes, `google_api_gateway_api`. |
| 8.13 | `cmd/shim apigateway` subcommand. Default `:9700`. |
| 8.14 | CI lane `conformance-envoy`: kind + Envoy Gateway. |
| 8.15 | **HTTP-route exit criterion test**: deploy a gateway with one route to a backend serving `pong`; HTTP-invoke the gateway's URL through that path; assert `pong`. Phase-8 exit criterion. |
| 8.16 | Phase 8 closer. |

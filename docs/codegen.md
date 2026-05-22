# Codegen

shimanism generates server stubs from each cloud's published specs. Hand-written code is restricted to per-operation `translate.go` files (or per-frontend adapters that implement the generated `ServerInterface`). All three lanes — AWS Smithy, Azure OpenAPI v2, GCP Discovery — are wired through `make codegen` and produce deterministic output. See [`doc/CODEGEN.md`](../doc/CODEGEN.md) for the full architecture; this file is a high-level overview.

## Current state

| Service | AWS Smithy | Azure OpenAPI | GCP Discovery |
|---|---|---|---|
| storage | ✅ `services/storage/gen/` | ✅ `gen/azure/azure_blob.gen.go` (Blob data-plane, 1.1 MB) | ✅ `gen/gcp/gcp_gcs.gen.go` (82 routes) |
| secrets | ✅ `services/secrets/gen/` | ✅ `gen/azure/azure_keyvault.gen.go` | ✅ `gen/gcp/gcp_secretmanager.gen.go` (32 routes) |
| queue | ✅ | ✅ Service Bus (shared) | ✅ (46 routes) |
| pubsub | ✅ | ✅ Service Bus (shared) | ✅ (46 routes) |
| rdbms | ✅ | ✅ PostgreSQL FlexibleServers ARM | ✅ Cloud SQL (74 routes) |
| cache | ✅ | ✅ Redis ARM | ✅ Memorystore (45 routes) |
| functions | ✅ | ✅ ContainerApps ARM | ✅ Cloud Run (58 routes) |
| apigateway | ✅ | ✅ APIM (minimal) | ✅ (30 routes) |

8/8 AWS frontends ship fully migrated through `gen.HandlerWithOptions`. 1/8 Azure frontend (`azure_keyvault`) ships fully migrated; the other 7 keep hand-written dispatch with the gen inventory as the spec-drift contract — adapter migration is Phase 13 follow-on.

`make codegen` regenerates every spec-driven file across all three lanes. `make codegen-check` (and CI's `codegen deterministic` job) asserts the output is byte-identical to the committed copy.

## Toolchain

- `cmd/codegen/main.go` — AWS Smithy emitter. Handles all four protocols (REST-XML / awsJson1_0 / awsJson1_1 / awsQuery / restJson1) via per-protocol templates in `internal/codegen/emit/`.
- `cmd/azure-codegen/main.go` — Azure preprocessor (8 stages: inline external refs, x-ms-examples skip, x-ms-enum promotion with parameter/header gating, parameter/definition name dedup, ARM `allOf` flatten, x-ms-paths flatten, empty-AllOf normalize, deterministic walk) + `kin-openapi/openapi2conv.ToV3` + `oapi-codegen` as a library.
- `cmd/gcp-codegen/main.go` — Discovery JSON → routing-only Go. Reuses `google.golang.org/api/<svc>/v1` wire types per AGENTS.md #11. Emits `Routes []Route` + `Match()` / `MatchAll()` helpers.
- `cmd/inject-provenance/main.go` — writes a `_provenance` top-level key into each vendored spec JSON; idempotent; CI guard blocks merges without it.
- `scripts/fetch-{aws,azure,gcp}-*.sh` — resolve `<ref>` to a SHA against the upstream repo, download the spec, seed SOURCES.md, and run inject-provenance.

## Why codegen

Three reasons:

1. **Fidelity.** The shim must match the cloud's published API exactly — response shapes, error envelopes, HTTP status codes, async-operation semantics, path templates, query-parameter names, header names. Generating from the upstream spec eliminates the "is the field name right?" class of bug.
2. **Drift detection.** When the upstream cloud changes its spec, regeneration produces a diff. The translation table (per-operation `translate.go`) is the only thing to review by hand.
3. **Reuse over reinvention.** Per [PLAN.md § locked-in decisions](../PLAN.md#locked-in-decisions): the cloud's own spec is the source of truth. We never fork or maintain a parallel implementation.

## Spec inputs

Always vendored from upstream-canonical sources:

| Cloud | Spec format | Source |
|---|---|---|
| AWS | Smithy JSON | `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models` |
| GCP REST | Discovery JSON | The documented Discovery doc URL per API |
| GCP gRPC | Protobuf | `googleapis/googleapis` |
| Azure | OpenAPI v3 | `Azure/azure-rest-api-specs` |

Specs live under `services/<svc>/spec/`. They're vendored, not forked — when upstream releases a new revision, we vendor it again and review the diff.

## Wire types: reuse first

Prefer reusing the official Go SDK's wire-type structs over re-emitting equivalents:

| Cloud | Reusable wire-type package |
|---|---|
| AWS | `github.com/aws/aws-sdk-go-v2/service/<svc>/types` |
| GCP REST | `google.golang.org/api/<svc>/v1` (generated from Discovery) |
| GCP gRPC | The proto-generated structs in `cloud.google.com/go/<svc>` |
| Azure | The SDK's internal `generated/` package (the types `azblob` etc. use under the hood) |

Re-emit server-side types **only when SDK types fight server-side handling** — for example, client-only fields, pointer-heavy shapes that don't round-trip, or struct tags that target the SDK's middleware rather than direct (un)marshalling. When re-emitting, generate from the same spec the SDK is generated from, not from a copy.

## Server-side codegen

For each spec format, the most authoritative existing generator:

| Spec format | First choice | Fallback rationale |
|---|---|---|
| Smithy (AWS) | Custom emitter (no official Smithy → Go *server* generator exists; `smithy-go` is client-side) | n/a — custom is the only option |
| OpenAPI v3 (Azure) | `oapi-codegen` server-stubs | Custom emitter when stubs can't match the handler shape after reasonable adapter glue |
| Discovery / protobuf (GCP) | Reuse `google.golang.org/api` wire types directly; emit only the routing + dispatch layer | Custom emitter when the generated types are too SDK-coupled to import cleanly |

The custom emitter lives in `internal/codegen/`. Each service has a `services/<svc>/codegen.json` manifest that tells the emitter which operations to generate stubs for. Generated stubs land in `services/<svc>/gen/`.

## Auth verification

Use the cloud's official signer/verifier libraries — **never roll a SigV4 / OAuth2 / SharedKey implementation**:

| Cloud | Signer / verifier |
|---|---|
| AWS | `aws-sdk-go-v2/aws/signer/v4` |
| GCP | `golang.org/x/oauth2` |
| Azure | The signer in `azure-sdk-for-go/sdk/azcore/auth` and the SharedKey verifier exposed by the storage SDK |

## Validation

The cloud's spec carries field-level constraints (string lengths, enum sets, pattern regexes). Honor them at the wire-decode boundary so an invalid request fails with the **source cloud's own error vocabulary**, not a generic 500. When the spec generator emits validation (Smithy + OpenAPI do), wire it in.

## Regenerating

```sh
make codegen
```

Today this only regenerates the storage stubs (the emitter walks `services/storage/codegen.json`). As other services gain spec-driven codegen, the `make` target will fan out. The diff is what reviewers look at — generated stubs vs hand-written `translate.go` should be cleanly separable.

When upstream changes a spec:

1. Update the vendored spec under `services/<svc>/spec/`.
2. Run `make codegen`.
3. Review the diff: are there new operations? Renamed fields? Deprecated paths?
4. Update the per-operation `translate.go` files if needed.
5. Run conformance.

Stale generated code (where the spec moved and the gen didn't follow) is a bug — see [BUGS.md § class-of-bug rules](../BUGS.md#class-of-bug-rules-carried-forward).

## When *not* to reuse

Reuse is a tool, not a contract. If reusing a piece of SDK or generator output forces the shim to lie (synthetic responses, swallowed errors, fabricated success), drop the reuse and emit our own honest implementation. The fidelity rule beats the reuse rule.

## Cross-link

- [PLAN.md § locked-in decisions](../PLAN.md#locked-in-decisions) — why codegen is the rule, not the exception.
- [AGENTS.md § spec is the source of truth](../AGENTS.md#spec-is-the-source-of-truth) — the hard rule for agents.
- [docs/development.md § adding a new operation](development.md#adding-a-new-operation) — where codegen fits in the recipe.
- [docs/testing.md](testing.md) — the conformance lanes that verify each emitted stub actually round-trips.

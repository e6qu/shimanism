# Codegen

shimanism's plan is for server stubs to come from the cloud providers' own published specs, with hand-written code restricted to per-operation `translate.go` files. **Today this is partially true: only the storage service has spec-driven generated stubs.** The other services compose their wire layer by hand using the cloud SDKs' wire-type packages directly. Migrating the rest to codegen is on the roadmap.

## Current state vs roadmap

| Service | Generated stubs (today) | Spec source | Status |
|---|---|---|---|
| storage | ✅ `services/storage/gen/` | Smithy (AWS) → custom emitter | Active |
| secrets | ❌ Hand-written wire | AWS Smithy + GCP gRPC + Azure REST | Roadmap |
| queue | ❌ Hand-written wire | Smithy (`awsJson1_0`) + GCP REST + Azure REST | Roadmap |
| pubsub | ❌ Hand-written wire | AWS awsQuery XML + GCP REST + Azure REST | Roadmap |
| rdbms | ❌ Hand-written wire | AWS awsQuery XML + GCP REST + Azure ARM | Roadmap |
| cache | ❌ Hand-written wire | AWS awsQuery XML + GCP REST + Azure ARM | Roadmap |
| functions | ❌ Hand-written wire | AWS restJson1 + GCP REST + Azure ARM | Roadmap |
| apigateway | ❌ Hand-written wire | AWS restJson1 + GCP REST + Azure ARM | Roadmap |

`make codegen` regenerates the storage stubs. The custom emitter at `cmd/codegen/main.go` is Smithy-only today; extending to OpenAPI v3 (Azure) and Discovery / protobuf (GCP) is roadmap work.

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

# Phase 11 — Tighten the wire boundary

> **Goal:** Replace hand-written wire layers with spec-driven generated stubs across every service, and wire real signature verification at the new decode boundary. The two changes are coupled: codegen establishes a uniform decode boundary; signature verification belongs at that boundary; doing both per service lands them together instead of retrofitting verification into hand-written handlers we already plan to replace.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · roadmap [PLAN.md](PLAN.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Status (draft)

This document is a **draft** opened after Phase 10 closed. Scope and sub-phases will be refined in the same review cadence as Phase 10 (write → submit to codex → apply feedback).

## Why now

Phase 10 closed cross-cloud `terraform apply` on every service. The remaining shape of the shim is honest at the *behavior* level — what each handler does once it has a request. What it is **not** yet honest about is the **boundary**:

- **Wire validation is uneven.** Only storage parses its requests through generated stubs that enforce the cloud's spec-level field constraints. The other 7 services hand-write the wire layer, so spec drift in any field name, length limit, or enum set is invisible until a real client sends a real request.
- **Signature verification is absent.** Every frontend accepts requests without validating SigV4 / OAuth2 / SharedKey. SDK + CLI + Terraform conformance papers over this with `skip_credentials_validation`, `option.WithoutAuthentication()`, and stub `fakeAzureCred` tokens. Any "shim is safe in front of production traffic" claim is unfounded today — recorded as **BUG-18 (P1)**.

Both gaps live at the same place in the request lifecycle: the decode boundary that converts the cloud's wire shape into the shim's neutral domain types. Solving them together is one PR per service; solving them separately is two PRs per service plus the throwaway scaffolding of the first.

## Scope

| | |
|---|---|
| Services in scope | All 8. |
| Layer in scope | Wire decode boundary: spec ingest, server stubs, validation, signature verification, error envelope. |
| Out of scope | Backend translation logic (`translate.go` files stay hand-written). Adding new shimmed operations. Real-cloud signature verification tests (Track A). |
| Driver matrix | Same as Phase 10: 3 frontends × 5 backends × 3 driver types per service. Spec-driven decode shouldn't change any conformance outcome — if the existing tests pass against generated stubs, the migration is honest. |

## Codegen extension order (locked-in)

Per user direction:

1. **OpenAPI v3 (Azure) via `oapi-codegen`.** Most mature off-the-shelf generator; smallest custom-code surface; covers Azure across all 7 hand-written services.
2. **AWS Smithy emitter extension.** The custom emitter at `internal/codegen/` is Smithy-only and already exists; extending it to AWS surfaces beyond S3 (Secrets Manager, SQS, SNS, RDS, ElastiCache, Lambda, APIGW v2) is a routing-table addition per surface.
3. **GCP Discovery / protobuf.** Reuse `google.golang.org/api/<svc>/v1` wire types directly; emit only the routing + dispatch layer. Last because it's the lightest emitter (no struct re-emission) and benefits from patterns established by the Azure + AWS work.

## Hard problems & how each is approached

### 1. `oapi-codegen` server-stub shape vs the shim's handler signature

`oapi-codegen` emits server interfaces in the style `type ServerInterface interface { GetSecret(c *gin.Context) }` (or stdlib equivalents). The shim's handlers operate on `(http.ResponseWriter, *http.Request)` with neutral domain dispatch. Two options:

- **(A) Adapter glue.** Generate `oapi-codegen` stubs into `services/<svc>/gen/azure/`; write a thin adapter that satisfies the generated interface and dispatches to the existing frontend logic. Keeps the generator clean; adapter is ~one method per operation.
- **(B) Custom OpenAPI emitter.** Drop `oapi-codegen` and extend the in-tree `internal/codegen/` to read OpenAPI v3, emit `(w, r)` handlers directly. More code; perfect fit.

**Default plan: (A) first; switch to (B) only if the adapter glue grows past a small constant per operation.** [AGENTS.md § Server-side codegen](AGENTS.md#reuse-over-reinvention) — "first choice `oapi-codegen`; fall back to custom when generated stubs can't match the handler shape after reasonable adapter glue."

### 2. Signature verification across three different auth schemes

The cloud-official verifier libraries are listed in [AGENTS.md § Reuse over reinvention](AGENTS.md#reuse-over-reinvention):

| Cloud | Library | Verification mode |
|---|---|---|
| AWS | `aws-sdk-go-v2/aws/signer/v4` | Recompute SigV4 over the request, compare to `Authorization` header. |
| GCP | `golang.org/x/oauth2` + Google identity-platform JWKS | Validate bearer JWT signature against Google's public JWKS; check `aud` matches the shim's configured audience. |
| Azure | `azcore/auth` (Bearer) + storage SDK's SharedKey verifier (for storage) | Recompute SharedKey or validate AAD token. |

What "verify" means here is **incoming request authentication only** — i.e. the shim refuses unsigned / wrong-key / wrong-audience requests with the source cloud's own 403 / 401. The shim does **not** propagate the caller's credentials to the backend; the backend uses its own configured identity. That separation is honest: the shim's job is to be the cloud's API surface, which includes its auth surface.

**Test harness implication.** SDK + CLI + Terraform conformance lanes today bypass auth via the various `skip_credentials_validation` / `WithoutAuthentication` knobs. Phase 11 conformance must **either**:

- Issue real-signed requests via stub credentials the verifier accepts (a deterministic test signing key the shim trusts in test mode). Honest because the verification path is exercised end-to-end.
- Keep auth-bypass mode under an explicit `SHIMANISM_TEST_UNAUTHENTICATED=1` flag and have a separate conformance lane that exercises the verifier with real signatures.

Decision (per codex review pending): default to the first — single signed-request test path, no bypass flag.

### 3. Spec drift between vendored spec and the SDK version

If the cloud bumps a spec field's max length, the shim's vendored spec lags until the next refresh. The current Smithy emitter handles this via the spec-vendoring pre-commit hook; same pattern applies to the Azure and GCP vendoring. The Renovate config doesn't track spec sources today — that's a follow-on (filed during 11.0 as a tracked task, not a BUG yet).

## Sub-phases

| Sub | Headline |
|---|---|
| **11.0** | Scope baseline. This document. Codex-reviewed before any code lands. |
| **11.1** | BUG-15 walk: GCP Pub/Sub provider-default audit (`message_retention_duration`, `expiration_policy`, `retain_acked_messages`, `enable_message_ordering`). Outcome: either close BUG-15 or document the provider-asymmetry root cause in `services/queue/APPLY_INTERSECTION.md` and reclassify. BUG-8 status update pinned to Track A (no code change). |
| **11.2** | OpenAPI v3 emitter foundation. Decide adapter glue vs. custom emitter against `oapi-codegen` v2; land the chosen path as `services/secrets/gen/azure/` PoC stubs for the Azure Key Vault secrets surface. |
| **11.3** | **Secrets: first service end-to-end spec-driven.** AWS Secrets Manager via extended Smithy emitter → `services/secrets/gen/aws/`. Azure Key Vault via 11.2 OpenAPI pipeline → `services/secrets/gen/azure/`. GCP Secret Manager via reused `google.golang.org/api/secretmanager/v1` wire types + emitted routing layer → `services/secrets/gen/gcp/`. Hand-written wire deleted. Conformance unchanged. |
| **11.4** | **BUG-18 signature verification at the secrets decode boundary.** SigV4 verifier on AWS frontend; OAuth2 JWT verifier on GCP frontend; SharedKey + Bearer verifier on Azure frontend. Conformance tests issue signed requests; auth-bypass flag dropped from the secrets lane. |
| **11.5** | Roll forward to queue. Smithy `awsJson1_0` for SQS; OpenAPI for Azure Service Bus admin; Discovery for GCP Pub/Sub. Signature verification per frontend. |
| **11.6** | Roll forward to pubsub. Same shape; AWS awsQuery XML adds a Smithy-emitter sub-task (awsQuery serialization is supported by Smithy 2.0 — verify before scoping). |
| **11.7** | Roll forward to rdbms. AWS awsQuery XML; GCP Cloud SQL Admin REST; Azure ARM OpenAPI. |
| **11.8** | Roll forward to cache. AWS awsQuery XML (ElastiCache); GCP Memorystore REST; Azure ARM OpenAPI. |
| **11.9** | Roll forward to functions. AWS restJson1 (Lambda); GCP Cloud Run REST; Azure ARM OpenAPI. |
| **11.10** | Roll forward to apigateway. AWS restJson1 (APIGW v2); GCP API Gateway REST; Azure APIM ARM OpenAPI. |
| **11.11** | Storage retrofit. Apply signature verification to the existing `services/storage/gen/` Smithy stubs. Drop the corresponding auth-bypass knobs from storage conformance. |
| **11.12** | Closer. All 8 services spec-driven; `make codegen` regenerates every service from vendored specs; BUG-18 closed; storage retrofit landed; auth-bypass flag removed from all conformance lanes. |

## Exit criteria

- All 8 services have `services/<svc>/gen/{aws,gcp,azure}/` generated stubs; no hand-written wire layer remains.
- Every frontend rejects unsigned + wrong-key requests with the source cloud's own 401/403 error envelope.
- `make codegen` regenerates every service from vendored specs in one command.
- Conformance lanes use real signing; no `skip_credentials_validation` / `WithoutAuthentication` / `fakeAzureCred` stubs.
- BUG-18 closed in [BUGS.md](BUGS.md).
- Per-service `INTERSECTION.md` + `APPLY_INTERSECTION.md` reconciled with any spec-driven fidelity discoveries.

## Open questions (decide during 11.0 review)

- **`oapi-codegen` adapter glue vs. custom OpenAPI emitter.** Land 11.2 with adapter glue first; revisit if the adapter grows past ~3 LOC per operation.
- **AWS awsQuery via Smithy 2.0.** Smithy 2.0 supports awsQuery as a protocol; confirm the existing custom emitter can route through that protocol path before scoping 11.6.
- **Signed-conformance test signing keys.** Generate a project-owned deterministic test key in CI; the shim trusts it only when `SHIMANISM_TEST_TRUSTED_KEY` is set. Real-cloud lanes (Track A) use real signatures.
- **Renovate coverage of vendored specs.** Today Renovate tracks Go modules + GitHub Actions; vendored specs in `services/<svc>/spec/` are manual. Open a tracked task during 11.0 to wire spec freshness into CI (compare vendored hash vs. upstream HEAD; alert on drift).
- **GCP gRPC vs. REST.** Some GCP services (Pub/Sub) have both protobuf-gRPC and REST surfaces. The shim today emits REST; gRPC would be a future expansion. Out of scope for Phase 11.

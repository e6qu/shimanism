# shimanism — Roadmap

> **Goal:** Protocol-translation shims that let unmodified cloud SDKs / CLIs / Terraform providers run against AWS, GCP, Azure, or Kubernetes-native backends by pointing them at a shim endpoint instead of the original service.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Guiding principles

1. **Spec is the contract.** Each shimmed service has a canonical published spec (AWS Smithy, GCP protobuf, Azure OpenAPI/ARM). Server-side wire layer is generated from that spec; hand-written code is translation logic only.
2. **Fidelity to the source API.** The shim speaks the cloud's API exactly. Error shapes, response headers, status codes, async semantics — all match. Where the call can't be honored against the chosen backend, return the source cloud's own error in its own vocabulary. Never fabricate success.
3. **Real backends, not emulators.** Translated calls drive a real, comparable service: another cloud's equivalent, a Kubernetes operator, or a self-hosted system. The shim itself holds no state of record.
4. **Conformance from day one.** Every shimmed operation is exercised in the same commit by (a) the cloud's official SDK, (b) its CLI, and (c) its Terraform provider, against every backend in scope.
5. **Intersection only.** Shim the features common to AWS / GCP / Azure / the chosen K8s peer. Out-of-intersection features fail loud with the source cloud's own error.
6. **Kubernetes is a first-class backend.** Every service has a K8s peer on equal footing with the three clouds. Third-party OSS peers where they fit (MinIO, Vault, NATS, CloudNativePG, Knative, Envoy Gateway); otherwise the in-tree [`peers/shimakit/`](peers/shimakit/) framework with concrete peers named `shima<service>`.
7. **No fakes, no fallbacks, no degraded modes.** If a dependency is required, it is required.
8. **One source spec, multiple adapters.** Codegen regenerates from upstream specs; agents own translation tables.
9. **Single-branch rule.** One branch per phase / sub-phase. Many commits, one PR. User merges.
10. **Continuity always.** STATUS / DO_NEXT / WHAT_WE_DID / BUGS update at every significant chunk.
11. **One service per phase, every frontend × every backend.** Each service-phase ships across **all three source-cloud frontends** translating into **all four backends** — the full 3 × 4 matrix. Each frontend is tested by its own cloud's official tooling (3 frontends × 3 driver types × 4 backends = 36 driver-backend combinations) before the phase closes.

## Locked-in decisions

| # | Decision | Value |
|---|----------|-------|
| 1 | Implementation language | **Go** |
| 2 | Spec sources | Pull upstream, never fork: AWS Smithy JSON from `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models`; GCP protobuf from `googleapis/googleapis`; Azure OpenAPI from `Azure/azure-rest-api-specs`. |
| 3 | Codegen | spec → typed Go server stubs (handlers, request/response types, error envelopes). Hand-written code restricted to per-operation `translate.go`. |
| 4 | Backend abstraction | Per-service `Backend` interface in domain terms. Cross-service generalization is premature. |
| 5 | Test fidelity rings | Per-PR: recorded interactions + unit. Nightly: live cloud accounts. Pre-release: vendor SDK integration suites against shim. |
| 6 | Deployment | Single Go binary + Helm chart. SaaS deferred. |
| 7 | Repo layout | Monorepo. `services/<service>/` per shim; shared `internal/codegen/`, `internal/harness/`. |
| 8 | License | AGPL-3.0. |
| 9 | Passthrough mode | Ship per service when there's a real reason (auth interception, observability injection). |
| 10 | Agent permissions for spec updates | Human-in-loop on upstream-spec change. Agents propose; humans review translation-table delta. |
| 11 | **Reuse-over-reinvention** | Lean on the cloud's official spec + official Go SDK whenever they fit. Each frontend's wire layer is generated from the cloud's canonical spec (AWS Smithy → custom emitter; GCP Discovery doc → `google.golang.org/api` raw types where pragmatic; Azure OpenAPI → `oapi-codegen` or equivalent). Auth verification uses the cloud's official verifier (e.g. `aws-sdk-go-v2/aws/signer/v4`). See [AGENTS.md § Reuse over reinvention](AGENTS.md#reuse-over-reinvention). |
| 12 | **Stateless shim** | The shim binary holds no state of record. All persistent state lives in the backend. No sidecar database, no shim-managed key/value namespace, no in-process cache that the shim treats as authoritative. Cross-cloud mappings derive at request time from data the backend already keeps. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless). |
| 13 | **In-tree K8s peer when OSS doesn't fit** | Third-party OSS first; when none fits, the in-tree [`peers/shimakit/`](peers/shimakit/) framework provides versioned named bytes + structured metadata + multi-namespace + soft-delete. Concrete peers built on top are named `shima<service>`. Each is its own Go module so operators can pin / deploy / upgrade independently. |

## Service phases (1-8 — all closed)

One service per phase; full 3 × 4 matrix; SDK + CLI + Terraform per frontend.

| # | Service | Frontends | Backends in scope |
|---|---|---|---|
| 1 | Object storage | AWS S3 · GCS · Azure Blob | AWS S3 · GCS · Azure Blob · MinIO (K8s peer) |
| 2 | Secrets | AWS Secrets Manager · GCP Secret Manager · Azure Key Vault | same three clouds + Vault |
| 3 | Queue | AWS SQS · GCP Pub/Sub (pull) · Azure Service Bus queues | same three clouds + NATS JetStream |
| 4 | Pub/Sub | AWS SNS · GCP Pub/Sub · Azure Service Bus topics | same three clouds + NATS core |
| 5 | Managed RDBMS (control plane) | AWS RDS · Cloud SQL Admin · Azure DB Admin | same three clouds + CloudNativePG |
| 6 | Managed Redis (control plane) | AWS ElastiCache · GCP Memorystore Admin · Azure Cache for Redis Admin | same three clouds + Redis Operator |
| 7 | Functions | AWS Lambda · GCP Cloud Run · Azure Container Apps | same three clouds + Knative |
| 8 | API Gateway | AWS API Gateway v2 · GCP API Gateway · Azure API Management | same three clouds + Envoy Gateway |

Each phase's per-frontend wire layer is hand-written today; spec-driven codegen for the non-Smithy formats is Phase 11 work. Per-service implementation detail lives in `services/<svc>/` (intersection contract in `INTERSECTION.md`, write-side contract in `APPLY_INTERSECTION.md`); per-phase narrative in [WHAT_WE_DID.md](WHAT_WE_DID.md); architecture in [`doc/CROSS_CLOUD_ROUTING.md`](doc/CROSS_CLOUD_ROUTING.md).

## Cross-cutting phases

| # | Headline | Status |
|---|---|---|
| 9 | Cross-cloud `terraform import` honest end-to-end across all 8 services. `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` is the exit criterion. Per-service `INTERSECTION.md` + `MIGRATION.md` audits. | Closed PR #13 + PR #16. |
| 10 | Cross-cloud `terraform apply` honest end-to-end across all 8 services. `TestCrossCloudApply_Roundtrip_StorageAWStoGCS` is the exit criterion. Per-service `APPLY_INTERSECTION.md`. Full developer + contributing docs under `docs/`. | Closed PR #17. |
| **11** | **Tighten the wire boundary.** Spec-driven codegen across every service + signature verification (BUG-18) at the new decode boundary. | **In-flight on `phase-11`.** |

## Phase 11 — Tighten the wire boundary

> **Goal:** Replace hand-written wire layers with spec-driven generated stubs across every service, and wire real signature verification at the new decode boundary. The two changes are coupled at the same point in the request lifecycle; doing them per-service lands them together instead of retrofitting verification into hand-written handlers we plan to replace.

### Why now

Phase 10 closed cross-cloud `terraform apply` on every service. What remains uneven is the **boundary**:

- **Wire validation.** Only storage parses requests through generated stubs that enforce spec-level field constraints. The other 7 services hand-write the wire layer, so spec drift in any field name, length limit, or enum set is invisible until a real client sends a real request.
- **Signature verification.** Every frontend accepts requests without validating SigV4 / OAuth2 / SharedKey. Conformance papers over this with `skip_credentials_validation`, `option.WithoutAuthentication()`, and stub `fakeAzureCred` tokens — recorded as **BUG-18 (P1)**. Any "shim is safe in front of production traffic" claim is unfounded today.

Both gaps live at the same place in the request lifecycle. Solving them together is one PR per service; solving them separately is two PRs per service plus the throwaway scaffolding of the first.

### Codegen extension order (locked-in)

1. **OpenAPI v3 (Azure) via `oapi-codegen`.** Most mature off-the-shelf generator; smallest custom-code surface; covers Azure across all 7 hand-written services.
2. **AWS Smithy emitter extension.** The custom emitter at `internal/codegen/` is Smithy-only and already exists; extending it to AWS surfaces beyond S3 is a routing-table addition per surface.
3. **GCP Discovery / protobuf.** Reuse `google.golang.org/api/<svc>/v1` wire types directly; emit only the routing + dispatch layer.

### Sub-phases

| Sub | Status | Headline |
|---|---|---|
| 11.0 | ◐ | Scope baseline (this section). Codex review pending before code lands. |
| 11.1 | ◻ | BUG-15 walk: GCP Pub/Sub provider-default audit (`message_retention_duration`, `expiration_policy`, `retain_acked_messages`, `enable_message_ordering`). Either close BUG-15 or document the provider-asymmetry root cause and reclassify. BUG-8 status update pinned to Track A (no code change). |
| 11.2 | ◻ | OpenAPI v3 emitter foundation. `oapi-codegen` adapter pilot on Azure Key Vault secrets surface → `services/secrets/gen/azure/`. Decide adapter glue vs custom emitter; default to adapter glue, switch only if it grows past ~3 LOC per operation. |
| 11.3 | ◻ | **Secrets: first service end-to-end spec-driven.** AWS Secrets Manager via extended Smithy emitter; Azure Key Vault via 11.2 OpenAPI pipeline; GCP Secret Manager via reused `google.golang.org/api/secretmanager/v1` wire types + emitted routing layer. Hand-written wire deleted. |
| 11.4 | ◻ | **BUG-18 signature verification at the secrets decode boundary.** SigV4 (AWS), OAuth2 JWT (GCP), SharedKey + Bearer (Azure). Conformance lanes drop auth-bypass; deterministic project-owned test signing key replaces it. |
| 11.5 | ◻ | Roll forward to queue. SQS Smithy `awsJson1_0`, Azure Service Bus admin OpenAPI, GCP Pub/Sub Discovery. Signature verification per frontend. |
| 11.6 | ◻ | Roll forward to pubsub. AWS awsQuery XML (verify Smithy 2.0 protocol support), GCP Pub/Sub Discovery, Azure Service Bus topics OpenAPI. |
| 11.7 | ◻ | Roll forward to rdbms. AWS awsQuery XML (RDS), GCP Cloud SQL Admin Discovery, Azure ARM OpenAPI. |
| 11.8 | ◻ | Roll forward to cache. AWS awsQuery XML (ElastiCache), GCP Memorystore REST, Azure ARM OpenAPI. |
| 11.9 | ◻ | Roll forward to functions. AWS restJson1 (Lambda), GCP Cloud Run Discovery, Azure Container Apps ARM OpenAPI. |
| 11.10 | ◻ | Roll forward to apigateway. AWS restJson1 (APIGW v2), GCP API Gateway Discovery, Azure APIM ARM OpenAPI. |
| 11.11 | ◻ | Storage retrofit. Apply signature verification to existing `services/storage/gen/` Smithy stubs. Drop auth-bypass knobs from storage conformance. |
| 11.12 | ◻ | Closer. All 8 services spec-driven; `make codegen` regenerates everything; BUG-18 closed; auth-bypass deleted across conformance. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

### Design notes

- **`translate.go` stays hand-written and auth-unaware.** Generated stubs call the verifier; the verifier rejects with the source cloud's own 401/403 envelope before dispatch. Per-operation translation logic doesn't change shape.
- **Adapter glue first; custom emitter only on demand.** If the `oapi-codegen` adapter grows past ~3 LOC per operation, switch to a custom OpenAPI emitter in `internal/codegen/`.
- **Deterministic project-owned test signing key.** Conformance generates real signed requests via a test key the shim trusts only when an explicit env var is set. Real-cloud lanes (Track A) use real signatures.
- **Stateless invariant carried.** Verification consumes the request signature once at the boundary; the shim doesn't cache claims, doesn't open sessions, doesn't propagate caller credentials to the backend.

### Exit criteria

- All 8 services have `services/<svc>/gen/{aws,gcp,azure}/` generated stubs; no hand-written wire layer remains.
- Every frontend rejects unsigned / wrong-key requests with the source cloud's own 401/403 envelope.
- `make codegen` regenerates every service from vendored specs in one command.
- Conformance lanes use real signing; no `skip_credentials_validation` / `WithoutAuthentication` / `fakeAzureCred` stubs.
- BUG-18 closed in [BUGS.md](BUGS.md).
- Per-service `INTERSECTION.md` + `APPLY_INTERSECTION.md` reconciled with any spec-driven fidelity discoveries.

### Open questions (decide during 11.0 review)

- `oapi-codegen` adapter glue vs custom OpenAPI emitter — Phase 11.2 forces the call.
- AWS awsQuery via Smithy 2.0 — confirm the existing custom emitter can route through that protocol path before scoping 11.6.
- Renovate coverage of vendored specs in `services/<svc>/spec/` — wire spec-freshness into CI as a tracked task during 11.0.
- GCP gRPC vs REST — REST first; gRPC future expansion, out of scope for Phase 11.

## Standing open questions (not phase-gated)

- Single org-wide deployment vs per-tenant — affects auth model.
- Where do live cloud test accounts live; who pays. Blocks the real-cloud SDK / CLI / Terraform lanes (Track A).
- Coding-agent permissions for upstream spec-version bumps: auto-PR or human-in-loop?
- AMQP fidelity tier for Azure Service Bus — REST-only initially, or AMQP from the start?

## Closed phases (PR index)

| PR | Phase | Headline |
|---|---|---|
| #17 | 10 | Cross-cloud `terraform apply` through the shim across all 8 services; 8 BUGs closed; full developer + contributing docs under `docs/`; codex doc + code review applied. Merged 2026-05-21 at `ebc30f7`. |
| #16 | 9 docs + 10.1 | Phase 9 docs roll-up + BUG-5 (stateless `Operations.Get` across 4 GCP frontends). Merged 2026-05-21 at `326f57d`. |
| #13 | 8 + 9 chunk | Phase 8 (API Gateway end-to-end) + Phase 9 (cross-cloud `terraform import`) substantial chunk. Merged 2026-05-20 at `ad85ddf`. |
| #12 | 7 | Functions control-plane shim — 3 frontends × 5 backends × 3 driver types. Merged 2026-05-19 at `9d02af0`. |
| #11 | 6 | Managed Redis control-plane shim. Merged 2026-05-19 at `cca8bc0`. |
| #10 | 5 | Managed RDBMS control-plane shim. Merged 2026-05-19 at `aeadbc8`. |
| #9 | 4 | Pubsub service end-to-end. Merged 2026-05-19 at `6305354`. |
| #8 | 3 | Queue service end-to-end. Merged 2026-05-19 at `07d11f5`. |
| #7 | 2 | Secrets service end-to-end. Merged 2026-05-19 at `7df43ec`. |
| #6 | 1 | Storage service end-to-end (full 3 × 4 matrix). Merged 2026-05-19 at `1f64d9f`. |
| #1, #2 | bootstrap | Repo + ruleset + continuity docs + Phase-0 CI checks. Merged 2026-05-18. |

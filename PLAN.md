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
| 12 | **Cross-cloud migration cell expansion.** Phase 9 + 10 proved the headline on one cell (storage AWS→GCS); Phase 12 takes one honest cross-cloud cell per service end-to-end across all 8. | Planned — opens after Phase 11 closes (or in parallel; doesn't share files). |

## Phase 11 — Tighten the wire boundary

> **Goal:** Replace hand-written wire layers with spec-driven generated stubs across every service, and wire real signature verification at the new decode boundary. The two changes are coupled at the same point in the request lifecycle; doing them per-service lands them together instead of retrofitting verification into hand-written handlers we plan to replace.

### Why now

Phase 10 closed cross-cloud `terraform apply` on every service. What remains uneven is the **boundary**:

- **Wire validation.** Only storage parses requests through generated stubs. The other 7 services hand-write the wire layer, so spec drift in any field name, length limit, or enum set is invisible until a real client sends a real request. The current Smithy emitter ignores scalar parse errors in query / header bindings and ignores XML decode errors — even storage's "spec-driven" decode is not actually enforcing the spec.
- **Signature verification.** Every frontend accepts requests without validating SigV4 / Bearer / SharedKey. Conformance papers over this with `skip_credentials_validation`, `option.WithoutAuthentication()`, and stub `fakeAzureCred` tokens — recorded as **BUG-18 (P1)**. Any "shim is safe in front of production traffic" claim is unfounded today.

### Premise corrections (from codex review of the initial plan)

The first draft of this plan made several wrong-library assumptions; the corrections shape the sub-phase ordering below.

- **AWS SigV4 is signer-only in `aws-sdk-go-v2/aws/signer/v4`.** Server-side verification has to reconstruct the canonical request, re-sign with the credential's secret, and constant-time compare. The canonical-request building blocks are reusable from `signer/v4`, but the verifier is ours. Body replay / `UNSIGNED-PAYLOAD` / presigned URLs / signed-header tampering / clock skew / temporary session tokens are all explicit hazards the verifier must handle.
- **`golang.org/x/oauth2` is token-acquisition plumbing, not a JWT verifier.** Google access tokens are not project-owned JWTs verifiable with a static key. ID tokens are validated with `google.golang.org/api/idtoken`, but they're a different credential from what Google SDK / CLI / Terraform actually send for Secret Manager / Pub/Sub / Cloud SQL (those send access tokens). The honest path for GCP is to accept signed bearer tokens whose claims (`iss`, `aud`, `exp`, signature against Google's published JWKS) are real — and document that "verification" against arbitrary Google access tokens has limits without a real identity-platform integration.
- **Azure Key Vault uses Bearer challenge auth, not SharedKey.** SharedKey is Storage; Service Bus is SAS / Entra ID. Phase 11's secrets sub-phase verifies the Bearer challenge; storage retrofit (11.11) covers SharedKey.
- **Current Smithy emitter is REST-XML-shaped.** Extending it to AWS Secrets Manager (`awsJson1_1`), SQS (`awsJson1_0`), SNS / RDS / ElastiCache (`awsQuery`), Lambda / APIGW v2 (`restJson1`) is **new protocol serde** at the emitter level, not a routing-table addition. Each AWS sub-phase scopes a protocol extension to the Smithy emitter.
- **`oapi-codegen` does not emit request-validation middleware by default.** The Azure pilot has to explicitly wire OpenAPI validation, Azure error-envelope mapping, the Bearer challenge response, and ARM long-running-operation behavior.
- **AGENTS.md canonical Go SDK row is `cloud.google.com/go/*` (gRPC).** Several current GCP conformance tests use `google.golang.org/api/*` (REST). Phase 11 reconciles this per service: either widen the AGENTS row to include REST, or land gRPC support per service (heavier).

### Codegen extension order

1. **AWS Smithy emitter — protocol extension to `awsJson1_1` (Secrets Manager).** The emitter exists; the work is adding a second protocol path alongside REST-XML, plus enforcing request validation honestly (reject malformed JSON, missing required fields, bad enum values with the source cloud's error envelope).
2. **OpenAPI v3 (Azure) via `oapi-codegen`** for Azure Key Vault, with explicit validation middleware + error-envelope mapping + Bearer challenge + ARM LRO handling.
3. **GCP routing layer** — reuse `google.golang.org/api/<svc>/v1` wire types; emit only routing + dispatch. AGENTS.md SDK-row reconciliation happens here.
4. **Smithy protocol extensions per AWS surface as we reach them** — `awsJson1_0` (SQS), `awsQuery` (SNS / RDS / ElastiCache), `restJson1` (Lambda / APIGW v2). Each is new emitter work, not addition.

### Sub-phases

| Sub | Status | Headline |
|---|---|---|
| 11.0 | ◐ | Plan baseline + codex review (this section + PR #18). |
| 11.1 | ◻ | Architecture spike: per-cloud verifier libraries (what library actually verifies, not signs) + GCP gRPC-vs-REST AGENTS.md reconciliation. Output: a doc that locks in the per-frontend verifier path before any code lands. BUG-15 walk + BUG-8 Track-A pin folded in. |
| 11.2 | ◻ | **Smithy emitter — `awsJson1_1` protocol path.** New protocol serde alongside the existing REST-XML path. Negative-conformance tests for malformed JSON, missing required fields, bad enum / timestamp / number values, wrong `X-Amz-Target` header → assert source-cloud's error envelope (not generic 500). Smithy field-level validation honored at decode (not silently swallowed as today). |
| 11.3 | ◻ | AWS Secrets Manager service migration to `services/secrets/gen/aws/`. Hand-written wire deleted. Conformance unchanged externally. |
| 11.4 | ◻ | **OpenAPI v3 (Azure) pilot for Key Vault.** `oapi-codegen` net/http server stubs + `kin-openapi` request-validation middleware + Azure error-envelope mapping + Bearer challenge issuance + ARM LRO polling. Migrate Key Vault frontend to `services/secrets/gen/azure/`. |
| 11.5 | ◻ | GCP Secret Manager — routing layer emitted from Discovery, reusing `google.golang.org/api/secretmanager/v1` wire types. Decide REST-vs-gRPC SDK conformance row per 11.1 output. |
| 11.6 | ◻ | **BUG-18 signature verification across the 3 secrets frontends.** AWS SigV4 verifier built on `signer/v4` canonical-request building blocks; Azure Bearer challenge + JWT signature validation; GCP bearer-token honest path (per 11.1 decision). Conformance lanes: real signing + valid-auth acceptance + tampered-signature rejection (wrong region/service, stale timestamp, mutated header, mutated body). Auth-bypass knobs dropped from secrets lanes. |
| 11.7 | ◻ | Roll forward to queue. Add `awsJson1_0` to Smithy emitter (SQS); OpenAPI for Azure Service Bus admin; GCP Pub/Sub Discovery. Sig verification carried forward. |
| 11.8 | ◻ | Roll forward to pubsub. Add `awsQuery` to Smithy emitter (SNS); GCP Pub/Sub Discovery; Azure Service Bus topics OpenAPI. |
| 11.9 | ◻ | Roll forward to rdbms. `awsQuery` extension already present (from 11.8); GCP Cloud SQL Admin Discovery; Azure ARM OpenAPI. |
| 11.10 | ◻ | Roll forward to cache. `awsQuery` (ElastiCache); GCP Memorystore REST; Azure ARM OpenAPI. |
| 11.11 | ◻ | Roll forward to functions. Add `restJson1` to Smithy emitter (Lambda); GCP Cloud Run Discovery; Azure Container Apps ARM OpenAPI. |
| 11.12 | ◻ | Roll forward to apigateway. `restJson1` (APIGW v2); GCP API Gateway Discovery; Azure APIM ARM OpenAPI. |
| 11.13 | ◻ | Storage retrofit. SharedKey verifier on the Azure Blob frontend; SigV4 verifier on the AWS S3 frontend; bearer-token verifier on the GCS frontend. Negative-conformance tests added retrospectively. Auth-bypass knobs dropped from storage lanes. |
| 11.14 | ◻ | Phase 11 closer. All 8 services spec-driven with honest field-level validation; `make codegen` regenerates everything; BUG-18 closed; auth-bypass deleted across conformance. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

### Design notes

- **`translate.go` stays hand-written and auth-unaware.** Generated stubs call the verifier; the verifier rejects with the source cloud's own 401/403 envelope before dispatch. Per-operation translation logic doesn't change shape.
- **`oapi-codegen` adapter glue is not a one-liner.** Generated stubs need explicit validation middleware, Azure error-envelope mapping, Bearer challenge handler, and ARM LRO behavior. Don't underestimate.
- **Test-mode signing keys are real keys, not bypass.** Conformance lanes generate real signed requests via a project-owned test key (deterministic IAM-like principal for AWS; well-formed JWT for Bearer paths). The shim trusts the key only when an explicit env var is set; real-cloud lanes (Track A) use real cloud identities.
- **Negative conformance is part of the contract.** Every wire-decode boundary gets tested with malformed-input, missing-required-field, bad-enum, tampered-signature, wrong-timestamp, wrong-region cases — and the assertion is the source cloud's own error vocabulary, not a generic 500.
- **Stateless invariant carried.** Verification consumes the request signature once at the boundary; the shim doesn't cache claims, doesn't open sessions, doesn't propagate caller credentials to the backend.

### Exit criteria

- All 8 services have `services/<svc>/gen/{aws,gcp,azure}/` generated stubs; no hand-written wire layer remains.
- Every frontend's decode boundary enforces the cloud's spec field constraints (required, enum, length, pattern). Negative conformance per cloud asserts the source cloud's error envelope.
- Every frontend rejects unsigned, wrong-key, and tampered-signature requests with the source cloud's own 401/403 envelope.
- Every frontend accepts valid signatures from the cloud's official SDK / CLI / Terraform — verified by removing the auth-bypass knobs.
- `make codegen` regenerates every service from vendored specs in one command.
- BUG-18 closed in [BUGS.md](BUGS.md).

### Open questions (resolve during 11.0–11.1)

- **GCP token verification honesty.** Google access tokens aren't simple JWTs to verify with a static key. The honest path may be: accept signed Bearer tokens whose JWKS signature + issuer + audience claims validate, and document the gap for opaque access tokens.
- **Smithy emitter protocol architecture.** Per-protocol templates side-by-side (REST-XML, awsJson1_1, awsJson1_0, awsQuery, restJson1) vs. one parameterized template with protocol-dispatch — pick during 11.2.
- **`oapi-codegen` request-validation choice.** `kin-openapi` is the de-facto middleware; verify it composes with `oapi-codegen`'s stdlib server stubs cleanly during 11.4.
- **GCP gRPC vs REST.** AGENTS.md canonical SDK row says gRPC; current tests use REST. 11.1 picks a per-service path and updates AGENTS.md.
- **Renovate coverage of vendored specs in `services/<svc>/spec/`.** Wire spec-freshness into CI as a tracked task during 11.1.

## Phase 12 — Cross-cloud migration cell expansion

> **Goal:** Phase 9 + 10 proved cross-cloud migration via Terraform on one cell (storage AWS→GCS). Phase 12 takes one honest cross-cloud cell per service end-to-end across all 8.

Doesn't depend on Phase 11 — can run in parallel or before. Each service-PR picks the cell with the smallest asymmetry surface (typically AWS → K8s peer, since the K8s peer's contract is the shim's domain-level intersection by construction), implements the missing translate-table entries, and lands `TestCrossCloudApply_Roundtrip_<svc>_<src>To<dst>` as the per-service exit criterion.

Cell selection per service is part of Phase 12.0 scoping; for now the candidates are:

| Service | Candidate cell | Why |
|---|---|---|
| storage | AWS→GCS (already proves) | Phase 10.7 baseline. |
| secrets | AWS→Vault | Vault KV is a clean superset of the AWS Secrets Manager intersection (no value-on-create asymmetry). |
| queue | AWS→NATS JetStream | NATS receipt-handle = reply subject; no SQS attribute round-trip mismatch on a K8s peer. |
| pubsub | AWS→NATS JetStream | Same reasoning. |
| rdbms | AWS→cnpg | cnpg's Cluster CR doesn't have AWS RDS's parameter-group / subnet-group reconcile semantics — the asymmetry is documented; the cell is honest. |
| cache | AWS→Redis Operator | Same shape. |
| functions | AWS→Knative | Phase 7's invoke-connectivity test demonstrates the path; Apply-side just needs the matching drift-assert wiring. |
| apigateway | AWS→Envoy Gateway | Phase 8's exit criterion already runs end-to-end; Apply-side adds the cross-cloud roundtrip assertion. |

Sub-phase structure (drafted; refined in 12.0):

| Sub | Headline |
|---|---|
| 12.0 | Scope baseline + per-service cell selection. |
| 12.1–12.8 | One PR per service, landing the chosen cross-cloud Apply cell as a roundtrip test. |
| 12.9 | Closer: cross-cloud Apply matrix has one honest cell per service; per-service `MIGRATION.md` updated with the runnable recipe. |

**Exit criteria:** every service has `TestCrossCloudApply_Roundtrip_<svc>_<cell>` green in CI; `shimctl env` covers the chosen cell; per-service `MIGRATION.md` includes a copy-pasteable Terraform + endpoint-override walkthrough.

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

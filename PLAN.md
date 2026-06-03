# shimanism — Roadmap

> **Goal:** Protocol-translation shims that let unmodified cloud SDKs / CLIs / Terraform providers run against AWS, GCP, Azure, or Kubernetes-native backends by pointing them at a shim endpoint instead of the original service.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Guiding principles

1. **Spec is the contract.** Each shimmed service has a canonical published spec (AWS Smithy, GCP Discovery / protobuf, Azure OpenAPI / ARM). Server-side wire layer is generated from that spec; hand-written code is translation logic only.
2. **Fidelity to the source API.** The shim speaks the cloud's API exactly. Error shapes, response headers, status codes, async semantics — all match. Out-of-intersection calls fail loud in the source cloud's own error vocabulary. Never fabricate success.
3. **Real backends, not emulators.** Translated calls drive a real, comparable service. The shim itself holds no state of record.
4. **Conformance from day one.** Every shimmed operation is exercised in the same commit by the cloud's SDK, CLI, and Terraform provider, against every backend in scope.
5. **Intersection only.** Shim features common to AWS / GCP / Azure / the chosen K8s peer.
6. **Kubernetes is a first-class backend.** Every service has a K8s peer.
7. **No fakes, no fallbacks, no degraded modes.**
8. **One source spec, multiple adapters.** Codegen regenerates from upstream; agents own translation tables.
9. **Single-branch rule.** One branch per phase. Many commits, one PR. User merges.
10. **Continuity always.** STATUS / DO_NEXT / WHAT_WE_DID / BUGS update at every significant chunk.
11. **One service per phase, every frontend × every backend.** 3 × 4 matrix, 3 driver types per cell.

## Locked-in decisions

| # | Decision | Value |
|---|----------|-------|
| 1 | Implementation language | **Go** |
| 2 | Spec sources | Pull upstream, never fork: AWS Smithy from `aws/aws-sdk-go-v2`; GCP Discovery JSON live + protobuf from `googleapis/googleapis`; Azure OpenAPI v2/v3 from `Azure/azure-rest-api-specs`. |
| 3 | Codegen | Three lanes: `cmd/codegen` (AWS Smithy), `cmd/azure-codegen` (Azure OpenAPI v2 via 8-stage preprocessor + `kin-openapi`/`oapi-codegen`), `cmd/gcp-codegen` (Discovery routing-only). Hand-written code restricted to per-operation `translate.go` files + per-frontend adapters. See [docs/codegen-pipelines.md](docs/codegen-pipelines.md). |
| 4 | Backend abstraction | Per-service `Backend` interface in domain terms. No premature cross-service generalization. |
| 5 | Test fidelity rings | Per-PR: recorded + unit. Nightly: live cloud accounts (Track A). |
| 6 | Deployment | Single Go binary + Helm chart. SaaS deferred. |
| 7 | Repo layout | Monorepo. `services/<svc>/` per shim; shared `internal/codegen/`, `internal/harness/`, `internal/{sigv4verifier,gcpbearer,azurebearer,azuresharedkey}/`. |
| 8 | License | AGPL-3.0. |
| 9 | Passthrough mode | Per-service, when there's a real reason (auth interception, observability injection). |
| 10 | Agent permissions for spec updates | Human-in-loop on upstream-spec change. |
| 11 | **Reuse-over-reinvention** | Lean on cloud's official spec + Go SDK whenever they fit. Each frontend's wire layer is generated from the cloud's canonical spec; auth verification uses the cloud's own building blocks. See [AGENTS.md § Reuse over reinvention](AGENTS.md#reuse-over-reinvention). |
| 12 | **Stateless shim** | No sidecar database, no shim-managed key/value namespace, no in-process cache treated as authoritative. Cross-cloud mappings derive at request time. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless). |
| 13 | **In-tree K8s peer when OSS doesn't fit** | Third-party OSS first; otherwise [`peers/shimakit/`](peers/shimakit/) → concrete `shima<service>` peers. Each is its own Go module. |
| 14 | **Vendored-spec provenance** | Every spec under `services/*/spec/` + `services/common-types/` carries a `_provenance` top-level key derived from SOURCES.md. `cmd/inject-provenance` + CI guards enforce. See [docs/codegen-pipelines.md § Vendored-spec provenance](docs/codegen-pipelines.md#vendored-spec-provenance). |
| 15 | **Signature verification at decode boundary** | Per-cloud verifiers under `internal/{sigv4verifier,gcpbearer,azurebearer,azuresharedkey}/`. Test mode uses HS256 with a project-owned key; production uses real-cloud JWKS (Phase 13.C). See [docs/verifiers.md](docs/verifiers.md). |

## Service phases 1–8 (all closed)

One service per phase; full 3 frontends × 4 backends × 3 driver-types matrix.

| # | Service | Frontends | Backends |
|---|---|---|---|
| 1 | Object storage | AWS S3 · GCS · Azure Blob | + MinIO |
| 2 | Secrets | AWS Secrets Manager · GCP Secret Manager · Azure Key Vault | + Vault |
| 3 | Queue | AWS SQS · GCP Pub/Sub (pull) · Azure Service Bus queues | + NATS JetStream |
| 4 | Pub/Sub | AWS SNS · GCP Pub/Sub · Azure Service Bus topics | + NATS core |
| 5 | Managed RDBMS (control plane) | AWS RDS · Cloud SQL Admin · Azure DB Admin | + CloudNativePG |
| 6 | Managed Redis (control plane) | AWS ElastiCache · GCP Memorystore Admin · Azure Cache Admin | + Redis Operator |
| 7 | Functions | AWS Lambda · GCP Cloud Run · Azure Container Apps | + Knative |
| 8 | API Gateway | AWS API Gateway v2 · GCP API Gateway · Azure API Management | + Envoy Gateway |

Per-service detail in `services/<svc>/` (`INTERSECTION.md`, `APPLY_INTERSECTION.md`, `MIGRATION.md`). Per-phase narrative in [WHAT_WE_DID.md](WHAT_WE_DID.md).

## Cross-cutting phases 9–12 (all closed)

| # | Headline | Status |
|---|---|---|
| 9 | Cross-cloud `terraform import` honest end-to-end across all 8 services. | ✅ PR #13 + #16 |
| 10 | Cross-cloud `terraform apply` honest end-to-end across all 8 services. | ✅ PR #17 |
| 11 | **Tighten the wire boundary.** Spec-driven codegen + BUG-18 signature verification at every decode boundary. 24/24 frontends verifier-wrapped. All 8 AWS frontends spec-driven (Smithy → REST-XML / awsJson1_0 / awsJson1_1 / awsQuery / restJson1). Azure oapi-codegen pilot (KeyVault). Three deferrals → Phase 12. | ✅ PR #18 |
| 12 | **Spec-driven toolchain landing.** 8/8 Azure specs codegen + 8/8 GCP route inventories with `Match()`/`MatchAll()`. 8-stage Azure preprocessor (incl. `flattenARMAllOf` closing BUG-20). Vendored-spec `_provenance` + spec-freshness lane. Per-service Terraform walkthroughs. `azure_keyvault` is the reference adapter; the other 7 Azure + 8 GCP frontends keep hand-written dispatch on top of the gen inventory (drift contract). | ✅ PR #19 |

Verifier architecture, GCP REST/gRPC reconciliation, and per-cloud auth design notes are in [docs/verifiers.md](docs/verifiers.md). Codegen pipeline architecture is in [docs/codegen-pipelines.md](docs/codegen-pipelines.md).

## Phase 13 — Full adapter migration + production auth + real-cloud Track A

> **Status: closed in PR #20.** Phase 13 absorbed the deferred adapter/auth work from Phases 11 + 12; real-cloud Track A residuals moved into Phase 14.D.

Phase 12 lands the spec-driven *toolchain*. Phase 13 turns the remaining hand-written dispatch layers over to it, wires production auth, and closes the two Track-A bugs against real cloud.

> **Closed shape:** 6 full handler migrations + 9 spec-drift blank-import contracts landed; 13.C production RS256 JWKS landed; 13.D.1 sockerless lane landed. Real-cloud Track A residuals moved to Phase 14.

### Scope summary

| Track | What | Source | Status |
|---|---|---|---|
| 13.A | Azure adapter migration — every Azure frontend dispatches through `gen.ServerInterface` + `gen.HandlerWithOptions`. | Phase 11.4 + 11.7b + 12.A.14 | 5/7 full + 2/7 blank-import |
| 13.B | GCP adapter migration — every GCP frontend dispatches via `gen.gcp.Match()` / `MatchAll()`. | Phase 11.5 + 11.7b | 1/8 full + 7/8 blank-import |
| 13.C | Production RS256 JWKS — wire real Google + Microsoft Entra JWKS. | Phase 11 follow-on + Phase 12.C | ✅ landed |
| 13.D | Sockerless storage lane (13.D.1) + real-cloud Track A residual (13.D.2). | BUGS.md, [docs/sockerless-validation.md](docs/sockerless-validation.md) | 13.D.1 ✅; residual moved to Phase 14.D |
| 13.E | Cross-cloud Apply matrix expansion — additional source/destination cells per service beyond the AWS→K8s-peer baseline already in CI. | Phase 12.1–12.8 | moved to Phase 14.E |

### 13.A — Azure adapter migration

**Status (PR #20):**

| # | Frontend | Spec ops | Status | Notes |
|---|---|---|---|---|
| 13.A.1 | `azure_redis` | 41 | ✅ full migration | `gen.HandlerWithOptions` mux; 6 real handlers + 35 `notImplemented`. |
| 13.A.2 | `azure_containerapps` | 11 | ✅ full migration | Same pattern; Properties anonymous-struct populated via JSON round-trip. |
| 13.A.3 | `azure_dbadmin` (PostgreSQL FlexibleServer) | 66 | ✅ full migration | Largest ARM gen; 10 real + 56 stubs. |
| 13.A.4 | `azure_servicebus` (queue) | 13 | ✅ full migration | **Hybrid dispatch** — gen mux can't be used (Go 1.22 ServeMux refuses upstream spec's overlapping `/{entityName}` vs `/{topicName}/subscriptions` patterns). Hand-written regex routes admin URLs into gen.ServerInterface methods; data-plane `/messages/...` stays hand-written. |
| 13.A.5 | `azure_servicebus_topics` (pubsub) | 13 | ✅ full migration | Same hybrid pattern; shared Service Bus spec with queue. |
| 13.A.6 | `azure_blob` | 69 | ◐ spec-drift blank-import | gen mux can't be used (spec uses `?comp=...` query discriminators net/http ServeMux doesn't dispatch on). Full migration needs the Service-Bus hybrid pattern + 58 method stubs; deferred. |
| 13.A.7 | `azure_apim` | 0 | ◐ spec-drift blank-import | Vendored APIM spec is intentionally minimal; gen.ServerInterface is empty. Migration moot until spec broadens. |

### 13.A — Azure adapter migration

**Reference impl:** `internal/secrets/frontends/azure_keyvault/server.go` (Phase 12.A.1/2). Pattern:

1. `Server` implements `gen.ServerInterface` — one method per spec operation.
2. `srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})`; `ServeHTTP` delegates to mux with a small pre-dispatch normalization pass for SDK idioms not in the spec (trailing-slash, empty-version).
3. Out-of-intersection operations return `notImplemented(w, "OpName")` — the Azure error envelope, not a stub.
4. In-intersection operations call the domain backend and map response shapes via the spec's wire types.

**Frontends + spec-method counts:**

| Order | Frontend | Spec methods | Hand-written LOC | Notes |
|---|---|---|---|---|
| 1 | `internal/cache/frontends/azure_redis` | 41 (ARM Redis*) | 272 | Smallest hand-written; many out-of-intersection stubs (AccessPolicy* / PrivateLink*). |
| 2 | `internal/functions/frontends/azure_containerapps` | 11 | 310 | Smallest gen interface; ContainerApp struct works via `flattenARMAllOf` (BUG-20 fix). |
| 3 | `internal/pubsub/frontends/azure_servicebus_topics` | 13 | 343 | Shared Service Bus spec with queue. |
| 4 | `internal/apigateway/frontends/azure_apim` | 0 (minimal spec) | 342 | APIM minimal spec has no operations; gen file is types-only; this migration is "wire gen types into responses" not "switch to HandlerWithOptions". |
| 5 | `internal/queue/frontends/azure_servicebus` | 13 | 324 | Shared with pubsub. |
| 6 | `internal/rdbms/frontends/azure_dbadmin` | 66 (FlexibleServer) | 413 | PostgreSQL — biggest spec; Server struct already proper via BUG-20 fix. |
| 7 | `internal/storage/frontends/azure_blob` | 69 (Blob data-plane) | 620 | Biggest hand-written; data-plane shape; the 1.1 MB gen file. |

**Validation per migration:** existing conformance suite (SDK + CLI + Terraform) must stay green. Add a `TestAzureGen_<Svc>_HandlerDispatch` test that posts a sample request through the gen mux to confirm the dispatch path is wired.

**Pattern recap (from `azure_keyvault` Phase 12.A.1/2 + 13.A.1-3):** `Server` implements `gen.ServerInterface`; `srv.mux = gen.HandlerWithOptions(srv, ...)`; `ServeHTTP` delegates. Out-of-intersection ops return `notImplemented` ARM envelope. Per-migration `TestAzureGen_<Svc>_HandlerDispatch` posts a canonical body through the gen mux to verify dispatch.

### 13.B — GCP adapter migration

**Pattern:** retire frontend-local regex tables in favour of `gen.gcp.Match()` / `MatchAll()`. The disambiguation layer (e.g. distinguishing `projects.secrets.get` from `projects.locations.secrets.get` on the overloaded `v1/{+name}` template) stays in the frontend — the gen inventory is the spec-drift contract, dispatch goes through it.

**Status (PR #20):** 13.B.1 `gcp_secretmanager` is the full migration (regex tables retired; ServeHTTP dispatches by path-shape inspection; `:destroy` no-op success documented). The other 7 frontends carry the spec-drift contract via blank import of `services/<svc>/gen/gcp` — existing regex dispatch passes the per-service `TestGCPRoutes_<Svc>_FrontendDispatchCoverage` tests so the refactor would be cosmetic.

**Frontends + route counts:**

| Order | Frontend | gen.Routes | Notes |
|---|---|---|---|
| 1 | `internal/secrets/frontends/gcp_secretmanager` | 32 | Smallest. Overloaded `v1/{+name}` (MatchAll needed for disambiguation). |
| 2 | `internal/apigateway/frontends/gcp_apigateway` | 30 | Clean. |
| 3 | `internal/cache/frontends/gcp_memorystore` | 45 | Clean. |
| 4 | `internal/pubsub/frontends/gcp_pubsub` | 46 | Shared with queue. |
| 5 | `internal/queue/frontends/gcp_pubsub` | 46 | Shared. |
| 6 | `internal/functions/frontends/gcp_cloudrun` | 58 | `/v2/` prefix. |
| 7 | `internal/rdbms/frontends/gcp_cloudsql` | 74 | Accepts both `/v1/` (Discovery-canonical) and `/sql/v1beta4/` (legacy SDK shape) — keep the dual-prefix tolerance in the dispatcher. |
| 8 | `internal/storage/frontends/gcs` | 82 | XML-API fallback path (`/<bucket>/<object>`) is NOT in `gen.gcp.Routes` and stays as a sibling regex. |

**Validation per migration:** existing per-frontend conformance + the `TestGCPRoutes_<Svc>_FrontendDispatchCoverage` tests added in Phase 12.B.8/9.

### 13.C — Production RS256 JWKS

Wire the real Microsoft Entra + Google JWKS paths. Touches `internal/azurebearer/` + `internal/gcpbearer/`. Test-mode HS256 stays the default; deployment-time config selects which path is active. See [docs/verifiers.md § Production deployment path](docs/verifiers.md#production-deployment-path-phase-13c--landed).

**Validation:** add `TestAzureBearer_RealJWKS_*` / `TestGCPBearer_RealJWKS_*` that mock the JWKS endpoint (the real production paths can't be exercised without a real Entra tenant / Google project — those are Track A).

### 13.D — Real-cloud Track A

Two slices.

**13.D.1 — Sockerless validation lane (landed).** Opt-in `make sockerless-storage` target builds the AWS + GCP simulator binaries from a local clone of `github.com/e6qu/sockerless`, starts them under TLS (AWS) / HTTP (GCP) on test-only ports, and runs `TestSockerless_*` in `services/storage/conformance/sockerless_test.go` + `services/secrets/conformance/sockerless_test.go`. AWS S3 bucket lifecycle + GCS full round-trip + AWS Secrets Manager CreateSecret/ListSecrets/DeleteSecret pass. Three fidelity gaps filed upstream:

- [e6qu/sockerless#173](https://github.com/e6qu/sockerless/issues/173) — AWS S3 routes under `/s3/` URL prefix. Working around with the suffix in our endpoint.
- [e6qu/sockerless#174](https://github.com/e6qu/sockerless/issues/174) — AWS S3 sim persists the SDK's `aws-chunked` envelope verbatim. Blocks PutObject/GetObject in the storage lane.
- [e6qu/sockerless#175](https://github.com/e6qu/sockerless/issues/175) — AWS Secrets Manager sim is missing `ListSecretVersionIds`. Blocks HeadSecret + GetSecretValue (both call into the version-ID mapping path) in the secrets lane.

Azure Blob isn't simulated by sockerless (only Azure Files); 13.D.1 covers AWS S3 + GCS + AWS Secrets Manager. See [docs/sockerless-validation.md](docs/sockerless-validation.md).

**13.D.2 — Real-cloud Track A (pending).** Live AWS / GCP / Azure accounts. Closes:

- **BUG-8** (P3, apigateway/gcp-tf): `hashicorp/google` API Gateway endpoint-override + real OAuth signing. Currently smoke-skipped in `services/apigateway/conformance/gcp_terraform_test.go`. The sockerless GCP APIGW backend lane passes; this is now the Terraform-provider leg.
- **BUG-15** (P3, queue/gcp): Pub/Sub `subscriptions.get` retention drift. Provider records `345600s` instead of `604800s`. The sockerless GCP queue backend retention PATCH/read lane passes; real-cloud Track A decides whether the remaining drift is a hashicorp/google provider bug or a shim frontend response gap.

Also lands real-signed signature-verification conformance against real IAM / Workload Identity / Entra ID.

### 13.E — Cross-cloud Apply matrix expansion (deferred to Phase 14)

Phase 12 ships `TestCrossCloudApply_Roundtrip_<svc>_<cell>` for one cell per service (typically AWS → K8s peer). Expanding to additional source/destination cells per service is mechanical and best paired with the sockerless lane — every new cell wants a backing simulator the cross-cloud Apply test can target without real-cloud cost. Folded into Phase 14.

### Exit criteria

- Every Azure frontend dispatches through `gen.ServerInterface` + `gen.HandlerWithOptions`, OR carries the blank-import spec-drift contract (full migration deferred to Phase 14 for frontends with ServeMux pattern conflicts: `azure_blob`, `azure_apim`).
- Every GCP frontend dispatches via `gen.gcp.Match()` / `MatchAll()`, OR carries the blank-import spec-drift contract (full migration deferred to Phase 14 for the 7 frontends whose existing regex dispatch is already pinned by `TestGCPRoutes_<Svc>_FrontendDispatchCoverage` — see 13.B.2-8).
- Production JWKS path documented + exercised against a mocked JWKS endpoint; **real-cloud Track A lane deferred to Phase 14.D**.
- BUG-8 and BUG-15 remain open; both absorbed into Phase 14.D.
- Sockerless validation lane landed for storage + secrets (13.D.1, ✅).

## Phase 14 — Sockerless-verified validation lane + deferred follow-ons

> **Premise.** The sockerless simulator project ([`github.com/e6qu/sockerless`](https://github.com/e6qu/sockerless)) provides AWS / GCP / Azure simulators that speak the same wire protocols as real cloud APIs. That makes them the right vehicle for **cross-cloud shim verification** and **Terraform-provider round-trip testing** at CI tempo: every shim translation layer can be exercised end-to-end against a deterministic sim instead of against (or in addition to) a real cloud account.
>
> Phase 14 cashes in the items Phase 13 deferred, on the cadence of the upstream sockerless project closing the six issues we filed in 13.D.1. Each shim-side follow-on has an explicit upstream dependency.
>
> **Status: nearly closed (33+1 sockerless lanes on `main` after PR #44).** 14.A landed; 14.D simulator audit landed through PR #219 + #221 + #229 + #231 + #235 + #238. 14.B is materially complete: storage matrix 3×3 (single-shot + multipart + copy), SB admin + raw-AMQP/TLS, ARM Azure (Redis/PG/APIM). Remaining 14.B/C/E shim-side work folds into **3 PRs** documented in [DO_NEXT.md § The 3-PR closure plan](DO_NEXT.md#the-3-pr-closure-plan); 14.D Track A stays blocked on real cloud credentials.

### Dependency on the sockerless project

**Round 1 — closed by sockerless PR #179 (2026-05-23).** All 6 issues we filed at the end of Phase 13.D.1 landed in a single sockerless umbrella PR. Phase 14.A re-enabled the corresponding shim assertions; new services (Pub/Sub, Memorystore, APIM, Service Bus, …) now exist in sockerless.

| Sockerless issue (round 1) | Phase 14 work it unblocked | Status |
|---|---|---|
| [#173](https://github.com/e6qu/sockerless/issues/173) — S3 `/s3/` URL prefix | 14.A.1 — drop the `/s3` workaround | ✅ 14.A landed |
| [#174](https://github.com/e6qu/sockerless/issues/174) — `aws-chunked` envelope stored verbatim | 14.A.2 — AWS S3 round-trip assertions | ✅ 14.A landed |
| [#175](https://github.com/e6qu/sockerless/issues/175) — missing `ListSecretVersionIds` | 14.A.3 — AWS Secrets Manager `HeadSecret` + `GetSecretValue` | ✅ 14.A landed |
| [#176](https://github.com/e6qu/sockerless/issues/176) — AWS missing services | 14.B.1 — new AWS service lanes (SQS, SNS, RDS, ElastiCache, APIGW v1+v2) | ◐ current lane includes AWS SQS; additional AWS service lanes remain optional follow-on. |
| [#177](https://github.com/e6qu/sockerless/issues/177) — GCP missing services | 14.B.2 — new GCP service lanes (Pub/Sub, Secrets, SQL, Memorystore, APIGW). | ◐ current lane includes GCP Pub/Sub queue/pubsub, Secret Manager, and APIGW; BUG-8 backend leg and BUG-15 backend leg cleared. |
| [#178](https://github.com/e6qu/sockerless/issues/178) — Azure missing services | 14.B.3 — new Azure service lanes (Blob+KV data plane, Service Bus, PG, Redis, APIM) | ◐ current lane includes Azure Blob + KV; additional Azure service lanes remain optional follow-on. |

**Round 2 — fidelity audit (Phase 14.D), 2026-05-24.** Probing sockerless directly with `curl` and the SDK-style request shapes the shim's backends emit surfaced 8 additional fidelity gaps in the now-shipped services. Filed and closed in sockerless PR #180.

| Sockerless issue (round 2) | Blocks (in shim's lane) |
|---|---|
| [#181](https://github.com/e6qu/sockerless/issues/181) — Azure Cache for Redis ARM case sensitivity | Azure Redis cache lane (shim emits lowercase per spec, sim only matches `Redis`). |
| [#182](https://github.com/e6qu/sockerless/issues/182) — GCP Pub/Sub strips 5 of 7 subscription fields | Pub/Sub field round-trip — **this is the same shape as BUG-15**. Closing this likely closes BUG-15 against sockerless without real-cloud Track A. |
| [#183](https://github.com/e6qu/sockerless/issues/183) — GCP Secret Manager ListSecrets routing leak (also affects `/v1/operations`) | GCP Secret Manager list + Operations LRO polling. |
| [#184](https://github.com/e6qu/sockerless/issues/184) — Azure Key Vault malformed `kid` / `id` URLs (duplicated host + HTTP) | Any KV client that follows the returned id; TF provider state recording the bad URL. |
| [#185](https://github.com/e6qu/sockerless/issues/185) — Azure Key Vault placeholder modulus | JWKS / signature-verify integration tests against KV keys. |
| [#186](https://github.com/e6qu/sockerless/issues/186) — AWS SQS attribute drops | AWS SQS attribute round-trip — same drift shape as BUG-15 in different protocol. |
| [#187](https://github.com/e6qu/sockerless/issues/187) — GCP Cloud SQL relative `selfLink` | TF state with unfollowable selfLink. |
| [#188](https://github.com/e6qu/sockerless/issues/188) — GCP Secret Manager `versions/latest` not resolved | Version-tracking flows. |

**Later audit rounds — closed by sockerless PRs #192, #200, #202, #211, #216, and #219.** Follow-up probes filed #189-191, #193-199, #201, #203-210, #213-215, and #218. PR #216 closed the final audit set (#209, #210, #213, #214, #215); PR #219 closed the GCP Secret Manager lifecycle gap (#218). As of 2026-05-25, the current shim lane passes against sockerless `06ee3a5`.

**Latest lane added.** The GCP Secret Manager backend lane found [sockerless#218](https://github.com/e6qu/sockerless/issues/218): the simulator lacked `ListSecretVersions`, `UpdateSecret`, and `DeleteSecret`. After #218 closed in PR #219, the shim added the real `services/secrets/backends/gcp` sockerless lane without a workaround.

### Why sockerless for cross-cloud + Terraform-provider validation

- **Cross-cloud shim verification.** The shim's job is *translate AWS-shaped call → GCP backend* (or any other source × dest pair). Verifying this end-to-end needs a target the destination cloud's SDK actually talks to. Sockerless gives us a deterministic, in-process target for each of the three destination clouds — no real-cloud cost, no flake from external dependencies, no per-PR billing.
- **Terraform-provider round-trips.** The matrix Phase 12 established (`TestCrossCloudApply_Roundtrip_<svc>_<cell>`) drives the cloud's Terraform provider against the shim, which then forwards to the destination backend. The Terraform provider doesn't know it's not talking to the real cloud; the destination doesn't know its caller is a TF apply. Sockerless backends close the loop: TF apply → shim frontend → shim backend → sockerless simulator → response back through the chain → `terraform plan -refresh-only -detailed-exitcode = 0`. Today's lane runs against in-memory backends; expanding to sockerless backends per cell lifts confidence that the wire-level translation is faithful to what real clouds actually serve.

### Sub-phases

| Track | What | Dependency | Status |
|---|---|---|---|
| 14.A | Re-enable shim assertions as sockerless fidelity bugs close. Three sub-items, one per sockerless#173/174/175. | sockerless#173-175 closing | ✅ landed (sockerless PR #179) |
| 14.B | Add new sockerless service lanes as sockerless missing-service + fidelity issues close. | All audit chains closed through sockerless PR #238. | ◐ — 33 lanes green covering storage (single-shot + multipart + copy across 3 clouds), secrets, queue admin + SB Send/Receive, pubsub, rdbms, cache (Azure Redis), apigateway. Residual: BUG-35 Container Apps pre-pull (planned PR 1). |
| 14.C | Full handler migrations for the 9 frontends that landed only as blank-import in Phase 13: `azure_blob` (69 ops; Service-Bus hybrid pattern), `azure_apim` (waits for upstream spec to broaden), 7 GCP frontends (cosmetic). | — (independent of sockerless) | ◐ — bundled with PR 1 (7 GCP frontends together) + PR 2 (`azure_blob`). `azure_apim` waits indefinitely on upstream spec broadening. |
| 14.D | Fidelity audit against sockerless's new services + file per-bug issues. Real-cloud Track A residual for whatever sockerless can't cover (e.g. real signed-credentials conformance and the remaining Terraform-provider legs of BUG-8/BUG-15). | — | ✅ simulator audit done through sockerless PR #238; real-cloud residual blocked on infra. |
| 14.E | Cross-cloud Apply matrix expansion — Terraform-driven `TestSockerless_E2E_*_Through_Shim_ApplyTF_*` cells across source/destination pairs per service family. | 14.B substantially complete | ✅ — 10 PRs (#58–#67). Storage cross-cloud Apply matrix closed across every source/backend permutation the shim covers (Azure / AWS / GCP source × inmem / AWS / GCP / Azure backend). Secrets: Azure-source row closed; AWS / GCS-source rows deferred to Phase 15 (mechanically identical to PR #67's storage batch). SB cross-cloud: blocked on missing shim-side AMQP listener (`internal/queue/frontends/azure_servicebus` is REST/ATOM-only by design). Surfaced 6 upstream sockerless gaps, all landed: #257 / #260 / #261 / #269 / #272 / #276. |

### The 3-PR closure plan

Remaining 14.B/C/E shim-side work is bundled into 3 PRs (full task lists in [DO_NEXT.md](DO_NEXT.md)):

- **PR 1** — BUG-35 Container Apps pre-pull + GCP Cloud Run lane + 3 BUG-24 reverse-direction cells + all 7 GCP frontend 14.C migrations + 14.E Azure-source/destination cross-cloud Apply cells. ~1500-2500 LOC.
- **PR 2** — `azure_blob` full handler migration (69 ops, Service-Bus-style hybrid dispatch). ~1500-2000 LOC concentrated in one file.
- **PR 3 (blocked on infra)** — 14.D Track A: BUG-8 + BUG-15 + real-signed verifier conformance.

### Exit criteria

- Every issue in sockerless#173-178 has been tracked to closure or to a documented re-deferral (with rationale recorded in [docs/sockerless-validation.md](docs/sockerless-validation.md)).
- Every Phase-13-deferred handler migration (13.A.6, 13.A.7, 13.B.2-8) has either fully migrated or been documented as a permanent blank-import contract (with rationale).
- BUG-8 and BUG-15 are closed or have a documented absorbed-into-future-phase status.
- The sockerless validation lane covers ≥ 1 cell per shimmed service (storage, secrets, functions, queue, pubsub, rdbms, cache, apigateway) for at least one of the three clouds.

## Phase 15 — Cross-cloud normalization standard + new-service expansion

> **Premise.** Phase 14.E closed the cross-cloud Apply matrix for storage and secrets and surfaced the first formal *normalization rule* (the empty-placeholder convention for value-less secret creates, PR #69). That rule generalises: every (source-cloud × destination-cloud) cell where semantics don't map 1:1 needs a published, stateless, deterministic translation. Phase 15 codifies the existing implicit normalisations into a contract document, closes out 14.E residuals, and adds two new shimmed services (NoSQL key-value + DNS) using that contract from the start.
>
> **Status: just opened (2026-05-30).**

### Sub-phases

| Track | What | Dependency | Status |
|---|---|---|---|
| 15.A | **Normalization standard.** Audit shimanism's existing services for implicit cross-cloud translations (version-ID shape, tags-vs-labels, region naming, soft-delete grace period, resource-ID format, secret value-less create, queue topic-vs-subscription, etc). Document each as a formal rule in a new `docs/normalizations.md`. Each rule names the asymmetry, the rule, the trade-off, and the test that exercises it. Sets the contract pattern for 15.C / 15.D. | — | ✅ closed |
| 15.B | **14.E residual cleanups.** Investigate the terraform-aws v5 `has_secret_string_wo` write-only-attribute drift on `TestCrossCloudApply_Roundtrip_SecretsAWStoAzure` (currently skipped). File at `hashicorp/aws` if it's a provider bug; otherwise document the limitation. Scope the SB cross-cloud cells (blocked on missing shim-side AMQP listener) — decide whether to file at sockerless, build AMQP listener in the shim, or formally close the SB cross-cloud row as out-of-intersection. | — | ✅ closed |
| 15.C | **NoSQL key-value service.** New shimmed service covering DynamoDB (AWS) + Firestore Native (GCP) + Cosmos DB Table API (Azure) + K8s peer (etcd-based). Key-value subset: Get / Put / Delete / Scan on a partition key. Includes spec ingest, gen, frontends, backends, conformance matrix per the existing service pattern. Per-PR rather than monolithic. | 15.A normalisations established | ✅ closed (PRs #90–#100) |
| 15.D | **DNS service.** New shimmed service covering Route 53 (AWS) + Cloud DNS (GCP) + Azure DNS (Azure) + K8s peer (CoreDNS). Public **and** private zones; standard record types (A / AAAA / CNAME / MX / TXT / NS). Per-PR. | 15.A normalisations established | ✅ closed (PR #89) |

### The ordering decision

15.A ships first: it's cheap, high-value, and sets the precedent for the new-service phases. 15.B can be interleaved (some cleanups are doc-only, others need investigation). 15.C and 15.D follow: NoSQL is bigger so DNS may ship sooner if scope risk surfaces.

### Exit criteria

- `docs/normalizations.md` enumerates every cross-cloud translation rule the shim implements, with one published rule per asymmetry.
- 14.E residual cells either work, have a filed upstream issue, or are formally closed as out-of-intersection in `services/*/APPLY_INTERSECTION.md`.
- NoSQL key-value service has the standard 3 frontends + 4 backends, the conformance matrix (SDK / CLI / Terraform per frontend per backend), and at least one cross-cloud Apply cell.
- DNS service has the standard frontends + backends + conformance matrix; public zone cell + private zone cell at minimum.

### Standing rule on new services

Per intersection-only scope: every new service must work across **AWS + GCP + Azure + K8s peer**. If a feature only exists in one cloud, it's out-of-intersection by definition and rejected with the source cloud's "not supported" error. This includes the standard pre-flight that opened Phase 14: identify the intersection upfront, publish the per-cell normalization rules, then write the code.

## Phase 16 — Compute and Networking

> **Premise.** Phase 15 closed NoSQL + DNS. Phase 16 adds the compute + networking surface: VPC networking primitives (networks, subnets, security groups, public IPs), compute instance lifecycle (run/start/stop/terminate/describe + machine types), and basic layer-4 TCP load balancers. Two new service directories: `services/compute/` (AWS EC2 + GCP Compute Engine + Azure Compute/Network + K8s Nodes/Namespace/NetworkPolicy) and `services/loadbalancer/` (AWS ELBv2 + GCP Compute LB + Azure LB + K8s Service). AWS VPC and EC2 instances share `ec2.amazonaws.com`; GCP networks and GCE instances share `compute.googleapis.com/v1` — both fit in one `services/compute/` directory to avoid action-dispatch split-brain. ELBv2 is a distinct AWS endpoint and lives in `services/loadbalancer/`.
>
> **Status: 16.A ✅ · 16.B ✅ · 16.C ✅ · 16.D ✅. Phase 16 complete (PRs #104–#120, closed 2026-06-03).**

### Sub-phases

| Track | What | Dependency | Status |
|---|---|---|---|
| 16.A | Normalization audit (N20–N27) + `docs/phase-16-scoping.md` + ec2Query codegen lane + `internal/ec2query/` runtime | — | ✅ PR #104 |
| 16.B | VPC networking primitives (VPCs/subnets/security groups/public IPs) — AWS + GCP + Azure frontends + K8s peer | 16.A | ✅ PRs #105–#108 |
| 16.C | Compute instance lifecycle (RunInstances/Describe/Start/Stop/Terminate/Reboot + DescribeInstanceTypes) — K8s Nodes read-only; mutations NotImplemented | 16.B | ✅ PRs #111–#120 |
| 16.D | Load balancers — layer-4 TCP (LB + target group + listener); `services/loadbalancer/`; K8s = Service:LB; RegisterTargets gated on #373/#374/#375 | 16.A | ✅ PRs #109–#110 |

### Codegen prerequisite (16.A)

EC2 uses `aws.protocols#ec2Query` — a distinct variant from `awsQuery`. Differences: flattened list serialization (`Field.N` vs `Field.member.N`), different error envelope (`<Response><Errors><Error>` vs `<ErrorResponse>`). Adding the lane requires:
- `internal/codegen/emit/template_ec2query.tmpl` (new, ~300 lines)
- `internal/codegen/emit/emit.go` (5-line branch for `ec2Query` detection)
- `internal/ec2query/router.go` (new, ~100 lines — Action dispatch + XML response helpers)

ELBv2 uses standard `awsQuery` (same as SNS/RDS/ElastiCache) — no new codegen for 16.D AWS frontend.

### Normalization rules (16.A deliverables)

- **N20** — Instance state machine: AWS (pending/running/stopping/stopped/shutting-down/terminated) vs GCP (GCP "TERMINATED" = stopped, not deleted) vs Azure (Deallocated = stopped-but-provisioned ≠ deleted). Domain carries `{pending, running, stopped, terminated}`; backends translate native states.
- **N21** — Security group semantics: AWS SGs stateful + ENI-attached; GCP firewalls stateless + network-scoped + tag-targeted; Azure NSGs stateful + subnet/NIC-attached + numeric priorities. Intersection: allow-only rules by protocol+port+sourceCIDR; no deny; no priorities; no tag targeting. GCP backend adds matching egress rule so return traffic is implicitly allowed.
- **N22** — Public IP model: two-step (allocate → associate) on all three clouds. K8s peer: NotImplemented (no public-IP primitive). `AllocateAddress` returns a domain `PublicIP`; `AssociateAddress` links it to an instance.
- **N23** — Machine/instance type naming: opaque per-cloud (like N13 for cache tiers). AMI IDs, GCE machine type names, Azure VM size strings pass through without translation. Cross-cloud Apply must supply destination-appropriate type.
- **N24** — Instance image reference: opaque per-cloud (like N6 for regions). AMI IDs, GCE image resource names, Azure Marketplace URNs pass through. Cross-cloud Apply fails with destination's image-not-found if image reference is source-cloud-specific.
- **N25** — VPC CIDR assignment: AWS requires CIDR on `CreateVpc`; GCP VPCs are custom-mode (CIDR lives on subnetworks, not on the VPC itself); Azure requires `addressSpace`. Domain `CreateNetworkOptions.CIDR` is optional; GCP backend ignores CIDR at the network level and uses it at subnetwork level.
- **N26** — Subnet availability zone: AWS subnets are AZ-scoped; GCP subnetworks are region-scoped; Azure subnets are VNet-scoped with no zone. Domain `Subnet.Zone` is optional; K8s peer: NotImplemented for subnets entirely.
- **N27** — LB layer restriction: intersection is layer-4 TCP forwarding only. ALB, L7 routing rules, host/path-based routing, TLS termination, WAF — out of intersection. Attempting to configure L7 features returns the source cloud's `InvalidConfigurationRequest` (or equivalent) error.

### K8s peer mapping

| Operation | K8s resource | Fidelity |
|---|---|---|
| CreateNetwork / Get / List / Delete | Namespace | Full |
| CreateSecurityGroup / Delete / List + Add/RemoveIngressRule | NetworkPolicy (namespace = network name; podSelector = {}) | Intersection (allow-only; egress via Egress PolicyType) |
| CreateSubnet / AllocatePublicIP / AssociatePublicIP | — | NotImplemented |
| RunInstances / Start / Stop / Terminate / Reboot | — | NotImplemented (source-cloud OperationNotSupported) |
| DescribeInstances / DescribeInstanceTypes | Node list/get; type from Node capacity | Read-only |
| CreateLoadBalancer / Delete / Describe | Service (type:LoadBalancer) | Full |
| RegisterTargets / DeregisterTargets | Endpoints | Full |

### Sockerless dependencies

| Issue | What it blocks |
|---|---|
| [sockerless #373](https://github.com/e6qu/sockerless/issues/373) — `/dev/kvm` not in capability check | 16.C sockerless lane (Firecracker exits before API socket without KVM) |
| [sockerless #374](https://github.com/e6qu/sockerless/issues/374) — 3 GB rootfs per VM on 14 GB runners | 16.C sockerless lane (disk exhaustion before any instance boots) |
| [sockerless #375](https://github.com/e6qu/sockerless/issues/375) — asset caching + ubuntu-latest pin | 16.C sockerless lane (5-min timeout too tight on cold runners) |

16.B (networking) and 16.D LB-create/describe have **no** dependency on these. The sockerless instance lane and RegisterTargets lane skip cleanly (with a message referencing the issues) until they close.

### Out of intersection (Phase 16)

NAT Gateways · Internet Gateways · Route Tables · VPC Peering · Auto Scaling Groups · EBS/Persistent Disk/Managed Disk (block storage — future phase) · ENIs/vNICs · Placement Groups · L7 load balancers · VPN/Interconnect/ExpressRoute · Instance Metadata Service (sockerless #371).

### Exit criteria

- `docs/phase-16-scoping.md` published; N20–N27 in `docs/normalizations.md`, each with code reference + test.
- `cmd/codegen` ec2Query lane generates correct request parsing + XML response; `internal/ec2query/` tests pass.
- `services/compute/` full 3 × 4 × 3 conformance matrix; NotImplemented rows in `services/compute/INTERSECTION.md`; sockerless networking lane green; sockerless instance lane added (skipping until #373–375 close).
- `services/loadbalancer/` full 3 × 4 × 3 conformance matrix; layer-4 restriction enforced; sockerless LB-create lane green; RegisterTargets lane skipping until #373–375.
- Cross-cloud Apply cell for at least one networking operation and one compute operation.

## Standing open questions (not phase-gated)

- Single org-wide deployment vs per-tenant — affects auth model.
- Where do live cloud test accounts live; who pays. Blocks Track A.
- Coding-agent permissions for upstream spec-version bumps: auto-PR or human-in-loop?
- AMQP fidelity tier for Azure Service Bus — REST-only initially, or AMQP from the start?

## Closed phases (PR index)

| PR | Phase | Headline | Merged |
|---|---|---|---|
| #104–#120 | 16 (all) | Phase 16 compute + networking + LB: ec2Query codegen, VPC networking (16.B), load balancers (16.D), compute instance lifecycle (16.C) — AWS/GCP/Azure/K8s frontends + real backends + full conformance matrix. 16.C includes AWS TF destroy waiter fix, GCP TF instance, Azure CLI az vm (BUG-57), Azure TF azurerm_linux_virtual_machine via sockerless passthrough (BUG-56), sockerless Firecracker lanes. | 2026-06-03 |
| #90–#108 (umbrella) | 15 | Cross-cloud normalization standard (N1–N27), NoSQL (DynamoDB/Firestore/Cosmos Tables), DNS (Route 53/Cloud DNS/Azure DNS/CoreDNS), sockerless Cosmos Tables ARM passthrough. 10 PRs, all four frontends × five backends. | 2026-06-02 |
| #21–#67 (umbrella) | 14 | Sockerless-verified validation lane + through-shim azurerm Apply matrix. 14.A re-enabled deferred shim assertions; 14.B/C closed the 33+1 sockerless lane sweep; 14.D simulator audit + 14.E cross-cloud Apply closed the storage matrix. | through 2026-05-30 |
| #20 | 13 | Azure + GCP adapter migrations, production RS256 JWKS, and the first sockerless storage/secrets lane. | 2026-05-24 `3cf9e13` |
| #19 | 12 | Spec-driven toolchain across all 8 services × 3 lanes (AWS Smithy / Azure OpenAPI / GCP Discovery). 8-stage Azure preprocessor closes BUG-20. Vendored-spec `_provenance` + spec-freshness lane + per-service Terraform walkthroughs. 82+ granular commits. | 2026-05-22 `778e8e9` |
| #18 | 11 | Tighten the wire boundary — 8/8 AWS frontends spec-driven (5 protocols); 24/24 frontends verifier-wrapped; BUG-18 closed end-to-end; Azure oapi-codegen pilot. | 2026-05-22 `bcd72e5` |
| #17 | 10 | Cross-cloud `terraform apply` across all 8 services; 8 BUGs closed; full developer + contributing docs. | 2026-05-21 `ebc30f7` |
| #16 | 9 docs + 10.1 | Phase 9 docs + BUG-5 stateless `Operations.Get`. | 2026-05-21 `326f57d` |
| #13 | 8 + 9 chunk | Phase 8 (API Gateway) + Phase 9 (cross-cloud import) chunk. | 2026-05-20 `ad85ddf` |
| #12 | 7 | Functions control-plane. | 2026-05-19 `9d02af0` |
| #11 | 6 | Managed Redis control-plane. | 2026-05-19 `cca8bc0` |
| #10 | 5 | Managed RDBMS control-plane. | 2026-05-19 `aeadbc8` |
| #9 | 4 | Pubsub. | 2026-05-19 `6305354` |
| #8 | 3 | Queue. | 2026-05-19 `07d11f5` |
| #7 | 2 | Secrets. | 2026-05-19 `7df43ec` |
| #6 | 1 | Object storage. | 2026-05-19 `1f64d9f` |
| #1, #2 | bootstrap | Repo + ruleset + Phase-0 CI checks. | 2026-05-18 |

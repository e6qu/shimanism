# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #17 (Phase 10 + codex doc review pass — 8/8 services apply-active, 8 BUGs closed) at `ebc30f7` on `origin/main`, 2026-05-21.
- **Active branch:** `phase-11`. Single PR for the whole Phase 11 effort (spec-driven codegen across every service + signature verification at the new decode boundary). Granular commits as sub-phases land.
- **Phase 10** closed in PR #17. Cross-cloud `terraform apply` honest end-to-end across all 8 services; `TestCrossCloudApply_Roundtrip_StorageAWStoGCS` is the exit criterion (passes). 8 BUGs closed in the PR.
- **Phase 11 plan** drafted at [`PHASE_11_PLAN.md`](PHASE_11_PLAN.md). Codex review pending before code lands. Codegen extension order locked-in: OpenAPI v3 (Azure) first via `oapi-codegen`, then AWS Smithy emitter extension, then GCP Discovery / protobuf routing-only.

## Phase 11 sub-task table

Plan in [`PHASE_11_PLAN.md`](PHASE_11_PLAN.md). One PR for the whole phase; granular commits per sub-phase.

| Sub | Status | Headline |
|---|---|---|
| **11.0** | ◐ | Scope baseline. `PHASE_11_PLAN.md` drafted; submit to codex review before code lands. |
| **11.1** | ◻ | BUG-15 walk (GCP Pub/Sub provider-default audit across `message_retention_duration` / `expiration_policy` / `retain_acked_messages` / `enable_message_ordering`) + BUG-8 Track-A pin (no code change). |
| **11.2** | ◻ | OpenAPI v3 emitter foundation. `oapi-codegen` adapter pilot on Azure Key Vault secrets surface → `services/secrets/gen/azure/`. |
| **11.3** | ◻ | **Secrets: first service end-to-end spec-driven.** AWS Secrets Manager via extended Smithy emitter; Azure Key Vault via 11.2 OpenAPI pipeline; GCP Secret Manager via reused `google.golang.org/api/secretmanager/v1` + emitted routing layer. Hand-written wire deleted. Conformance unchanged. |
| **11.4** | ◻ | **BUG-18 signature verification at the secrets decode boundary.** SigV4 (AWS), OAuth2 JWT (GCP), SharedKey + Bearer (Azure). Conformance lanes drop the auth-bypass knobs; deterministic test signing key. |
| **11.5** | ◻ | Roll forward to queue. SQS Smithy `awsJson1_0`, Azure Service Bus admin OpenAPI, GCP Pub/Sub Discovery. Signature verification per frontend. |
| **11.6** | ◻ | Roll forward to pubsub. AWS awsQuery XML (Smithy 2.0 protocol — verify support before scoping), GCP Pub/Sub Discovery, Azure Service Bus topics OpenAPI. |
| **11.7** | ◻ | Roll forward to rdbms. AWS awsQuery XML (RDS), GCP Cloud SQL Admin Discovery, Azure ARM OpenAPI. |
| **11.8** | ◻ | Roll forward to cache. AWS awsQuery XML (ElastiCache), GCP Memorystore REST, Azure ARM OpenAPI. |
| **11.9** | ◻ | Roll forward to functions. AWS restJson1 (Lambda), GCP Cloud Run Discovery, Azure Container Apps ARM OpenAPI. |
| **11.10** | ◻ | Roll forward to apigateway. AWS restJson1 (APIGW v2), GCP API Gateway Discovery, Azure APIM ARM OpenAPI. |
| **11.11** | ◻ | Storage retrofit. Apply signature verification to existing `services/storage/gen/` Smithy stubs. Drop the corresponding auth-bypass knobs from storage conformance lanes. |
| **11.12** | ◻ | Phase 11 closer. All 8 services spec-driven; `make codegen` regenerates everything; BUG-18 closed; auth-bypass flag deleted across conformance. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 11 design notes

- **Codegen + verification coupled per service.** Each service migration lands the generated stubs *and* the signature verifier wired at the new decode boundary together. Avoids retrofitting verification glue into hand-written handlers we're replacing.
- **Adapter glue first; custom emitter only on demand.** Phase 11.2 builds an adapter that maps `oapi-codegen`'s emitted server interface to the shim's `(http.ResponseWriter, *http.Request)` shape. If the adapter grows past ~3 LOC per operation, switch to a custom OpenAPI emitter in `internal/codegen/`.
- **`translate.go` stays hand-written and auth-unaware.** Generated stubs call the verifier; the verifier rejects with the source cloud's own 401/403 envelope before dispatch. Per-operation translation logic doesn't change shape.
- **Deterministic project-owned test signing key.** Conformance lanes generate real signed requests via a test key the shim trusts only when an explicit env var is set. Real-cloud lanes (Track A) use real signatures.
- **Stateless invariant carried.** Verification consumes the request signature once at the boundary; the shim doesn't cache claims, doesn't open sessions, doesn't propagate caller credentials to the backend.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR at a time.** Phase 11 = one PR; all sub-phases on `phase-11`.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API.
- Real backends only.
- Tests from official client surfaces.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention.**

## Resumable tracks

- **Track A — Cloud test accounts.** Real-cloud lanes for Apply against real AWS / GCP / Azure accounts; also the home for real-signed signature-verification conformance. Blocks BUG-8 closure.
- **Track B — Coding-agent automation.**
- **BUG-8 (apigateway/gcp-tf-frontend).** `hashicorp/google` API Gateway endpoint-override + OAuth signing — Track A only.
- **BUG-15 (queue retention plan/apply asymmetry).** Partial fix landed in Phase 10.3. Phase 11.1 walks the provider behavior and either closes or reclassifies.
- **BUG-18 (signature verification across frontends).** Phase 11.4 lands it on the secrets service first; subsequent service migrations carry it forward. Phase 11.11 retrofits storage.
- **Renovate coverage of vendored specs.** Renovate today tracks Go modules + GitHub Actions; vendored specs in `services/<svc>/spec/` are manual. Tracked task for Phase 11.0 — wire spec freshness into CI (compare vendored hash vs. upstream HEAD; alert on drift).

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — verify the Phase 11 PR is the only one open (once code lands).
3. `git checkout phase-11`.
4. Read STATUS snapshot + this file's "Where we are".
5. Read STATUS invariants + AGENTS.md.
6. Skim BUGS open (3 entries: BUG-8, BUG-15, BUG-18).
7. Read [`PHASE_11_PLAN.md`](PHASE_11_PLAN.md) for the codegen extension order and sub-phase rationale.
8. Pick the next ◻ sub-task from the Phase 11 table.

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #17 at `ebc30f7` on `origin/main`, 2026-05-21. Phase 10 closed — cross-cloud `terraform apply` honest end-to-end across all 8 services; 8 BUGs closed.
- **Active branch:** `phase-11`. PR #18 (draft) holds the Phase 11 plan baseline. Codex review folded back; Phase 12 added for cross-cloud cell expansion.
- **Phase 11 sub-task table:** lives in [PLAN.md § Phase 11](PLAN.md#phase-11--tighten-the-wire-boundary). Do not duplicate it here — update it in PLAN.md as sub-tasks land.
- **Phase 12 sub-task table:** lives in [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion). Doesn't depend on Phase 11.

## Next concrete actions (in priority order)

PR #18 has shipped major Phase 11 progress (sub-phases 11.0–11.3 + 11.6a-d + 11.7a + 11.9 + 11.10 + 11.13a/b). Pick from these in priority order:

1. **`awsQuery` emitter extension.** Highest leverage — unblocks SNS / RDS / ElastiCache (3 services). Form-encoded request body parser + XML response encoder + XML error envelope. Largest remaining emitter chunk; ~60-120 min focused work.
2. **SNS / RDS / ElastiCache adapter migrations.** Depend on (1). Pattern is identical to 11.3c / 11.7a / 11.9b / 11.10b once the emitter is in place.
3. **GCP routing emitter (11.5).** Discovery JSON → routing-only Go (wire types via `google.golang.org/api/<svc>/v1`). Start with Secret Manager; then GCP frontend migrations for all 8 services. ~60-90 min for the emitter, ~30 min per service after.
4. **Azure `oapi-codegen` pilot (11.4).** Net/http stubs + `kin-openapi` validation + Bearer challenge + ARM LRO. Key Vault first; then Azure frontend migrations for all 8 services.
5. **Azure Bearer + SharedKey verifier packages.** Same shape as `internal/gcpbearer` (already built). Wire into Azure-shaped frontends. SharedKey for Blob (Storage retrofit).
6. **GCS + Azure Blob storage retrofit (11.13b/c).** Wrap the existing GCS + Azure Blob handlers with the bearer / SharedKey verifiers (analogous to the S3 SigV4 wrap in 11.13a).
7. **Conformance lane rewrite.** Drop `SHIMANISM_TEST_UNAUTHENTICATED=1` from `internal/harness/server.go init()`; convert each conformance test's AWS-SDK client to sign with the test access-key + secret instead of `AnonymousCredentials{}`.
8. **Closer (11.14).** BUG-18 closed in BUGS.md; all 8 services spec-driven + verified.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR at a time.** Phase 11 = one PR; all sub-phases on `phase-11`.
- **One plan file.** PLAN.md is the only roadmap doc; per-phase plans live inline as a section.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- **Reuse over reinvention.**

## Open bugs (3)

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. **Track A only** (real-cloud lanes).
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3; Phase 11.1 walks the provider behavior and either closes or reclassifies.
- **BUG-18** (P1) — all frontends. No signature validation (SigV4 / OAuth2 / SharedKey). Phase 11.4 lands it on secrets first; subsequent service migrations carry it forward; Phase 11.11 retrofits storage.

## Resumable tracks

- **Track A — Cloud test accounts.** Real-cloud Apply lanes against real AWS / GCP / Azure accounts; also the home for real-signed signature-verification conformance. Blocks BUG-8 closure.
- **Track B — Coding-agent automation.**
- **Renovate coverage of vendored specs.** Renovate tracks Go modules + GitHub Actions today; vendored specs in `services/<svc>/spec/` are manual. Wire spec freshness into CI (compare vendored hash vs upstream HEAD; alert on drift) — tracked task during Phase 11.0.

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — verify only the Phase 11 PR is open (once code lands).
3. `git checkout phase-11`.
4. Read STATUS snapshot + this file's "Where we are".
5. Skim BUGS open (3 entries).
6. Read [PLAN.md § Phase 11](PLAN.md#phase-11--tighten-the-wire-boundary) for sub-phase rationale and the codegen extension order.
7. Pick the next ◻ sub-task from the PLAN.md Phase 11 table.

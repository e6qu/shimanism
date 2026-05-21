# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #17 at `ebc30f7` on `origin/main`, 2026-05-21. Phase 10 closed — cross-cloud `terraform apply` honest end-to-end across all 8 services; 8 BUGs closed.
- **Active branch:** `phase-11`. PR #18 (draft) holds the Phase 11 plan baseline. Codex review folded back; Phase 12 added for cross-cloud cell expansion.
- **Phase 11 sub-task table:** lives in [PLAN.md § Phase 11](PLAN.md#phase-11--tighten-the-wire-boundary). Do not duplicate it here — update it in PLAN.md as sub-tasks land.
- **Phase 12 sub-task table:** lives in [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion). Doesn't depend on Phase 11.

## Next concrete actions (in priority order)

PR #18 has shipped substantial Phase 11 progress. The remaining work doesn't fit one session; pick from these in priority order.

1. **Lambda adapter migration (11.9b).** Gen file in tree (`services/functions/gen/aws_lambda.gen.go`, 14 ops). Write `internal/functions/frontends/aws_lambda/adapter.go` implementing `gen.LambdaBackend`; delete `server.go` + `errors.go`. Same shape as `internal/secrets/frontends/aws_secretsmanager/adapter.go`. ~30-60 min.
2. **APIGW v2 adapter migration (11.10b).** Same shape; gen file in tree.
3. **Per-frontend SigV4 wiring (11.6b).** Verifier package exists. Each AWS adapter calls `verifier.Verify(r)` early; conformance tests use the deterministic test signing key. Start with secrets (already migrated, smallest surface).
4. **`awsQuery` emitter extension.** Form-encoded request body + XML response + XML error envelope. Heaviest remaining emitter work; unblocks SNS, RDS, ElastiCache (11.8 / 11.11 / 11.12).
5. **GCP routing emitter (11.5).** Discovery JSON → routing-only Go (wire types via `google.golang.org/api/<svc>/v1`). Start with Secret Manager.
6. **Azure `oapi-codegen` pilot (11.4).** Net/http stubs + `kin-openapi` validation + Bearer challenge + ARM LRO. Key Vault first.
7. **Storage retrofit (11.13).** Wire SigV4 + SharedKey + bearer verifiers onto existing `services/storage/gen/` stubs.
8. **Conformance lane cleanup + closer (11.14).** Drop auth-bypass knobs; BUG-18 closed.

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

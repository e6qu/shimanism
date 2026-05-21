# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #18 at `bcd72e5` on `origin/main`, 2026-05-22. Phase 11 closed — BUG-18 closed end-to-end; 8/8 AWS frontends spec-driven; Azure oapi-codegen pilot proves the spec-driven lane for Azure.
- **Active branch:** `phase-12`. PR forthcoming once the first substantive chunk lands. One-PR-per-phase rule applies: every Phase 12 sub-phase lands on `phase-12`.
- **Phase 12 sub-task plan:** lives in [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons). Two parallel tracks (12.A/B/C absorb the Phase 11 deferrals; 12.1–12.8 land one cross-cloud Apply cell per service).

## Next concrete actions (in priority order)

The first substantive chunk is **12.A.1 — complete the azure_keyvault spec-driven migration** (the 11.4 pilot migrated only `SetSecret`; the rest of the handlers still use hand-rolled wire types). Smallest unit of work that continues from a known-good baseline.

1. **12.A.1** — migrate the remaining `azure_keyvault` handlers (`getSecret`, `deleteSecret`, `listSecrets`, `listSecretVersions`, `getSecretVersion`, `purgeSecret`) to decode/encode via the spec-driven `gen.SecretBundle` / `gen.DeletedSecretBundle` / `gen.SecretListResult` types. Verify conformance stays green.
2. **12.A.2** — replace `azure_keyvault/server.go`'s hand-rolled regex router with `gen.HandlerWithOptions`. Adapter implements the generated `gen.ServerInterface`.
3. **12.A.3–7** — vendor + codegen + adapter migration for the other 7 Azure frontends (storage / queue / pubsub / rdbms / cache / functions / apigateway). Same `cmd/azure-codegen` pipeline; `services/<svc>/azure-codegen.json` per service.
4. **12.B** — GCP routing emitter (Discovery JSON → routing Go) + 8 adapter migrations. Hand-written GCP frontends keep working; the emitter adds dispatch consistency.
5. **12.1–12.8** — Track 1 cross-cloud cells. Cell selection per service in [PLAN.md § Phase 12 Track 1 table](PLAN.md#track-1--cross-cloud-cells).
6. **12.C** — production RS256 JWKS for real Google + Microsoft Entra tokens. Lower priority; deferred until a deployment target requires it.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR per phase.** All Phase 12 work lands on `phase-12`.
- **One plan file.** PLAN.md is the only roadmap doc.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- **Reuse over reinvention.**

## Open bugs (2)

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. **Track A only** (real-cloud lanes).
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3; Track-A real-cloud walk pending.

## Resumable tracks

- **Track A — Cloud test accounts.** Real-cloud Apply lanes against real AWS / GCP / Azure accounts; also the home for real-signed signature-verification conformance. Blocks BUG-8 closure + BUG-15 reclassification.
- **Track B — Coding-agent automation.**
- **Renovate coverage of vendored specs.** Renovate tracks Go modules + GitHub Actions today; vendored specs in `services/<svc>/spec/` are manual. Wire spec freshness into CI (compare vendored hash vs upstream HEAD; alert on drift) — 12.0 candidate.

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — Phase 12 PR (`#19` likely) is the open item; verify CI status.
3. `git checkout phase-12`.
4. Read STATUS snapshot + this file's "Where we are".
5. Skim BUGS open (2 entries).
6. Pick the next ◻ or ◐ sub-task from [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons).

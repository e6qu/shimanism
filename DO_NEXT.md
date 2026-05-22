# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #18 at `bcd72e5` on `origin/main`, 2026-05-22. Phase 11 closed — BUG-18 closed end-to-end; 8/8 AWS frontends spec-driven; Azure oapi-codegen pilot proves the spec-driven lane for Azure.
- **Active branch:** `phase-12`. PR forthcoming once the first substantive chunk lands. One-PR-per-phase rule applies: every Phase 12 sub-phase lands on `phase-12`.
- **Phase 12 sub-task plan:** lives in [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons). Two parallel tracks (12.A/B/C absorb the Phase 11 deferrals; 12.1–12.8 land one cross-cloud Apply cell per service).

## Next concrete actions (in priority order)

**Phase 12 substantively landed on PR #19.** Spec-driven toolchain for both clouds is fully built + validated:
- 7/8 Azure specs codegen end-to-end (`azure_keyvault` fully migrated as reference impl)
- 8/8 GCP services Discovery → route inventory generated
- Per-service spec-drift + gen-compile tests
- CI `codegen deterministic` job
- 6-stage Azure preprocessor (common-types / multi-file / examples-skip / x-ms-enum / x-ms-paths flatten)

Mechanical follow-ons remain post-merge:

1. **Adapter migrations.** 6 Azure frontends + 8 GCP frontends still dispatch via hand-written regex routes. Each migration: wire generated types into request decoding + response encoding; route through the gen `ServerInterface` (Azure) / `gen.gcp.Routes` inventory (GCP). The hand-written frontends keep passing conformance, so migration is dispatch-consistency, not fidelity.
2. **Azure Blob full unblock.** `flattenXMSPaths` handles the `x-ms-paths` layer; the spec has additional ref-shape quirks (refs into `#/components/schemas/AccessTier` the v2→v3 converter expects as parameter refs). Needs another per-spec preprocessor pass.
3. **Production RS256 JWKS.** Verifiers run test-mode HS256; production paths documented in the verifier comments (`google.golang.org/api/idtoken.Validate`, Microsoft's JWKS).
4. **Track 1 cross-cloud cells.** Largely covered by Phase 10's per-service cross-cloud Apply tests; matrix-expansion candidates documented in PLAN.md.

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

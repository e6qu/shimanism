# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-1.4-conformance-harness` — PR open. Intersection scoping correction + conformance harness skeleton. |
| In-flight | **Phase 3 on `phase-3-queue` branch.** Three queue frontends (AWS SQS, GCP Pub/Sub pull, Azure Service Bus queue) × four backends (the three clouds + NATS JetStream as K8s peer) × three driver types (SDK + CLI + Terraform). Same N × N matrix discipline as Phases 1 + 2. Design baseline in [`services/queue/OPERATIONS.md`](services/queue/OPERATIONS.md) — 8-op intersection with opaque receipt handles, capped 600s visibility timeout, capped 20s long-poll wait. |
| Phase 2 closed | PR #7 merged `7df43ec` 2026-05-19. Three secrets frontends × five secrets backends × three driver types; 12 required CI checks. Stateless invariant + shimakit framework + shima<service> naming convention landed alongside. |
| Phase 1 closed | PR #6 merged `1f64d9f` 2026-05-19. Three storage frontends × five storage backends × three driver types matrix; 11 required CI checks. |
| CI baseline | Required checks include the 12 Phase-2 ones; Phase 3 will add `conformance-nats` (NATS dev container) when 3.15 lands. |
| Scope rule (2026-05-18) | **Each phase ships the full N × N matrix.** Previous PLAN.md had Phases 9 and 10 as "GCP source row" and "Azure source row" of horizontal expansion across all 8 services; user reversed this. Each service phase now includes all 3 frontends + all 4 backends + SDK / CLI / Terraform for each, before moving to the next service. Phases 9 and 10 deleted; their work is absorbed into Phases 1-8. |
| Last merged | PR #5 — Phase 1.3 (codegen, originally all 107 ops) (`03b0ebb`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. |
| Renovate | Config committed (48h minimum release age, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
| Bugs | 1 filed · 1 fixed · 0 open. |
| Live infra | None. |

## Invariants (carry across compactions / fresh sessions)

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user. User merges.
- **Single-branch rule.** All work for one phase / sub-phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then start the fix commit.
- **Update continuity docs every significant chunk** (not just at phase end): STATUS.md + WHAT_WE_DID.md + DO_NEXT.md. This is what lets context survive compaction.
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing; sync local `main` after merge.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.
- **One service per phase.** Each phase ships one shimmed service end-to-end against all backends in scope.

### Architecture (load-bearing across all services)
- **The shim speaks the cloud's published API exactly.** Error shapes, response headers, status codes, async semantics — match. Server stubs are generated from the upstream spec; hand-written code is translation logic only.
- **Real backends, never emulators.** A shimmed call drives a real comparable service. The shim holds no state of record.
- **Stateless shim.** No sidecar storage, no shim-managed key/value namespace, no in-process cache treated as authoritative. State lives in the backend; cross-cloud mappings are derived at request time. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless).
- **Intersection-only scope.** Out-of-intersection feature calls fail loud with the source cloud's own error vocabulary. **Never fabricate success.**
- **Kubernetes is a first-class fourth backend** for every shimmed service.
- **No fakes, no fallbacks, no degraded modes.** Translation can't be honest → call fails loud.
- **Test from the official client surfaces.** SDK + CLI + Terraform provider in the same commit, against every backend in scope.

### Locked-in decisions (full table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions))
- Go is the implementation language.
- Specs pulled upstream, never forked: AWS Smithy JSON, GCP protobuf, Azure OpenAPI.
- Codegen: spec → typed Go server stubs; per-operation `translate.go` is the only hand-written code.
- Monorepo: `services/<service>/`, shared `internal/codegen/`, `internal/harness/`.
- Test rings: per-PR recorded interactions, nightly live cloud, pre-release vendor integration suites.

## Current phase — Phase 1: Object storage (S3-source)

Phase 1 carries the foundation work alongside its first real consumer. The codegen pipeline, conformance harness, and Go CI matrix are built inside Phase 1 sub-phases rather than as standalone infrastructure.

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md) and [PLAN.md § Phase 1](PLAN.md#phase-1--object-storage-s3-source). PR #6 piles sub-phases 1.4 through 1.7.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #4 | 1.2 | S3 Smithy spec vendored + license policy + Renovate + dependency policy (48h release age, pure-Go preference, pnpm + no lifecycle scripts) + version bumps to Go 1.26 / actions v6. Merged 2026-05-18 at `98e6ce9`. |
| #3 | 1.1 | Repo skeleton: Go module (1.25), Makefile, Go CI lane. PLAN.md restructured to one-service-per-phase. Merged 2026-05-18 at `48c0edf`. |
| #2 | (bootstrap) | Continuity docs + Phase-0 CI checks wired into the main-branch ruleset. Merged 2026-05-18 at `4549a90`. |
| #1 | (bootstrap) | Repo created. Branch ruleset. PHILOSOPHY.md as koans. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |

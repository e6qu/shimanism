# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-1.1-repo-skeleton` — PR open. PLAN.md restructured to one-service-per-phase; Phase 1.1 (Go module skeleton) added. |
| In-flight | **Phase 1.1: Repo skeleton.** Go module at `github.com/e6qu/shimanism`, Makefile, Go CI lane. No service code yet beyond a placeholder `cmd/shim/main.go`. |
| Last merged | PR #2 — Continuity docs + Phase-0 CI checks (`4549a90`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Three required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`. Go lane added in Phase 1.1 (not yet required by ruleset — will be after first green run). |
| Bugs | 0 filed · 0 fixed · 0 open. |
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

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md) and [PLAN.md § Phase 1](PLAN.md#phase-1--object-storage-s3-source). Currently at 1.1.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #2 | (bootstrap) | Continuity docs + Phase-0 CI checks (branch rebased on origin/main, symlinks resolve, continuity docs present) wired into the main-branch ruleset. Merged 2026-05-18 at `4549a90`. |
| #1 | (bootstrap) | Repo created. Branch ruleset (linear history, PR-only, squash + rebase merge). PHILOSOPHY.md as koans + Bierce terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |

# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-1.2-s3-spec-ingest` — PR open. AWS S3 Smithy JSON vendored under `services/storage/spec/`; refresh tooling wired. |
| In-flight | **Phase 1.2: Spec ingestion + engineering hygiene.** S3 Smithy JSON pinned via `make fetch-specs`. License policy in `doc/COMPATIBLE_LICENSES.md`; `make license-check` + CI lane enforce it. Renovate config (`.github/renovate.json5`) wired. Bumped to Go 1.26 + actions/checkout@v6 + actions/setup-go@v6. |
| Last merged | PR #3 — Phase 1.1 repo skeleton + PLAN restructure (`48c0edf`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. First four required by ruleset; license check added in Phase 1.2 — wires to ruleset after first green run. |
| Renovate | Config committed (48h minimum release age for supply-chain mitigation, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate for it to take effect. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
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
| #3 | 1.1 | Repo skeleton: Go module (1.25), Makefile, Go CI lane. PLAN.md restructured to one-service-per-phase. Merged 2026-05-18 at `48c0edf`. |
| #2 | (bootstrap) | Continuity docs + Phase-0 CI checks (branch rebased on origin/main, symlinks resolve, continuity docs present) wired into the main-branch ruleset. Merged 2026-05-18 at `4549a90`. |
| #1 | (bootstrap) | Repo created. Branch ruleset (linear history, PR-only, squash + rebase merge). PHILOSOPHY.md as koans + Bierce terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |

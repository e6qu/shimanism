# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `continuity-docs` — this PR adds PLAN / STATUS / WHAT_WE_DID / DO_NEXT / BUGS / AGENTS / CLAUDE.md→AGENTS.md symlink. |
| In-flight | Pre-phase: locking in foundational decisions (see [PLAN.md § Pre-phase decisions](PLAN.md#pre-phase-decisions-to-lock-in-before-phase-0)). No service code yet. |
| Last merged | PR #1 — Initial PHILOSOPHY.md + README.md (`e5cc262`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Not yet set up. No conformance harness yet. |
| Bugs | 0 filed · 0 fixed · 0 open. |
| Live infra | None. |

## Invariants (carry across compactions / fresh sessions)

### Process
- **Never auto-merge PRs.** Push, wait for CI green (once CI exists), ping user. User merges.
- **Single-branch rule.** All work for one phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then start the fix commit.
- **Update continuity docs every significant chunk** (not just at phase end): STATUS.md + WHAT_WE_DID.md + DO_NEXT.md. This is what lets context survive compaction.
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing; sync local `main` after merge.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs. Code comments document *what* and *why*.

### Architecture (load-bearing across all services)
- **The shim speaks the cloud's published API exactly.** Error shapes, response headers, status codes, async semantics — match. Server stubs are generated from the upstream spec; hand-written code is translation logic only.
- **Real backends, never emulators.** A shimmed call drives a real comparable service somewhere else. The shim holds no state of record.
- **Intersection-only scope.** Shim what AWS / GCP / Azure / K8s-peer all support. Out-of-intersection feature calls fail loud with the source cloud's own error vocabulary. **Never fabricate success.**
- **Kubernetes is a first-class fourth backend** for every shimmed service. If no suitable open-source peer exists, we build one.
- **No fakes, no fallbacks, no degraded modes.** If a dependency is required, it is required. Translation can't be honest → call fails loud.
- **Test from the official client surfaces.** Every shimmed operation is exercised by SDK + CLI + Terraform provider in the same commit, against every backend in scope.

### Continuity-doc rules
- **STATUS.md = current state**, snapshot + invariants + active phase. Update on every significant chunk.
- **WHAT_WE_DID.md = reverse-chronological narrative**, per-phase entries with *why* + surprises + root causes. Not per-PR; not per-bug.
- **DO_NEXT.md = resume checklist + in-flight sub-tasks**. Front-loaded for the session-resume case.
- **BUGS.md = log every bug *before* fixing**. One-liner; details defer to `git log <commit>`.
- **PLAN.md = roadmap**, phase definitions, exit criteria, closed-phase index.
- **AGENTS.md = behavioral rules** for any agent (human or AI) working in this repo. `CLAUDE.md` is a symlink to it.

## Current phase — Pre-phase 0 (foundational decisions)

No code phase has begun. The Pre-phase decisions in [PLAN.md](PLAN.md#pre-phase-decisions-to-lock-in-before-phase-0) need user confirmation before Phase 0 work starts. Listed defaults are the recommendation; user signs off, then Phase 0 work fans out.

After Pre-phase sign-off, Phase 0 deliverables (spec ingestion, codegen, conformance harness, CI matrix) become the first concrete code work.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #1 | (bootstrap) | Repo created with branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce-style terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |

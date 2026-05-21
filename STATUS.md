# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-11`. Plan landed; code work pending codex review of [PLAN.md § Phase 11](PLAN.md#phase-11--tighten-the-wire-boundary). |
| In-flight | **Phase 11 — Tighten the wire boundary.** Spec-driven codegen across every service + signature verification (BUG-18) at the new decode boundary. Coupled per service. Codegen extension order locked-in: OpenAPI v3 (Azure) first via `oapi-codegen`, then AWS Smithy emitter extension, then GCP Discovery/protobuf routing-only. Full sub-task table in [PLAN.md](PLAN.md#sub-phases) and [DO_NEXT.md](DO_NEXT.md). |
| Last merged | PR #17 — Phase 10 + codex doc review (8/8 services apply-active, 8 BUGs closed). `ebc30f7`, 2026-05-21. |
| Phases 1-10 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | 19 filed · 16 fixed · 3 open · 1 false positive. Open: BUG-8 (apigateway/gcp-tf — Track A only), BUG-15 (queue retention plan/apply asymmetry — partial fix; Phase 11.1 walks), BUG-18 (P1: no signature validation across frontends — Phase 11.4 onward closes it service-by-service). |
| CI | 16 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config committed (48h min release age, weekly batches, pinned action SHAs). **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. |
| Live infra | None. |

## Invariants (carry across compactions / fresh sessions)

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user. User merges.
- **Single-branch rule.** All work for one phase / sub-phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then start the fix commit.
- **Update continuity docs every significant chunk** (not just at phase end): STATUS.md + WHAT_WE_DID.md + DO_NEXT.md.
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing; sync local `main` after merge.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.
- **One plan file.** PLAN.md is the only roadmap doc. Per-phase planning lives inline as a section in PLAN.md; no `PHASE_X_PLAN.md` files.

### Architecture (load-bearing across all services)
- **The shim speaks the cloud's published API exactly.** Error shapes, response headers, status codes, async semantics — match. Server stubs are generated from the upstream spec; hand-written code is translation logic only.
- **Real backends, never emulators.** A shimmed call drives a real comparable service. The shim holds no state of record.
- **Stateless shim.** No sidecar storage, no shim-managed key/value namespace, no in-process cache treated as authoritative. State lives in the backend; cross-cloud mappings are derived at request time. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless).
- **Intersection-only scope.** Out-of-intersection feature calls fail loud with the source cloud's own error vocabulary. **Never fabricate success.**
- **Kubernetes is a first-class fourth backend** for every shimmed service.
- **No fakes, no fallbacks, no degraded modes.** Translation can't be honest → call fails loud.
- **Test from the official client surfaces.** SDK + CLI + Terraform provider in the same commit, against every backend in scope.

### Locked-in decisions
Full table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions). Highlights: Go; specs pulled upstream (never forked); codegen owns wire stubs (`translate.go` is the only hand-written code); AGPL-3.0; reuse-over-reinvention; stateless shim; `shima<service>` naming for in-tree K8s peers built on `peers/shimakit/`.

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or a post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #1 (PHILOSOPHY.md + README.md) at `e5cc262` on `origin/main`, 2026-05-18.
- **Active branch:** `continuity-docs` — this PR adds the continuity files (PLAN / STATUS / WHAT_WE_DID / DO_NEXT / BUGS / AGENTS / CLAUDE.md symlink).
- **Project phase:** Pre-phase 0. No service code yet. We are still locking in foundational decisions.

## Immediate next sub-tasks

| Sub | Status | Headline |
|---|---|---|
| **Pre-phase.1** | ◐ | User confirmation of the [Pre-phase decision table](PLAN.md#pre-phase-decisions-to-lock-in-before-phase-0). Defaults are listed; user signs off or amends. Blocks Phase 0 start. |
| **Pre-phase.2** | ◻ | Choose first cloud-source spec to ingest end-to-end (recommendation: AWS S3 since it's the Phase 1 service). Validates the codegen pipeline before fanning out. |
| **Pre-phase.3** | ◻ | Pick the conformance-harness shape: how the SDK / CLI / Terraform-provider drivers plug in; how backend selection works; how recorded interactions are captured. Sketch in a `CONFORMANCE.md` (or extend PLAN.md). |
| **Phase 0.1** | ◻ | Repo skeleton: monorepo layout, `services/<svc>/` template, Go module structure, Makefile, basic CI on GitHub Actions. |
| **Phase 0.2** | ◻ | Spec ingestion: pull AWS Smithy JSON for one service (S3) from upstream. Vendor or just-in-time fetch — decide. |
| **Phase 0.3** | ◻ | Codegen pilot: Smithy JSON → Go server stub for one S3 operation (e.g. `ListBuckets`). Validate the spec→server flow before going wider. |
| **Phase 0.4** | ◻ | Conformance harness skeleton: invoke `aws s3api list-buckets`, `boto3.list_buckets()`, and a Terraform `data "aws_s3_bucket"` against an `EchoService` adapter that returns the canonical AWS shape. All three drivers must pass against the dummy. |
| **Phase 0.5** | ◻ | CI wires harness to every PR. Exit-criteria green = Phase 0 done. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Invariants snapshot (full list in [STATUS.md](STATUS.md))

- Never auto-merge; user merges every PR.
- Single-branch rule per phase; rebase on `origin/main` before push.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk — not just at phase end.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators.
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Needed before Phase 1 nightly tier can light up.
- **Track B — GCP-source horizontal expansion.** Holds until Phase 5 wraps; Phase 6 is the explicit handoff.
- **Track C — Azure-source + AMQP fidelity decision.** Holds until Phase 6 wraps.
- **Track D — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `git status` and `git log --oneline -5` — know the local state.
3. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
4. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
5. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new work.
6. Pick the next ◻ sub-task in this file; mark ◐ when starting; commit with the continuity-doc updates in the same PR.

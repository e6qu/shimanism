# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #5 (Phase 1.3 — codegen) at `03b0ebb` on `origin/main`, 2026-05-18.
- **Active branch:** `phase-1.4-conformance-harness` — PR open. Three commits piled on: (1) intersection scoping (16 ops); (2) SDK + CLI + Terraform conformance harness; (3) TF resource-lifecycle support (manifest grew to 34 ops including bucket-config probes; typed `restxml.ShimError` for backend-error → S3-status mapping).
- **Project phase:** **Phase 1 — Object storage (S3-source).** Phase 1.4 has the harness running real `terraform init / apply / destroy` against `resource "aws_s3_bucket"` + `resource "aws_s3_object"`.

## Phase 1 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **1.1** | ✅ | Repo skeleton: Go module at `github.com/e6qu/shimanism`, Makefile, Go CI lane, placeholder `cmd/shim/main.go`. PR #3, merged at `48c0edf`. |
| **1.2** | ✅ | Spec ingestion + engineering hygiene: S3 Smithy JSON vendored + license policy + Renovate + supply-chain hardening + version bumps. PR #4, merged at `98e6ce9`. |
| **1.3** | ✅ | Codegen pipeline. PR #5, merged at `03b0ebb`. |
| **1.4** | ◐ | Conformance harness + Terraform resource-lifecycle path. Manifest holds 34 ops (16 core + 18 bucket-config probes). `internal/restxml` ships URI / scalar / time / typed-error / router runtime. Generated handlers funnel backend errors through `WriteBackendError`. `services/storage/backends/inmem/` is a real in-memory backend covering all 34. Three conformance drivers (SDK / CLI / TF) run against it in CI. Open bug: [BUG-1](BUGS.md) (router x-id stripping shadows sibling-op disambiguation on object paths — shadowed for now, tracked). PR #6 open. |
| **1.5** | ◻ | First real backend: **MinIO**. S3-protocol passthrough — the "control case" that proves the shim's wire layer is faithful before we attempt translation. Phase 1.5 wires a `services/storage/backends/minio/` package, runs the same conformance suite against MinIO instead of the in-mem backend, and asserts the diff is zero. |
| **1.6** | ◻ | `ListBuckets` / `ListObjectsV2` / `GetObject` / `PutObject` → **GCS**. First real cross-cloud translation. The 18 bucket-config probes return their default-state response uniformly. |
| **1.7** | ◻ | Same surface → **Azure Blob**. |
| **1.8** | ◻ | `CopyObject` against all four backends. Azure block-blob block-ID translation; GCS rewrite semantics. |
| **1.9** | ◻ | Multipart upload against all four backends. GCS resumable session translation; Azure block-list translation. |
| **1.10** | ◻ | Presigned URLs. |
| **1.11** | ◻ | Fix BUG-1 (router x-id stripping) — likely as part of Phase 1.5+ once a real backend reveals where the leak matters in practice. |
| **1.12** | ◻ | Phase 1 closer: full conformance lane green across all four backends; Terraform `aws_s3_bucket` + `aws_s3_object` apply against MinIO / GCS / Azure Blob via `endpoints { s3 = ... }`. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-storage test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Needed before Phase 1.12 nightly tier can light up.
- **Track B — GCP source row (Phase 9).** Holds until Phase 8 (API Gateway) wraps.
- **Track C — Azure source row (Phase 10).** Holds until Phase 9 wraps. AMQP-vs-REST fidelity decision to be made at Phase 10.3 start.
- **Track D — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

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
| **1.5.0** | ◻ | Domain refactor. `internal/storage/domain/` introduces the neutral `Storage` interface (streaming-friendly: `io.Reader` for `httpPayload` blob inputs, `io.ReadCloser` for outputs). `internal/storage/frontends/aws_s3/` wraps `gen.AmazonS3Backend` and translates to `domain.Storage`. `services/storage/backends/inmem/` implements `domain.Storage` directly, drops the `gen.*` types. Codegen streaming changes for `httpPayload`+blob members. Conformance suite unchanged (still hits AWS frontend, in-mem backend). See [`doc/CROSS_CLOUD_ROUTING.md`](doc/CROSS_CLOUD_ROUTING.md). |
| **1.5.1** | ◻ | **MinIO backend** — `services/storage/backends/minio/` implements `domain.Storage` via `minio-go`. The S3-compatible "control case": same wire on both sides, proves the routing layer is faithful before we try a cross-shape translation. Docker-based MinIO in CI. |
| **1.5.2** | ◻ | **AWS passthrough backend** — `services/storage/backends/aws/` via `aws-sdk-go-v2/service/s3`. Useful for auth interception, observability injection, cross-region routing. |
| **1.6** | ◻ | **GCS backend** — first real cross-cloud (opposite-shape) translation. AWS-shaped frontend → `domain.Storage` → `cloud.google.com/go/storage` → real GCS. |
| **1.7** | ◻ | **Azure Blob backend** — same shape via `azure-sdk-for-go/sdk/storage/azblob`. |
| **1.8** | ◻ | **K8s peer backend** — MinIO-in-cluster via operator (or similar). Makes the "leave the cloud entirely" path real. |
| **1.9** | ◻ | `CopyObject` cross-cloud nuances. Azure block-blob block-ID translation; GCS rewrite semantics. |
| **1.10** | ◻ | Multipart upload cross-cloud nuances. GCS resumable session translation; Azure block-list translation. |
| **1.11** | ◻ | Presigned URLs. |
| **1.12** | ◻ | Fix [BUG-1](BUGS.md) (router x-id stripping) once a real backend reveals where the leak matters in practice. |
| **1.13** | ◻ | Phase 1 closer: full conformance lane green across all five backends; Terraform `aws_s3_bucket` + `aws_s3_object` apply against MinIO / AWS / GCS / Azure Blob / K8s peer via `endpoints { s3 = ... }`. |

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

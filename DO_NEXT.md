# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #4 (Phase 1.2 — vendor S3 spec + license policy + Renovate + dep policy + version bumps) at `98e6ce9` on `origin/main`, 2026-05-18.
- **Active branch:** `phase-1.3-codegen-pilot` — PR open. Codegen tool at `cmd/codegen` reads the vendored Smithy JSON and emits Go server stubs; first operation generated is `ListBuckets`.
- **Project phase:** **Phase 1 — Object storage (S3-source).** Phase 1 absorbs foundation work (codegen, harness, CI) alongside its first user.

## Phase 1 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **1.1** | ✅ | Repo skeleton: Go module at `github.com/e6qu/shimanism`, Makefile (lint/test/build/vet), Go CI lane, placeholder `cmd/shim/main.go`. PR #3, merged at `48c0edf`. |
| **1.2** | ✅ | Spec ingestion + engineering hygiene: S3 Smithy JSON vendored + license policy + Renovate + supply-chain hardening + version bumps. PR #4, merged at `98e6ce9`. |
| **1.3** | ◐ | Codegen for the full AWS S3 surface (all **107 operations**, no deferrals). Handles every shape kind S3 uses (structure, list, map, union, enum, intEnum, all primitives, the `smithy.api#Unit` sentinel), every HTTP binding (httpQuery, httpHeader, httpLabel, httpPayload, httpPrefixHeaders), every XML trait (xmlName, xmlFlattened, xmlAttribute, xmlNamespace), required, timestampFormat, and error responses. `internal/restxml` provides hand-written runtime helpers (URI template match, scalar/time parsing, error envelope) the generated code calls into. `make codegen` regenerates from spec; determinism test asserts the committed `aws_s3.gen.go` matches re-emit byte-for-byte. PR open. |
| **1.4** | ◻ | Conformance harness skeleton in `internal/harness/`: SDK + CLI + Terraform drivers all hit an `EchoService` that returns canonical AWS S3 shape. Establishes the test contract. |
| **1.5** | ◻ | First real backend: `ListBuckets` → **MinIO**. Same protocol as S3 — control case for the plumbing. |
| **1.6** | ◻ | `ListBuckets` → **GCS**. First real cross-cloud translation. |
| **1.7** | ◻ | `ListBuckets` → **Azure Blob**. |
| **1.8** | ◻ | `PutObject` + `GetObject` (single-part) across all four backends. |
| **1.9** | ◻ | Multipart upload (`CreateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload`). |
| **1.10** | ◻ | Presigned URLs. |
| **1.11** | ◻ | Remaining bucket lifecycle: `CreateBucket`, `DeleteBucket`, `HeadBucket`, `ListObjectsV2`, `DeleteObject`, `HeadObject`, `CopyObject`. |
| **1.12** | ◻ | Phase 1 closer: full conformance lane green across all four backends; Terraform end-to-end against GCS / MinIO / Blob via `endpoints { s3 = ... }`. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- Single-branch rule per phase / sub-phase; rebase on `origin/main` before push.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk — not just at phase end.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators.
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
2. `git status` and `git log --oneline -5` — know the local state.
3. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
4. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
5. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new work.
6. Pick the next ◻ sub-task in the table above; mark ◐ when starting; include continuity-doc updates in the same PR.

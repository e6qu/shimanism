# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #5 (Phase 1.3 — codegen) at `03b0ebb` on `origin/main`, 2026-05-18.
- **Active branch:** `phase-1.4-conformance-harness` — PR #6 open. Eight commits piled on so far: (1) intersection scoping (16 ops); (2) SDK + CLI + Terraform conformance harness; (3) TF resource-lifecycle support (manifest grew to 34 ops including bucket-config probes; typed `restxml.ShimError` for backend-error → S3-status mapping); (4) Phase 1.5.0 — neutral `domain.Storage` interface + AWS S3 frontend adapter + in-mem backend refactor + streaming `httpPayload` codegen; (5) Phase 1.5.1 — MinIO backend; (6) Phase 1.5.2 — AWS S3 passthrough backend; (7) Phase 1.6 — GCS backend; (8) Phase 1.7 — Azure Blob backend. Five backends now wired into the conformance factory list (inmem, minio, aws, gcs, azureblob), each gated on its own env var so CI lights one up at a time.
- **Project phase:** **Phase 1 — Object storage (S3-source).** Phase 1.4 has the harness running real `terraform init / apply / destroy` against `resource "aws_s3_bucket"` + `resource "aws_s3_object"`. Phases 1.5–1.7 stacked on the same branch deliver four additional real backends behind the neutral `domain.Storage` interface.

## Phase 1 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **1.1** | ✅ | Repo skeleton: Go module at `github.com/e6qu/shimanism`, Makefile, Go CI lane, placeholder `cmd/shim/main.go`. PR #3, merged at `48c0edf`. |
| **1.2** | ✅ | Spec ingestion + engineering hygiene: S3 Smithy JSON vendored + license policy + Renovate + supply-chain hardening + version bumps. PR #4, merged at `98e6ce9`. |
| **1.3** | ✅ | Codegen pipeline. PR #5, merged at `03b0ebb`. |
| **1.4** | ◐ | Conformance harness + Terraform resource-lifecycle path. Manifest holds 34 ops (16 core + 18 bucket-config probes). `internal/restxml` ships URI / scalar / time / typed-error / router runtime. Generated handlers funnel backend errors through `WriteBackendError`. `services/storage/backends/inmem/` is a real in-memory backend covering all 34. Three conformance drivers (SDK / CLI / TF) run against it in CI. Open bug: [BUG-1](BUGS.md) (router x-id stripping shadows sibling-op disambiguation on object paths — shadowed for now, tracked). PR #6 open. |
| **1.5.0** | ✅ | Domain refactor. `internal/storage/domain/` introduces the neutral `Storage` interface (streaming-friendly: `io.Reader` for `httpPayload` blob inputs, `io.ReadCloser` for outputs). `internal/storage/frontends/aws_s3/` wraps `gen.AmazonS3Backend` and translates to `domain.Storage`. `services/storage/backends/inmem/` implements `domain.Storage` directly, drops the `gen.*` types. Codegen streaming changes for `httpPayload`+blob members. Conformance suite unchanged (still hits AWS frontend, in-mem backend). See [`doc/CROSS_CLOUD_ROUTING.md`](doc/CROSS_CLOUD_ROUTING.md). Piled on PR #6 at `829d360`. |
| **1.5.1** | ✅ | **MinIO backend** — `services/storage/backends/minio/` implements `domain.Storage` via `minio-go` (uses `minio.Core` for explicit multipart). Skipped in CI unless `MINIO_ENDPOINT` is set; conformance factory `minio` enabled. Piled on PR #6 at `e9ca37a`. |
| **1.5.2** | ✅ | **AWS passthrough backend** — `services/storage/backends/aws/` via `aws-sdk-go-v2/service/s3`. Skipped unless `AWS_S3_CONFORMANCE_ENDPOINT` is set or `AWS_S3_CONFORMANCE=1`; conformance factory `aws` enabled. Piled on PR #6 at `c584b7e`. |
| **1.6** | ✅ | **GCS backend** — first cross-shape translation. AWS-shaped frontend → `domain.Storage` → `cloud.google.com/go/storage` → real GCS. Multipart mapped to GCS temp-objects-and-compose under `<key>.uploads/<uploadID>/`. Skipped unless `STORAGE_EMULATOR_HOST` is set or `GCS_CONFORMANCE=1`; conformance factory `gcs` enabled. Piled on PR #6 (uncommitted as of this update — see WHAT_WE_DID.md). |
| **1.7** | ✅ | **Azure Blob backend** — `services/storage/backends/azureblob/` via `azure-sdk-for-go/sdk/storage/azblob`. Multipart mapped to native Azure block list with base64 block IDs derived from `(uploadID, partNumber)`. Skipped unless `AZURE_STORAGE_CONNECTION_STRING` is set or `AZURE_BLOB_CONFORMANCE=1`; conformance factory `azureblob` enabled. Piled on PR #6 (uncommitted as of this update — see WHAT_WE_DID.md). |
| **1.8** | ✅ | **K8s peer backend** — `cmd/shim` rewritten as a runnable service with subcommands (`shim storage -backend=<...>`); `deploy/k8s/peer/` ships a kustomization with a MinIO StatefulSet + Service and a shim Deployment + Service; `Dockerfile` builds a distroless static image consumed by the Deployment. The "leave the cloud entirely" path is now operational. Piled on PR #6. |
| **1.9** | ✅ | `CopyObject` cross-cloud nuances. Azure: poll loop now fails loud on `failed`/`aborted` status, fails on still-pending after 30s (no silent partial copies), keeps ETag + LastModified in sync via GetProperties. GCS: `Copier.Run` already handles the rewrite-token loop for >5GB internally — no change needed. Piled on PR #6. |
| **1.10** | ✅ | Multipart upload cross-cloud nuances. `domain.MultipartETag` computes the canonical S3 multipart ETag `<md5(concat(part-md5s))>-<N>` from the per-part ETags; in-mem, GCS, and Azure Blob backends now return this shape from `CompleteMultipartUpload` instead of native cloud ETags (GCS's CRC32C-derived Etag, Azure's block-blob ETag). MinIO + AWS passthrough already return the canonical shape natively. Piled on PR #6. |
| **1.11** | ✅ | Presigned URLs: `TestSDK_PresignedURL` exercises the SDK's PresignClient against the shim. The router's ForbiddenQueries does not block SigV4 query params (X-Amz-*), and the shim accepts SigV4-bearing requests without validation at this phase (validation is a future hardening step — for now the shim is a passthrough). Piled on PR #6. |
| **1.12** | ✅ | Fix [BUG-1](BUGS.md): `restxml.RouteOptions.ForbiddenQueries` + codegen emits the S3 feature-query list for the base ops + GetObjectTagging / GetObjectAcl added as object-level probes. Piled on PR #6. |
| **1.13** | ✅ | CI conformance matrix: `conformance-minio` (MinIO container), `conformance-gcs` (fake-gcs-server), `conformance-azureblob` (Azurite). Each lane runs `TestConformanceMatrix_*` (all 3 frontends × the lane's backend) with the env var its docker container exposes; the factory's other-backend skips keep each lane focused. Real-AWS lane deferred to Track A (cloud test accounts). |
| **1.14** | ✅ | **GCS frontend.** `internal/storage/frontends/gcs/` reuses `google.golang.org/api/storage/v1` raw wire types (per locked-in decision #11). Routes cover ListBuckets / GetBucket / InsertBucket / DeleteBucket / ListObjects / GetObject (metadata + media) / DeleteObject / InsertObject (simple media + multipart) / CopyObject / RewriteObject / storageLayout probe. Conformance: `cloud.google.com/go/storage` SDK (TestGCS_SDK_*), `gcloud storage` CLI bucket lifecycle (TestGCS_CLI_BucketLifecycle), `hashicorp/google` Terraform with `storage_custom_endpoint` (TestTerraform_GCS_ResourceLifecycle). gcloud cp object round-trip is skipped pending an upstream gcloud TypeError fix; SDK covers the cell. |
| **1.15** | ✅ | **Azure Blob frontend.** `internal/storage/frontends/azure_blob/` matches the shapes the Azure SDK's internal `generated/` package uses. Routes cover ListContainers / Create + Get + Delete container / ListBlobs / Put + Get + Head + Delete blob / CopyBlob via `x-ms-copy-source`. SharedKey auth verification deferred (shim accepts unsigned at this phase). Conformance: `azure-sdk-for-go/sdk/storage/azblob` SDK (TestAzureBlob_SDK_*), `az storage blob` CLI (TestAzureBlob_CLI_*, runs when `az` is on PATH). `hashicorp/azurerm` Terraform skipped with documented upstream constraint (no blob-endpoint override; derived from ARM). |
| **1.16** | ◐ | Phase 1 closer: CI green across all conformance lanes. Final docs update + PR ready to merge. |

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

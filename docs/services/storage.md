# Object storage

A user's AWS S3 / GCS / Azure Blob client talks to the shim; the bytes land on any of the destinations below (or another cloud's storage entirely, via a different backend).

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS S3 | XML (restxml) + SigV4 | Path-style addressing supported; virtual-hosted style works through the harness too. |
| GCS REST | JSON over HTTPS | Reuses `cloud.google.com/go/storage` SDK wire types. |
| Azure Blob | REST + SharedKey | Block-blob multipart via Put Block + Put Block List. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real S3 | Passthrough via `aws-sdk-go-v2/service/s3`. |
| `gcs` | Real GCS | Passthrough via `cloud.google.com/go/storage`. |
| `azure_blob` | Real Azure Blob | Passthrough via `azure-sdk-for-go/sdk/storage/azblob`. |
| `minio` | MinIO server | S3-compatible; the in-tree K8s peer. |
| `inmem` | Process-local map | Tests + local dev only. |

## Intersection contracts

- **[`services/storage/OPERATIONS.md`](../../services/storage/OPERATIONS.md)** — the 16 operations the shim covers across all backends (List/Create/Delete Buckets; List/Get/Put/Delete/Head/Copy Objects; multipart Create/Upload/Complete/Abort/List/ListParts).
- **[`services/storage/INTERSECTION.md`](../../services/storage/INTERSECTION.md)** — per-frontend op classification (real work / feature-unset / out of intersection).
- **[`services/storage/APPLY_INTERSECTION.md`](../../services/storage/APPLY_INTERSECTION.md)** — the Apply contract that Phase 10's matrix tests assert against. Bucket + Object resources; out-of-contract sub-resources (versioning, lifecycle, ACL, CORS, encryption, etc.) listed explicitly.

## Conformance

Per-frontend × per-backend conformance lives under `services/storage/conformance/`:

- **SDK** drivers (Go canonical; Python / Node added where relevant).
- **CLI** drivers (`aws`, `gcloud`, `az`).
- **Terraform** drivers (`hashicorp/aws`, `hashicorp/google`, `hashicorp/azurerm`) for both import (Phase 9) and apply (Phase 10).

Headline tests:
- `TestConformanceMatrix` — every (frontend × backend × driver) cell.
- `TestTerraform_AWSS3_Import` (Phase 9) — import a real bucket through the AWS frontend.
- `TestTerraform_AWSS3_Apply_Bucket_NoDrift` (Phase 10) — full apply → plan-no-drift → destroy.
- `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` (Phase 9.13) — read-side cross-cloud exit criterion.
- `TestCrossCloudApply_Roundtrip_StorageAWStoGCS` (Phase 10.7) — write-side cross-cloud exit criterion. Storage is the canonical proof of the migration headline.

## Known gaps

- All out-of-intersection bucket sub-resources (versioning, lifecycle, ACL, CORS, encryption, replication, object lock, etc.) return source-cloud `NotImplemented` / `400` / `OperationNotAllowed` — see APPLY_INTERSECTION.md for the full list.
- Tag-related Terraform-provider quirks documented in [BUGS.md § false positives](../../BUGS.md#false-positives) (BUG-14: hashicorp/aws records `tags = {}` after refresh regardless of HCL declaration; not a shim fidelity gap).

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Cross-cloud migration: [docs/cross-cloud-routing.md](../../docs/cross-cloud-routing.md)
- Per-service migration recipes: [services/storage/MIGRATION.md](../../services/storage/MIGRATION.md)

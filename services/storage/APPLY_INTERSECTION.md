# Object storage — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against. Matrix tests do **not** drive "whatever attributes the provider tries"; they drive **only** the contract below.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md) (read-side per Phase 9). Operations classified per [`apigateway/APPLY_INTERSECTION.md`](../apigateway/APPLY_INTERSECTION.md) — same categories: **CRUD-in (real work)**, **soft-fail (cloud's "not supported")**, **out-of-scope (provider attribute not part of this contract)**.

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_s3_bucket` | S3 `CreateBucket` / `HeadBucket` / `DeleteBucket` | `CreateBucket` / `HeadBucket` / `DeleteBucket` |
| `aws_s3_object` | S3 `PutObject` / `HeadObject` / `DeleteObject` / `CopyObject` | `PutObject` / `HeadObject` / `DeleteObject` / `CopyObject` |
| `google_storage_bucket` | GCS `Buckets.insert/get/delete` | same |
| `google_storage_bucket_object` | GCS `Objects.insert/get/delete/copy` | same |
| `azurerm_storage_container` | Azure `Create Container` / `Get Container Properties` / `Delete Container` | same |
| `azurerm_storage_blob` | Azure `Put Blob` / `Get Blob Properties` / `Delete Blob` / `Copy Blob` | same |

## Apply contract — buckets

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `name` / `bucket` | ✅ | All backends honor. Globally-unique constraint (S3 / GCS) surfaced via the backend's native conflict error. |
| `region` / `location` | ✅ | S3 → `LocationConstraint`. GCS → `location`. Azure → storage account region (inherited; not container-scoped). |
| `force_destroy` (provider-side) | ⚠ | Provider-managed: if true, provider issues bulk DeleteObject before DeleteBucket. The shim sees individual deletes; honors them. |
| `acl`, `cors_rule`, `lifecycle_rule`, `logging`, `replication_configuration`, `server_side_encryption_configuration`, `versioning`, `website`, `object_lock_configuration`, `request_payer`, `policy`, `accelerate_configuration` | ◇ | **Out of intersection** — handled by separate resources (`aws_s3_bucket_versioning`, etc.) that fall outside Phase 10 scope. Shim returns the source cloud's native "not supported" envelope (S3 → `NotImplemented` 501; GCS → `400 BAD_REQUEST` with reason; Azure → `OperationNotAllowed`) when the provider tries to configure them. |

### Update

Bucket-level Update is **effectively no-op across the intersection**:

- `name` / `bucket` — immutable everywhere. Provider marks `ForceNew`.
- `region` / `location` — immutable everywhere. Provider marks `ForceNew`.

For attributes the provider plans as in-place update (e.g. tags), see "Out of contract" below.

### Delete

| Backend | Honest semantics |
|---|---|
| AWS S3 | `DeleteBucket` requires empty bucket; non-empty → `BucketNotEmpty` 409. `force_destroy=true` is provider-side bulk-delete first. |
| GCS | Same: bucket must be empty; non-empty → `409 conflict`. |
| Azure | `Delete Container` removes the container; child blobs are deleted. (Different from S3 in semantics; provider-side `force_destroy` is irrelevant here.) |
| MinIO | S3-compatible; same as AWS. |

**Cross-cloud divergence:** Azure deletes children; AWS / GCS / MinIO require empty-first. This is a *semantic asymmetry the shim cannot smooth honestly* — if a user writes AWS-shape HCL against an Azure-backed bucket, `terraform apply` against the shim sees Azure's "non-empty container deletes successfully" while AWS-shape providers don't expect that behavior. **Contract decision:** the shim follows the *backend's* delete semantics; user takes the asymmetry on. If this proves load-bearing in Track A real-cloud testing we revisit (likely by requiring empty-first at the shim level when frontend is AWS).

## Apply contract — objects

### Create (`PutObject` / `Objects.insert` / `Put Blob`)

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `bucket`, `key` | ✅ | All backends. |
| Body (`content`, `source`, `content_base64`) | ✅ | Streamed through the domain `PutObject`. |
| `content_type` | ✅ | All backends round-trip. |
| `metadata` (`map[string]string`) | ✅ | S3 `x-amz-meta-*`, GCS `metadata`, Azure `x-ms-meta-*`. Round-trips through domain `Metadata`. |
| `etag` (output-only) | ✅ | Backend returns its native ETag. Multipart ETag canonicalization via `domain.MultipartETag`. |
| `cache_control`, `content_disposition`, `content_encoding`, `content_language` | ◇ | Not in `domain.PutObjectOptions`. **Out of contract** until added uniformly. |
| `acl`, `server_side_encryption`, `sse_kms_key_id`, `storage_class`, `object_lock_*` | ◇ | Per-cloud variance too high for honest cross-cloud translation. Out of contract; shim returns source cloud's `NotImplemented` / `400 BAD_REQUEST` / `OperationNotAllowed` envelope. |
| `tags` / `labels` | ◇ | **Out of contract for Phase 10.** Object tagging is in `INTERSECTION.md` as category-2 honest-empty (probes pass with no tags); Apply-side tag *write* requires domain extension and is deferred. |

### Update

Object payload **cannot** be updated in place across the intersection. Provider issues:

- **PutObject again** (same key) — semantically replaces. Honest across all backends; shim's `PutObject` overwrites.
- **CopyObject with `MetadataDirective=REPLACE`** — metadata-only update. Honest across S3 / GCS / Azure (Azure via `Set Blob Metadata`, mapped through `CopyObject` shim translation per existing storage backends).

ForceNew: `bucket`, `key` (changing either replaces the resource everywhere).

### Delete (`DeleteObject` / `Objects.delete` / `Delete Blob`)

Synchronous across the intersection. No async polling. `NoSuchKey` / `NoSuchObject` / `BlobNotFound` envelopes preserved per frontend.

## Soft-delete (preview — full contract in 10.4)

Soft-delete (versioning, recoverable delete) is an **opt-in intersection feature for Phase 10**, not a default. The user must declare a retention window in source-cloud HCL; without it, the shim hard-deletes and Terraform's destroy completes synchronously.

| Backend | First-class soft-delete primitive | In-contract for 10.4? |
|---|---|---|
| AWS S3 | Bucket versioning (`aws_s3_bucket_versioning.versioning_configuration.status = Enabled`) | ✅ |
| GCS | Object versioning on the bucket | ✅ |
| Azure Blob | Account-level + container-level soft-delete | ✅ |
| MinIO | S3-compatible versioning | ✅ |

All four cells have a real primitive, so storage's soft-delete intersection is the *non-trivial* row (compare to secrets, where Vault has no soft-delete and the row narrows). Detail in 10.4.

## Out of contract (provider attributes the shim does *not* claim honest semantics for)

When the Terraform provider plans changes to these and Apply hits the shim, the shim returns the source cloud's *real* "not supported" envelope (per 10.2-C). **The shim never fabricates success on these.**

- All separate sub-resources: `aws_s3_bucket_versioning` (except via 10.4), `aws_s3_bucket_acl`, `aws_s3_bucket_lifecycle_configuration`, `aws_s3_bucket_logging`, `aws_s3_bucket_policy`, `aws_s3_bucket_replication_configuration`, `aws_s3_bucket_server_side_encryption_configuration`, `aws_s3_bucket_cors_configuration`, `aws_s3_bucket_website_configuration`, `aws_s3_bucket_request_payment_configuration`, `aws_s3_bucket_intelligent_tiering_configuration`, `aws_s3_bucket_inventory`, `aws_s3_bucket_metric`, `aws_s3_bucket_object_lock_configuration`, `aws_s3_bucket_notification`, `aws_s3_bucket_ownership_controls`, `aws_s3_bucket_public_access_block`, `aws_s3_bucket_accelerate_configuration`.
- GCS equivalents: `google_storage_bucket_iam_*`, lifecycle, retention, encryption.
- Azure equivalents: container-level immutability policies, legal holds.

**Test fixture rule** (10.0-A): every Phase 10 apply-test HCL keeps these out — the matrix only exercises in-contract attributes. Out-of-contract behavior is validated separately by the 10.2-C invalid-input fidelity tests.

## What this contract commits the shim to

For every (source cloud A × backend B) cell in the Phase 10 storage Apply matrix, the shim must:

1. Accept the in-contract Create attributes; round-trip them through Read with zero drift.
2. Reject out-of-contract attributes with the source cloud's real error envelope (not 200 OK with silent drop; not a generic 500).
3. Honor Update only for attributes the provider plans as in-place (object metadata, replacement-via-PutObject); surface "operation not supported in update" envelopes for anything else that hits this surface.
4. Honor Delete with the backend's native semantics; document the AWS-vs-Azure non-empty asymmetry as a known intersection edge.
5. Soft-delete: opt-in only; full contract in 10.4.

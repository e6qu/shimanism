# services/storage — supported operations

shimanism's storage shim covers the **intersection** of operations that exist semantically in AWS S3, GCS, Azure Blob Storage, and MinIO. The intersection is what we can honestly translate across; AWS-only operations (S3 Object Lambda, Storage Lens, SelectObjectContent, Outposts management, etc.) are not in the shim at all because they have nowhere to translate *to*. This is the PHILOSOPHY.md `Circle` koan, enforced.

The canonical wire format is **AWS S3 REST-XML** — the codegen front door speaks S3 so the AWS SDK, AWS CLI, and `hashicorp/aws` Terraform provider work unchanged via endpoint override. Phase 9 adds the equivalent GCS-shaped front door; Phase 10 adds the Azure Blob-shaped front door.

## The 16

| # | Operation | S3 | GCS | Azure Blob | MinIO |
|---|---|---|---|---|---|
| 1 | List bucket-level resources | `ListBuckets` | `Buckets.list` | List Containers | S3-compatible: `ListBuckets` |
| 2 | Create bucket | `CreateBucket` | `Buckets.insert` | Create Container | `CreateBucket` |
| 3 | Delete bucket | `DeleteBucket` | `Buckets.delete` | Delete Container | `DeleteBucket` |
| 4 | Check bucket existence | `HeadBucket` | `Buckets.get` (with HEAD-like minimal projection) | Get Container Properties | `HeadBucket` |
| 5 | List objects in a bucket | `ListObjectsV2` | `Objects.list` | List Blobs | `ListObjectsV2` |
| 6 | Get object body + metadata | `GetObject` | `Objects.get` (alt=media) | Get Blob | `GetObject` |
| 7 | Upload object | `PutObject` | `Objects.insert` | Put Blob (block / single-shot) | `PutObject` |
| 8 | Delete object | `DeleteObject` | `Objects.delete` | Delete Blob | `DeleteObject` |
| 9 | Get object metadata | `HeadObject` | `Objects.get` (no body) | Get Blob Properties | `HeadObject` |
| 10 | Server-side copy | `CopyObject` | `Objects.copy` / `Objects.rewrite` | Copy Blob | `CopyObject` |
| 11 | Initiate multipart upload | `CreateMultipartUpload` | `Objects.insert` (resumable session start) | Put Block (with block-id pattern) | `CreateMultipartUpload` |
| 12 | Upload part | `UploadPart` | `Objects.insert` (resumable chunk) | Put Block | `UploadPart` |
| 13 | Finalize multipart | `CompleteMultipartUpload` | (close resumable session) | Put Block List | `CompleteMultipartUpload` |
| 14 | Abort multipart | `AbortMultipartUpload` | (cancel resumable session) | (drop block list, blocks expire) | `AbortMultipartUpload` |
| 15 | List in-progress multiparts | `ListMultipartUploads` | (list resumable sessions) | (uncommitted blob list) | `ListMultipartUploads` |
| 16 | List parts of a multipart | `ListParts` | (resumable session status) | Get Block List | `ListParts` |

## Notes on cross-cloud fidelity

- **GCS multipart**: GCS uses *resumable upload sessions* rather than independent part numbers. The shim's S3→GCS adapter maps part numbers to byte offsets within the session. Fidelity tradeoff: the shim accepts the S3 contract but the GCS session must be sized when initiated. Out-of-order parts work because GCS supports rewriting offsets.
- **Azure block-blob multipart**: Azure uses base64-encoded block IDs. The shim's S3→Azure adapter maps S3 part numbers to deterministic block IDs.
- **HeadBucket**: GCS doesn't have a dedicated HEAD; the shim issues a minimal `Buckets.get` and discards the body, returning only the status + headers.
- **Versioning, ACLs, lifecycle, encryption**: each cloud's representation differs materially. Out of scope for the intersection; each backend implementation may opt to honor a subset via the source-cloud's vocabulary, but the codegen does not emit handlers for cloud-specific config operations.

## Why these 16 and not more

- Anything cloud-specific (`SelectObjectContent`, `RestoreObject`, `PutBucketLifecycleConfiguration`, `GetBucketIntelligentTieringConfiguration`, …) does not translate: there is no equivalent in the other three backends.
- The 16 above cover the data-plane operations every object-storage workload uses. Bigger surfaces buy diminishing returns at significant cross-cloud-translation cost.
- Operations the spec describes but that we choose to *exclude here* are not bugs — they are intentional non-goals per [`PHILOSOPHY.md § The Circle`](../../PHILOSOPHY.md#the-circle).

## Adding an operation

1. Confirm semantic equivalence exists in S3 + GCS + Azure Blob + MinIO.
2. Update `services/storage/codegen.json` (the manifest).
3. Run `make codegen`.
4. Add the per-backend translation in `services/storage/backends/<backend>/`.
5. Add a conformance test that exercises the op via SDK + CLI + Terraform.
6. Update this file with the per-cloud equivalent row.

# Object storage — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).

## AWS S3 frontend

Generated server stubs in `services/storage/gen/`. Every op below is registered through `storagegen.RegisterAmazonS3Routes` and adapts to `domain.Storage` via `internal/storage/frontends/aws_s3/adapter.go`.

| Op | Category | Status |
|---|---|---|
| ListBuckets, CreateBucket, DeleteBucket, HeadBucket | 1 | ✅ |
| ListObjectsV2, GetObject, PutObject, DeleteObject, HeadObject, CopyObject | 1 | ✅ |
| CreateMultipartUpload, UploadPart, CompleteMultipartUpload, AbortMultipartUpload, ListMultipartUploads, ListParts | 1 | ✅ |
| GetBucketLocation, GetObjectTagging (canonical "no tags"), GetObjectAcl (canonical default ACL) | 2 — feature unset | ✅ probes wired in `probes.go` |
| Object Lock, S3 Glacier restore, Replication, Inventory, Lifecycle, Logging, CORS, Website, Encryption, Versioning, Policy, Tagging | 3 — out (vendor-specific or out of Phase 1 intersection) | ◇ |

**404 envelope** uses real S3 XML (`<Error><Code>NoSuchBucket</Code>…`). ✅

## GCS frontend

`internal/storage/frontends/gcs/`. Mirrors the JSON API surface of `storage.googleapis.com`.

| Op | Category | Status |
|---|---|---|
| Objects.{insert,get,delete,list,patch,copy} | 1 | ✅ |
| Objects.compose (multipart-analog) | 1 | ✅ |
| Buckets.{insert,get,delete,list} | 1 | ✅ |
| Objects.resumable upload session | 1 | ✅ |
| Bucket IAM, requester-pays, lifecycle, retention, versioning, encryption | 3 — out | ◇ |

## Azure Blob frontend

`internal/storage/frontends/azure_blob/`. Mirrors the `<account>.blob.core.windows.net` REST surface.

| Op | Category | Status |
|---|---|---|
| Put Blob, Get Blob, Delete Blob, List Blobs, Set/Get Blob Metadata | 1 | ✅ |
| Block-blob ops (Put Block, Put Block List, Get Block List) | 1 | ✅ |
| Create/Delete/List Container, Set Container Metadata | 1 | ✅ |
| Append blob, page blob, immutable storage, lifecycle policies, soft-delete versioning | 3 — out | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS S3 op(s) | GCS op(s) | Azure op(s) | MinIO peer | Status |
|---|---|---|---|---|---|
| List my namespaces | ListBuckets | Buckets.list | List Containers | MinIO ListBuckets | ✅ |
| Create namespace | CreateBucket | Buckets.insert | Create Container | MinIO MakeBucket | ✅ |
| Drop namespace | DeleteBucket | Buckets.delete | Delete Container | MinIO RemoveBucket | ✅ |
| List objects | ListObjectsV2 | Objects.list | List Blobs | MinIO ListObjects | ✅ |
| Read object | GetObject | Objects.get | Get Blob | MinIO GetObject | ✅ |
| Write object | PutObject (or multipart) | Objects.insert (or resumable) | Put Blob (or block-blob) | MinIO PutObject | ✅ |
| Delete object | DeleteObject | Objects.delete | Delete Blob | MinIO RemoveObject | ✅ |
| Copy object | CopyObject | Objects.copy | Copy Blob | MinIO CopyObject | ✅ |
| Existence probe | HeadObject | Objects.get | Head Blob | (via stat) | ✅ |

No gaps in the migration-critical set. All 9 ops above work for every (frontend → backend) cell.

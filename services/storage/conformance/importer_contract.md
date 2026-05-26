# Storage importer-read contract

> Phase 9 sub-phase 9.2. Captured from a `TF_LOG=DEBUG terraform import` run against the shim's AWS S3 frontend with the inmem backend. Subset of ops the hashicorp/aws `aws_s3_bucket` importer's Read path invokes for a single bucket import.

## aws_s3_bucket import — observed wire ops

Each row is one HTTP request the official provider issues. The shim must answer each honestly (category 1, 2, or 3 per [`services/apigateway/INTERSECTION.md`](../../apigateway/INTERSECTION.md) taxonomy).

| HTTP method + path | Category | Expected response | Shim behaviour |
|---|---|---|---|
| `HEAD /shim-imported-bucket` | 1 | `200 OK` if bucket exists | ✅ real backend HeadBucket |
| `GET /shim-imported-bucket?policy=` | 2 | `404 NoSuchBucketPolicy` | ✅ probe (canonical unset) |
| `GET /shim-imported-bucket?acl=` | 1 | canonical default-owner ACL XML | ✅ probe (canonical default) |
| `GET /shim-imported-bucket?cors=` | 2 | `404 NoSuchCORSConfiguration` | ✅ probe |
| `GET /shim-imported-bucket?website=` | 2 | `404 NoSuchWebsiteConfiguration` | ✅ probe |
| `GET /shim-imported-bucket?versioning=` | 2 | `200` with `<VersioningConfiguration/>` (empty = unset) | ✅ probe |
| `GET /shim-imported-bucket?accelerate=` | 2 | `200` with empty acceleration | ✅ probe |
| `GET /shim-imported-bucket?requestPayment=` | 2 | `200` with `BucketOwner` (default) | ✅ probe |
| `GET /shim-imported-bucket?logging=` | 2 | `200` with empty logging | ✅ probe |
| `GET /shim-imported-bucket?lifecycle=` | 2 | `404 NoSuchLifecycleConfiguration` | ✅ probe |
| `GET /shim-imported-bucket?replication=` | 2 | `404 ReplicationConfigurationNotFoundError` | ✅ probe |
| `GET /shim-imported-bucket?encryption=` | 2 | `404 ServerSideEncryptionConfigurationNotFoundError` | ✅ probe |
| `GET /shim-imported-bucket?object-lock=` | 2 | `404 ObjectLockConfigurationNotFoundError` | ✅ probe |
| `GET /shim-imported-bucket?tagging=` | 2 | `404 NoSuchTagSet` | ✅ probe (the original BUG-1 fix landed this) |

**Result:** `terraform import aws_s3_bucket.imported shim-imported-bucket` succeeds. Subsequent `terraform plan -detailed-exitcode` returns 0 (no diff) for the in-config attribute set. The `aws_s3_bucket` provider stores many computed-only attributes (ARN, hosted_zone_id, region) in the state which the shim populates from real backend reads.

## Cross-cloud variation

The same import scenario against the GCS or Azure backends drives the same shim frontend (`aws_s3`), so the same wire ops are issued. The backend variation lives below `domain.Storage`. Matrix coverage in [`backends_test.go`](backends_test.go) (the `TestConformanceMatrix_*Frontend` tests) verifies the basic CRUD; this importer contract is the AWS-frontend-specific Read-path enumeration.

## How to regenerate this contract

1. Stand up the shim with `harness.StartStorageServer(t, inmem.New())`.
2. Pre-seed a bucket directly in the backend.
3. Write a minimal `aws_s3_bucket "x"` resource pointing at the shim.
4. Run `terraform import aws_s3_bucket.x x` with `TF_LOG=DEBUG`.
5. Filter the DEBUG log for `aws/v1.Send` lines; each is one wire op.
6. Update this table when the provider's Read path changes.

The `TestTerraform_AWSS3_Import` test in [`terraform_import_test.go`](terraform_import_test.go) runs steps 1–4 in CI; if the provider's Read path grows a new op the shim doesn't answer, the import either fails or the subsequent `terraform plan` reports a diff. Both surface as test failures.

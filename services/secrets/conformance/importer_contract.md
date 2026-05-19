# Secrets importer-read contract

> Phase 9 sub-phase 9.2 — captured from a `TF_LOG=DEBUG terraform import aws_secretsmanager_secret` run against the shim's AWS Secrets Manager frontend with the inmem backend.

## aws_secretsmanager_secret import — observed wire ops

awsJson1_1 protocol — every request goes to `POST /` with an `X-Amz-Target` header identifying the operation. The shim dispatches on the header.

| `X-Amz-Target` | Op | Category | Status |
|---|---|---|---|
| `secretsmanager.DescribeSecret` | Read main attributes | 1 | ✅ |
| `secretsmanager.GetResourcePolicy` | Read resource policy | 2 (typically unset) | ✅ |
| `secretsmanager.ListSecretVersionIds` | Read version-stage labels | 1 | ✅ |
| `secretsmanager.ListTagsForResource` | Read tags | 1 | ✅ |

**Result:** `terraform import aws_secretsmanager_secret.imported shim-imported-secret` succeeds. `terraform plan -detailed-exitcode` returns 0.

Coverage: the existing Phase 2 intersection covers the secrets-import path without gaps. No category-2 envelope was needed beyond the standard awsJson1_1 error shape; the inmem backend's tag/version semantics round-trip cleanly through DescribeSecret.

## Cross-cloud variation

The same import driven by the GCP frontend (`gcloud secrets describe`) or the Azure frontend (`az keyvault secret show`) exercises the per-cloud Read paths against the same `domain.Secrets` shape; matrix coverage in [`matrix_test.go`](matrix_test.go) verifies the cross-frontend round-trip. This importer contract is the AWS-frontend-specific enumeration.

## How to regenerate

Run `TestTerraform_AWSSecrets_Import` with `TF_LOG=DEBUG`. Filter the DEBUG log for `aws/v1.Send` lines to recover the `X-Amz-Target` sequence.

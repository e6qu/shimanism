# Secrets — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).

## AWS Secrets Manager frontend (awsJson1_1)

| Op | Category | Status |
|---|---|---|
| CreateSecret, GetSecretValue, PutSecretValue, DeleteSecret, ListSecrets, DescribeSecret, UpdateSecret | 1 | ✅ |
| ListSecretVersionIds, GetSecretVersionStage | 1 | ✅ |
| TagResource, UntagResource, ListTagsForResource | 1 | ✅ |
| RestoreSecret, RotateSecret | 3 — out (rotation is vendor-specific automation) | ◇ |
| ValidateResourcePolicy, GetResourcePolicy | 3 — out (resource policies vendor-specific) | ◇ |

## GCP Secret Manager frontend (REST JSON)

| Op | Category | Status |
|---|---|---|
| Secrets.{create,get,delete,list,patch} | 1 | ✅ |
| Versions.{add,access,destroy,disable,enable,list,get} | 1 | ✅ |
| Replication-policy field on Secret.create | 2 — feature unset (default) when not supplied | ✅ |
| IAM ops (`secrets/{n}:getIamPolicy`, `:setIamPolicy`) | 3 — out (cross-cloud IAM separate) | ◇ |

## Azure Key Vault secrets-surface frontend (REST JSON over HTTPS)

| Op | Category | Status |
|---|---|---|
| SetSecret, GetSecret, DeleteSecret, ListSecrets, ListSecretVersions | 1 | ✅ |
| UpdateSecret (rotate by setting a new value at same name; KV naturally versions) | 1 | ✅ |
| GetDeletedSecret, RecoverDeletedSecret, PurgeDeletedSecret | 2 — feature unset until soft-delete activates | ⚠ partial (soft-delete lifecycle simulated against inmem) |
| Backup/Restore (binary blob op) | 3 — out (vendor-specific format) | ◇ |
| KV's certificate / key / managed-storage-account surfaces | 3 — out (only secrets surface is Phase 2 scope) | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS op | GCP op | Azure op | Vault peer | Status |
|---|---|---|---|---|---|
| Create a secret | CreateSecret | Secrets.create + Versions.add | SetSecret | Vault KV v2 write | ✅ |
| Read latest value | GetSecretValue | Versions.access (latest) | GetSecret (no version) | Vault KV v2 read | ✅ |
| Append a new version | PutSecretValue | Versions.add | SetSecret (same name) | Vault KV v2 write | ✅ |
| List all secrets | ListSecrets | Secrets.list | ListSecrets | Vault KV list | ✅ |
| List versions of a secret | ListSecretVersionIds | Versions.list | ListSecretVersions | (via metadata) | ✅ |
| Delete (soft) | DeleteSecret | Secrets.delete | DeleteSecret | Vault delete | ✅ |
| Set tags / labels | TagResource | patch.labels | (via Set with Tags) | (kv metadata) | ✅ |

No category-1 gaps. Phase 2 closed clean.

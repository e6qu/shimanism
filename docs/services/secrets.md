# Secrets

Secret management — create / read / put-new-value / delete a named secret across cloud providers.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS Secrets Manager | awsJson1_1 (X-Amz-Target dispatch) | `CreateSecret`, `GetSecretValue`, `PutSecretValue`, `DescribeSecret`, `ListSecrets`, `UpdateSecret`, `TagResource`, `UntagResource`, `GetResourcePolicy` (canonical "no policy"). |
| GCP Secret Manager | gRPC + REST | Reuses `cloud.google.com/go/secretmanager/apiv1` types. |
| Azure Key Vault | REST + Bearer (AAD / Microsoft Entra challenge) | TLS-required for the SDK; the frontend issues the WWW-Authenticate challenge on first request. Key Vault does not use SharedKey (that's Azure Storage). Description encoded as a reserved `shim-description` tag. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS Secrets Manager | Passthrough. |
| `gcp` | Real GCP Secret Manager | Passthrough. Description encoded as `shim-description` label. |
| `azure` | Real Azure Key Vault | Passthrough. Description encoded as `shim-description` tag. |
| `vault` | HashiCorp Vault (KV v2) | The K8s peer. Description + tags stored in `custom_metadata`. |
| `inmem` | Process-local map | Tests + local dev. |

## Version mapping

AWS uses UUID `VersionId`. Azure uses GUID version handles. GCP uses monotonic ints. Vault KV v2 uses monotonic ints. The domain exposes a monotonic `uint64`. Cross-cloud, the mapping is derived per request by listing versions and sorting by creation timestamp — **no shim-side translation table** ([AGENTS.md § stateless](../../AGENTS.md#the-shim-is-stateless)).

## Intersection contracts

- **[`services/secrets/OPERATIONS.md`](../../services/secrets/OPERATIONS.md)** — the operation list.
- **[`services/secrets/INTERSECTION.md`](../../services/secrets/INTERSECTION.md)** — per-frontend op classification.
- **[`services/secrets/APPLY_INTERSECTION.md`](../../services/secrets/APPLY_INTERSECTION.md)** — Apply contract. Notes the AWS→Azure value-on-create asymmetry: Azure Key Vault genuinely requires a `Value` at `SetSecret`; AWS's separate `aws_secretsmanager_secret` + `aws_secretsmanager_secret_version` resources mean a TF apply against an Azure backend fails on the value-less Create.

## Update intersection

`domain.Secrets.UpdateSecret(name, UpdateSecretOptions{Description, Tags, Enabled})` is in-contract across backends:

- inmem patches in place.
- AWS uses `UpdateSecret` (description) + `TagResource` (tags); Enabled=false rejected as honest (Secrets Manager has no per-secret enabled flag).
- Azure uses `UpdateSecretProperties`; description rides on the reserved tag.
- GCP uses `UpdateSecret` with `FieldMask`; description rides on the reserved label. Enabled=false → `InvalidArgument`.
- Vault writes `custom_metadata`. Enabled=false → `InvalidArgument`.

See [BUGS.md § resolved](../../BUGS.md#resolved-history-compressed) entries for BUG-17 detail.

## Conformance

Headline tests:
- `TestSecretsMatrix` — every (frontend × backend × driver) cell.
- `TestTerraform_AWSSecrets_Apply_NoDrift` — Apply lifecycle through AWS frontend + inmem backend (Create → Update description → no-drift → Destroy).
- `TestCrossCloudImport_Roundtrip_SecretsAWStoAzure` (Phase 9.13) — read-side cross-cloud.
- `TestCrossCloudApply_Roundtrip_SecretsAWStoAzure` (Phase 10.7) — documented-skip with cross-cloud asymmetry explanation.

## Known gaps

- AWS→Azure value-on-create asymmetry (documented above; not a shim bug).
- Soft-delete cross-cloud intersection: opt-in only per APPLY_INTERSECTION.md. GCP has no soft-delete primitive; cells with retention windows against GCP backends return `OperationNotSupportedException`.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/secrets/MIGRATION.md](../../services/secrets/MIGRATION.md)

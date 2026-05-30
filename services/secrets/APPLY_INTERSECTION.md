# Secrets — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against. Matrix tests do **not** drive "whatever attributes the provider tries"; they drive **only** the contract below.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md). Operations classified per [`apigateway/APPLY_INTERSECTION.md`](../apigateway/APPLY_INTERSECTION.md).

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_secretsmanager_secret` | AWS `CreateSecret` / `DescribeSecret` / `UpdateSecret` / `DeleteSecret` | `CreateSecret` / `HeadSecret` / (Tags + Description Update — see below) / `DeleteSecret` |
| `aws_secretsmanager_secret_version` | AWS `PutSecretValue` / `GetSecretValue` | `PutSecretValue` / `GetSecretValue` |
| `google_secret_manager_secret` | GCP `secrets.create/get/patch/delete` | `CreateSecret` / `HeadSecret` / (limited Update) / `DeleteSecret` |
| `google_secret_manager_secret_version` | GCP `secrets.addVersion` / `versions.access/get/destroy` | `PutSecretValue` / `GetSecretValue` |
| `azurerm_key_vault_secret` | Azure KV `Set Secret` / `Get Secret` / `Update Secret` / `Delete Secret` | `CreateSecret`+`PutSecretValue` / `GetSecretValue` / (Update tags/enabled) / `DeleteSecret` |

## Apply contract — secret resource (metadata-only)

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `name` | ✅ | All backends. Path-style names where the backend permits (Azure flattens to single-segment; AWS / GCP / Vault accept `/`-segmented). |
| `description` | ✅ | Round-trips through `domain.Secret.Description`. |
| `tags` | ✅ | Round-trips through `domain.Secret.Tags`. GCP labels (key/value, lowercased) — see fidelity note. |
| `kms_key_id` / `encryption_key` / `key_vault_id` (output-only or for-the-backend) | ◇ | **Out of contract.** Per-backend encryption-at-rest config is a vendor concern; the shim doesn't translate. Backends use their account/project default. |
| `replica` (AWS multi-region) | ◇ | AWS-only feature; out of intersection. Shim returns `OperationNotSupportedException` on a non-empty replica block. |
| `force_overwrite_replica_secret` | ◇ | Same. |
| `recovery_window_in_days` (AWS, on the secret) / `soft_delete_retention_days` (Azure, account-level) | ⚠ | **See 10.4.** Opt-in only; the contract for this attribute lives in the soft-delete sub-phase. |

### Fidelity note — GCP labels

GCP allows lowercase letters, numbers, dashes, and underscores in label keys / values; values up to 63 chars. AWS tags allow ~all printable ASCII. Cross-cloud tagging through the GCP backend rejects mixed-case or special-char tags with `InvalidArgument` — this is honest, not a fidelity gap. Users targeting GCP back-ends from AWS-shape HCL must constrain tag values accordingly.

### Update

| Attribute | In-place across all backends? | Notes |
|---|---|---|
| `description` | ✅ | All four backends support description update without recreate. |
| `tags` | ✅ | All four. (Subject to GCP label constraints, above.) |
| `name` | ❌ — `ForceNew` everywhere | Immutable across the intersection. |
| `enabled` (Azure / AWS) | ✅ on AWS + Azure; ⚠ on GCP + Vault | GCP + Vault adapters always report Enabled=true and reject Enabled=false with `InvalidArgument` (per domain.go contract). Provider must not plan `enabled = false` against those backends; if it does, shim returns `InvalidArgument` with the source-cloud envelope. |
| `recovery_window_in_days` | ⚠ | See 10.4. |

Domain note: `UpdateSecret` is **not** in the `domain.Secrets` interface today. AWS frontend supports it; cross-cloud, the description+tags update goes through a separate path. **Action item flagged for 10.3 (Update intersection audit):** add `UpdateSecretMetadata(name, description, tags) error` to the domain, or document the existing fan-out path explicitly.

### Delete

`DeleteSecret(ctx, name, force bool)`:

- **force=true** — hard delete. All four backends.
- **force=false** — soft delete *where supported*. The four-cell soft-delete matrix:

| Backend | Soft-delete primitive | Default with force=false |
|---|---|---|
| AWS Secrets Manager | recovery window (7-30 days, default 30) | recovery window honored |
| Azure Key Vault | account-level + per-secret soft-delete | honored |
| GCP Secret Manager | none — no soft-delete primitive | hard delete (per domain comment); **shim returns the source cloud's `OperationNotSupported` envelope when frontend HCL declares a retention window against this backend** (10.4) |
| Vault (KV v2) | versioned delete (destroys are reversible until purge) | versioned delete; not equivalent to AWS/Azure retention windows |

**Apply implications:**
- AWS HCL with `recovery_window_in_days = 30` → AWS / Azure / Vault backends: honest. GCP backend: 10.4 contract returns `OperationNotSupportedException`.
- AWS HCL with `recovery_window_in_days = 0` → all backends hard-delete (as Phase 9.5 already did).
- Azure HCL implicitly soft-deletes (account-level) → AWS / Azure honest; GCP returns `OperationNotSupported`; Vault uses versioned-destroy as the closest analogue (documented in 10.4 as a *fidelity tradeoff*, not a fake).

## Apply contract — secret version resource

### Create (`PutSecretValue`)

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `secret_id` / `secret` (parent reference) | ✅ | All backends. |
| `secret_string` / `secret_data` | ✅ | All backends. UTF-8 string body. |
| `secret_binary` | ⚠ | AWS supports. Azure encodes via `shim-binary=true` tag (existing convention). GCP supports. Vault round-trips through canonical JSON (existing convention). **In-contract**, with the per-backend encoding documented in `domain.go`. |
| `version_stages` (AWS) | ◇ | AWS-specific version-staging metadata. Out of intersection — no GCP / Azure / Vault equivalent. Shim returns `OperationNotSupportedException`. |

### Update

Secret version is **append-only across the intersection**:

- AWS `PutSecretValue` always appends a new version.
- GCP `addVersion` always appends.
- Azure `Set Secret` appends a new version of the secret value.
- Vault KV v2 appends.

Provider's "update" of `secret_string` is therefore semantically Create-a-new-version + (optionally) Destroy-the-old. Shim honors append; destroy-old is the per-backend behavior of `DeleteSecretVersion` (out of in-contract Apply scope — provider does not invoke unless explicitly configured).

ForceNew: `secret_id` (changing reparents the version) → behaves as new resource.

### Delete

`DeleteSecretVersion` is not in `domain.Secrets`. Provider-issued destroy of a single version is **out of contract** for Phase 10. The intersection only supports deleting the *entire* secret (via `DeleteSecret`). HCL that declares a versioned destroy hits Apply-time `OperationNotSupportedException`.

## Out of contract

When the Terraform provider plans changes to these and Apply hits the shim, the shim returns the source cloud's *real* "not supported" envelope (per 10.2-C).

- AWS `aws_secretsmanager_secret_rotation` (rotation Lambda, schedule).
- AWS `aws_secretsmanager_secret_policy` (resource-based IAM policy).
- GCP `google_secret_manager_secret_iam_*` (IAM bindings).
- GCP `replication`, `topics`, `expire_time`, `ttl`, `rotation` (Pub/Sub-based rotation).
- Azure `not_before_date`, `expiration_date`, `content_type` (the latter is sometimes used; see fidelity note).

### Azure `content_type` fidelity note

Azure Key Vault stores a `contentType` string on each secret version. Cross-cloud there's no equivalent. **Decision:** out of contract for Phase 10; provider HCL that sets `content_type` against an Azure backend works (Azure-to-Azure passthrough honors it); against any other backend, shim returns `OperationNotSupported`. Users wanting MIME-like discrimination cross-cloud must encode it in the value (JSON envelope).

## Cross-cloud Apply: AWS → Azure asymmetry

A real cross-cloud asymmetry: hashicorp/aws issues `CreateSecret`
first (no value), then `PutSecretValue` via the separate
`aws_secretsmanager_secret_version` resource. AWS Secrets Manager
accepts the value-less CreateSecret; Azure Key Vault's data plane
has no such operation (`SetSecret` is the only create path and
requires `Value`).

The shim's Azure backend bridges the asymmetry via **empty-placeholder
translation**: a value-less `CreateSecret` writes an empty string value
to Azure (the closest analog to "no value yet" that Azure's data plane
can represent — `SetSecret(value: "")` is valid). The secret is
immediately queryable by the source provider's stabilization poll
(`DescribeSecret`-style). The follow-up `PutSecretValue` calls Azure
`SetSecret` with the real value, which Azure stores as a new version
(version 2). End-of-Apply state matches AWS at the value layer — the
real value is the latest version. Trade-off: there's an extra
version 1 carrying the empty placeholder, observable via
`ListSecretVersions` but not via value reads.

`TestCrossCloudApply_Roundtrip_SecretsAWStoAzure` exercises this
path in-process. The sockerless variants
(`TestSockerless_E2E_AWSSecrets_Through_Shim_ApplyTF_BackendAzure`
and the GCP-source twin) exercise it end-to-end against the Azure
KV simulator.

## What this contract commits the shim to

For every (source A × backend B) cell in the Phase 10 secrets Apply matrix:

1. Accept the in-contract Create attributes; round-trip through Read with no drift.
2. Reject out-of-contract attributes with the source cloud's real error envelope.
3. Honor Update for `description` + `tags` (with the GCP-label constraint), `enabled` on AWS + Azure only.
4. Honor `force=false` Delete according to the 10.4 soft-delete contract (preview above).
5. Append-only secret versions; cross-cloud per-version destroy returns `OperationNotSupported`.

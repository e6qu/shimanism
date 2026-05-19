# Secrets — operation and feature mapping

> The intersection footprint shimanism's `secrets` service can cover, across the four backends in scope:
> **AWS Secrets Manager**, **GCP Secret Manager**, **Azure Key Vault (secrets surface)**, **HashiCorp Vault (KV v2)** as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle) for why.
>
> The shim itself is stateless — every value, version mapping, and encoding flag lives in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## The intersection — 7 operations

The set of operations every backend supports in roughly equivalent form. These are the only ops the shim implements; the AWS/GCP/Azure frontend codegen scopes its manifest to this list.

| Domain op | AWS Secrets Manager | GCP Secret Manager | Azure Key Vault | Vault (KV v2) |
|---|---|---|---|---|
| **CreateSecret**(name, value) | `CreateSecret` (carries initial value) | `CreateSecret` then `AddSecretVersion` (two calls; second carries value) | `SetSecret` (PUT — also writes value) | `kv put` against `data/<path>` (auto-creates if absent) |
| **GetSecretValue**(name, version?) | `GetSecretValue` (`VersionId` or `VersionStage`) | `AccessSecretVersion` (`versions/<N>` or `versions/latest`) | `GetSecret` (with optional version GUID) | `GET data/<path>` (optional `?version=<N>`) |
| **PutSecretValue**(name, value) | `PutSecretValue` (creates new version, optionally moves `AWSCURRENT`) | `AddSecretVersion` | `SetSecret` (always creates new version) | `kv put` (auto-bumps version) |
| **DeleteSecret**(name, force) | `DeleteSecret` (soft-delete with recovery window; `ForceDeleteWithoutRecovery` skips it) | `DeleteSecret` (hard) | `StartDeleteSecret` (soft) + `PurgeDeletedSecret` for force | `kv metadata delete` (hard) |
| **HeadSecret**(name) | `DescribeSecret` | `GetSecret` (metadata-only; no value field) | `GetSecret` with no version + `--query "[?type=='attributes']"` | `kv metadata get` |
| **ListSecrets**(prefix?) | `ListSecrets` (filter on `Name`) | `ListSecrets` | `GetSecrets` (paged) | `kv list` |
| **ListVersions**(name) | `ListSecretVersionIds` | `ListSecretVersions` | `GetSecretVersions` | `kv metadata get` (returns `versions{}` map) |

A multi-call sequence in one cloud counts as a single domain op when the second call is mechanical (e.g. AWS' "Create then Put" because their `CreateSecret` already includes the value; GCP's "Create then AddVersion" because their `CreateSecret` only sets metadata). The shim's frontend adapter does whatever per-cloud orchestration is required to make the domain op atomic from the caller's perspective.

## Version semantics

The four systems model versions differently. The domain uses **monotonic uint64** as the canonical version handle. Mapping monotonic → native is **derived at request time** from data the backend already keeps (the shim is stateless per [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless)).

| Cloud | Native version handle | "Latest" alias | Stage labels | Creation timestamp available? |
|---|---|---|---|---|
| AWS Secrets Manager | UUID `VersionId` + `VersionStages[]` | `AWSCURRENT` stage | yes — multiple per version (AWSCURRENT, AWSPREVIOUS, AWSPENDING, user-defined) | yes — `CreatedDate` per version |
| GCP Secret Manager | numeric `versions/<N>` | `versions/latest` | no | yes (but `N` already monotonic) |
| Azure Key Vault | hex GUID | bare secret name returns latest | no | yes — `Created` per version |
| Vault (KV v2) | numeric `metadata.versions[i].version` | implicit on bare GET | no | yes (but version already monotonic) |

**Domain rule:** `Version` is a `uint64` that increments by 1 per `PutSecretValue` call. The first `CreateSecret` is version `1`. The mapping monotonic → native is derived per-request by listing the secret's versions and **sorting them by creation timestamp**:

- **GCP** + **Vault**: the cloud's native numbering is already monotonic ascending; the domain integer equals it directly.
- **AWS**: `ListSecretVersionIds(IncludeDeprecated=true)` → sort by `CreatedDate` → the nth entry is monotonic version `n`. `AWSCURRENT` is the stage label on the most recent live version; `AWSPREVIOUS` on the second-most-recent. Stage labels stay inside the AWS-frontend adapter — they never reach the domain.
- **Azure**: `GetSecretVersions` → sort by `Created` → the nth entry is monotonic version `n`.

This costs an extra list call per ranged read on AWS / Azure. Acceptable: in real workloads `GetSecretValue` is read-heavy on `latest`, which is one round trip on all four clouds. The extra list only happens when a caller asks for a specific historical version. Per-request caching inside a single handler invocation is fine; persistent caching (in the shim) is forbidden by the no-state rule.

`AWSCURRENT` / `AWSPREVIOUS` only exist in the AWS-frontend adapter. The domain knows nothing about stage labels.

## Value type

| Cloud | Value type | Encoding |
|---|---|---|
| AWS Secrets Manager | `SecretString` OR `SecretBinary` (mutually exclusive) | raw UTF-8 string OR base64-encoded bytes |
| GCP Secret Manager | `payload.data` (bytes) | bytes (typically UTF-8) |
| Azure Key Vault | `SecretAttributes.value` (string) | UTF-8 string only — no binary |
| Vault (KV v2) | `data: map<string, string>` | structured key-value (one secret can hold many fields) |

**Domain rule:** values are `[]byte`. Single-string secrets pass through as the bytes of that string. Vault entries with multiple fields are encoded as canonical JSON when written through the shim; when read back through a non-Vault frontend, the JSON encoding is preserved as-is (the caller is responsible for understanding the format). When Vault is the frontend, the shim accepts a JSON-encoded value and decodes it into Vault's `data` map; non-JSON values land as `{"value": "<the raw string>"}`.

The Azure-string-only constraint pushes back: Azure can't store binary secrets. The Azure backend adapter base64-encodes binary domain values when storing in Azure; on read, base64-decodes back to bytes. The encoding signal is a `shim-binary=true` **tag on the secret in Azure itself** — Azure already supports per-secret tags, so this is backend-side metadata, not shim-side state. (The no-state rule forbids the shim from owning the encoding map; storing the flag in Azure's own tag dictionary keeps it where it belongs.)

## Metadata

| Field | AWS SM | GCP SM | Azure KV | Vault KV v2 | Cross-cloud? |
|---|---|---|---|---|---|
| Name | ✓ | ✓ | ✓ | ✓ | yes |
| Description | ✓ `Description` | (no first-class — encode via label) | ✓ `tags['description']` | (no first-class — encode via `custom_metadata`) | yes (each backend stores it the cloud's way) |
| Tags / labels | ✓ `Tags []TagListEntry` | ✓ `Labels map[string]string` | ✓ `Tags map[string]string` | ✓ `custom_metadata map[string]string` | yes |
| Created at | ✓ | ✓ | ✓ `created` | ✓ `created_time` | yes |
| Updated at | ✓ `LastChangedDate` | ✓ `updateTime` | ✓ `updated` | ✓ `updated_time` | yes |
| Enabled flag | ✓ via `AWSCURRENT` stage | (no) | ✓ `enabled` bool | (no) | partial — frontend-specific |
| KMS key | ✓ `KmsKeyId` | ✓ `replication.userManaged.replicas[].customerManagedEncryption` | ✓ "key" (in Premium tier) | (out of scope — Vault encrypts at the engine level) | **out of intersection** |
| Expiry / TTL | ✓ rotation Lambda (workflow) | ✓ `ttl` / `expireTime` | ✓ `expires` (`not_before`/`not_after`) | (no) | **out of intersection** |

The domain exposes: `Name`, `Description`, `Tags map[string]string`, `CreatedAt`, `UpdatedAt`, `Version` (monotonic uint64), `Enabled bool` (default `true`; only the AWS + Azure frontends meaningfully toggle it; the GCP + Vault adapters return `true` unconditionally on read and reject `Enabled=false` writes with `InvalidArgument`).

## Soft delete / recovery

The four systems differ sharply on delete semantics.

| Cloud | Default delete | Hard delete | Recovery window |
|---|---|---|---|
| AWS Secrets Manager | soft (recovery window 7-30 days, configurable) | `ForceDeleteWithoutRecovery=true` | yes, restore via `RestoreSecret` |
| GCP Secret Manager | hard (immediate) | n/a — same as default | none |
| Azure Key Vault | soft (purge protection 7-90 days) + `StartDeleteSecret` | follow with `PurgeDeletedSecret` | yes, restore via `RecoverDeletedSecret` |
| Vault KV v2 | versioned delete (soft, can undelete versions) + `metadata delete` (hard) | `metadata delete` removes everything | yes, restore via `kv undelete` |

**Domain rule:** the domain interface exposes a single `DeleteSecret(name, force bool)` op. With `force=false` the shim invokes the cloud's soft-delete path (defaulting to the cloud's smallest legal recovery window — 7 days on AWS, n/a on GCP, 7 days on Azure, soft-by-default on Vault). With `force=true` the shim invokes the hard-delete path. `RestoreSecret` is **not** in the intersection (GCP doesn't have it); a soft-deleted secret is rehydrated via the cloud's native tooling. The shim returns `SecretBeingDeleted` (a domain error) on any operation against a soft-deleted secret.

## Out-of-intersection features

These are real per-cloud features, but they don't translate to a meaningful equivalent on the other clouds. When a request targets one of these, the shim returns the source cloud's own "not supported" error (e.g. `OperationNotSupportedException` for AWS, `INVALID_ARGUMENT` for GCP, `BadRequest`/`InvalidParameter` for Azure).

**AWS Secrets Manager only:**
- Rotation schedules + Lambda rotation hooks (`RotateSecret`, `CancelRotateSecret`)
- Cross-region replication (`ReplicateSecretToRegions`, `RemoveRegionsFromReplication`)
- KMS encryption-context overrides per secret
- VPC endpoints / private-link configuration
- `AWSPENDING` / custom stage labels (only `AWSCURRENT` + `AWSPREVIOUS` are honoured by the AWS frontend, since other backends have no notion of stages)

**GCP Secret Manager only:**
- User-managed replication policies (`replication.userManaged.replicas[]`)
- TTL-based expiration (`ttl` / `expireTime`)
- Pub/Sub topic notifications on version events
- ETag-based conditional updates
- Annotations (separate from labels)

**Azure Key Vault only:**
- HSM-backed keys (Premium tier — and these are out of the *secrets* surface anyway; keys + certs are separate APIs)
- Soft-delete deferred purge windows beyond the canonical 7-day floor
- Managed identity integration on the resource
- `not_before` / `not_after` activation windows on a version

**Vault (KV v2) only:**
- All non-KV engines (transit, PKI, database, transform, totp, ssh, ...)
- Dynamic secrets / lease management
- Identity / token / auth-method APIs
- Policies and policy bindings
- Cross-DC replication (Enterprise)

## What's emphatically not a shim

Any **control-plane / IAM** operation belongs in a separate phase. The secrets shim covers the secret-data surface only:

- AWS resource policies (`PutResourcePolicy`, `GetResourcePolicy`, `DeleteResourcePolicy`)
- GCP IAM bindings on the secret resource (`SetIamPolicy`, `GetIamPolicy`, `TestIamPermissions`)
- Azure access policies (Key Vault permission model, RBAC role assignments)
- Vault policies and auth methods

Authorization is also not in scope at this phase — the shim accepts requests that carry the cloud's auth headers but does not validate them, mirroring Phase 1's posture. Signature/token *verification* is a future hardening step (planned post-Phase-8).

## Mapping summary

| Capability | Coverage |
|---|---|
| Single-string secret round-trip across all 4 backends | ✓ |
| Binary secret round-trip across AWS / GCP / Vault | ✓ |
| Binary secret round-trip through Azure | ✓ (via shim-managed base64 + `shim-binary=true` tag) |
| Multi-version reads (by version number or "latest") | ✓ |
| Soft delete + force delete | ✓ (each backend uses its native form) |
| Restore from soft delete | ✗ (out of intersection — GCP has no soft delete to restore from) |
| Tags / labels | ✓ |
| Description | ✓ |
| Enabled flag | partial (frontend-specific) |
| Rotation / lifecycle | ✗ (cloud-specific) |
| Replication / regions | ✗ (cloud-specific) |
| KMS / encryption configuration | ✗ (cloud-specific) |
| IAM / resource policies | ✗ (separate phase) |

The **7-op intersection** is the right size: small enough that every backend can implement it honestly, large enough to cover the workflows that actually use secrets in practice (write a token, read it from a worker, rotate it by writing a new version, decommission it).

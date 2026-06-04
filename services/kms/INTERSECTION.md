# Key Management Service — Intersection Registry

Phase 19. Documents the intersection operation set, the per-cloud
equivalence table, and out-of-intersection features.

The shim is stateless: key material never lives in the shim. Encrypt /
Decrypt forward to the backend, which holds the keys (the cloud's HSM in
production; the inmem backend's in-process AES-256-GCM keys in tests).
Decrypt takes no key ID — the ciphertext blob carries the key reference,
mirroring every cloud KMS's Decrypt semantics.

## Key lifecycle

| Operation | AWS KMS | GCP Cloud KMS | Azure Key Vault keys | K8s peer | Notes |
|-----------|---------|---------------|----------------------|----------|-------|
| CreateKey | `CreateKey` | `cryptoKeys.create` | `PUT /keys/{name}/create` | **NotImplemented** | symmetric ENCRYPT_DECRYPT |
| DescribeKey / Get | `DescribeKey` | `cryptoKeys.get` | `GET /keys/{name}` | **NotImplemented** | |
| ListKeys | `ListKeys` | `cryptoKeys.list` | `GET /keys` | **NotImplemented** | |
| ScheduleKeyDeletion | `ScheduleKeyDeletion` | `cryptoKeyVersions.destroy` | `DELETE /keys/{name}` | **NotImplemented** | AWS keeps key in PendingDeletion |
| CancelKeyDeletion | `CancelKeyDeletion` | `cryptoKeyVersions.restore` | recover deleted key | **NotImplemented** | |

## Crypto operations

| Operation | AWS KMS | GCP Cloud KMS | Azure Key Vault keys | K8s peer | Notes |
|-----------|---------|---------------|----------------------|----------|-------|
| Encrypt | `Encrypt` | `cryptoKeyVersions.encrypt` | `POST /keys/{name}/{ver}/encrypt` | **NotImplemented** | symmetric |
| Decrypt | `Decrypt` | `cryptoKeyVersions.decrypt` | `POST /keys/{name}/{ver}/decrypt` | **NotImplemented** | key ref in ciphertext blob |

## Rotation

| Operation | AWS KMS | GCP Cloud KMS | Azure Key Vault keys | K8s peer | Notes |
|-----------|---------|---------------|----------------------|----------|-------|
| EnableKeyRotation | `EnableKeyRotation` | `cryptoKeys.patch` (rotationPeriod) | rotation policy | **NotImplemented** | |
| DisableKeyRotation | `DisableKeyRotation` | clear rotationPeriod | clear policy | **NotImplemented** | |
| GetKeyRotationStatus | `GetKeyRotationStatus` | read rotationPeriod | read policy | **NotImplemented** | |

## Cloud-specific shapes (Phase 19.B)

- **GCP Cloud KMS** has a project/location/keyRing/cryptoKey/version hierarchy the flat domain doesn't model. A `cryptoKey` maps to a domain key; the user-chosen `cryptoKeyId` becomes the domain key ID. Decrypt addresses a specific cryptoKey (key from the URL path). Rotation is `cryptoKeys.patch` with `rotationPeriod`.
  - **keyRing container — intersection (Phase 19.D).** The "named container for keys" concept is *not* in the cross-cloud data-plane intersection: GCP has the `keyRing` (data plane); Azure's container is the **Key Vault**, an ARM *control-plane* resource (`Microsoft.KeyVault`) outside this data-plane service's scope; AWS KMS has **no** container (flat keyspace + aliases); the K8s peer has no key API. keyRing is therefore a GCP-frontend-local concept. It is **not faked**: `domain.KMS` carries `CreateKeyRing`/`GetKeyRing`, backends that can hold honest state implement them for real — the in-memory test backend-of-record (tracked in a map) and the native GCP Cloud KMS backend (real `keyRings.create`/`get`) — and backends with no honest home (AWS KMS, Azure Key Vault data plane) return `ErrNotSupported`. A `GET` on an uncreated ring is a real `404`; re-create is `409`; creating a key in a non-existent ring is `404`. Verified by `TestGCPSDK_KMS_KeyRingExistence`.
  - **cryptoKeyVersions** (Phase 19.D): the flat domain models one implicit primary version per symmetric key. `cryptoKeyVersions.list`/`get` synthesize version `1`, with `createTime`/`state` taken from the backend's real key metadata (never fabricated). `cryptoKeyVersions.destroy` maps onto `domain.ScheduleKeyDeletion`, returning the version `DESTROY_SCHEDULED` with the backend's real `destroyTime` (`Key.DeletionDate`). Required by `hashicorp/google`'s `google_kms_crypto_key` read + delete.
  - **Data integrity** (Phase 19.D): Encrypt/Decrypt honor the Cloud KMS CRC32C (Castagnoli) integrity fields — request-side `plaintext_crc32c` / `ciphertext_crc32c` / `additional_authenticated_data_crc32c` are verified (mismatch → `INVALID_ARGUMENT`), and responses carry `ciphertextCrc32c` / `plaintextCrc32c` + the `verified*Crc32c` flags. `gcloud kms encrypt`/`decrypt` reject responses lacking these. Request `bytes` are decoded tolerant of standard or URL-safe base64 (proto3 JSON). Additional authenticated data is out of intersection — a non-empty AAD is rejected (`INVALID_ARGUMENT`), never silently dropped.
- **Azure Key Vault keys** are asymmetric (RSA/EC) for standard vaults, unlike AWS/GCP symmetric. The shim's encrypt/decrypt treats the ciphertext as **opaque bytes** (the SDK round-trips it without inspecting), so the inmem backend's AES-GCM and a real vault's RSA-OAEP are interchangeable at the shim boundary. The key name is the domain key ID; byte values ride the wire as base64url (handled by the azkeys serde). Delete maps to `ScheduleKeyDeletion` (soft-delete).
- **Cross-cloud Decrypt** is why `domain.Decrypt` takes an optional keyID: AWS symmetric omits it (key ref in the ciphertext), GCP/Azure pass the key from the request path.

## Backends (Phase 19.C)

| Backend | Status | Validated by |
|---|---|---|
| inmem | full (real AES-256-GCM) | SDK/CLI/TF conformance (all three frontends) |
| AWS KMS (`services/kms/backends/aws`) | full | sockerless lane (`TestSockerless_AWSKMS_Through_Shim`) + Track A |
| GCP Cloud KMS (`services/kms/backends/gcp`) | full; ScheduleKeyDeletion/CancelKeyDeletion → NotSupported (Cloud KMS destroys versions, not keys) | Track A only — **sockerless has no Cloud KMS simulator** (filed upstream) |
| Azure Key Vault keys (`services/kms/backends/azure`) | full | sockerless KV sim (lane wiring is a 19.D follow-on) + Track A |
| K8s | out of intersection (no K8s key-crypto primitive) | n/a |

## K8s peer

Direct KMS operations have no K8s-native built-in analog — etcd encryption
providers are cluster-config, not an imperative key API; `cert-manager` and
`external-secrets` manage certificates/references, not raw key crypto. The
K8s peer returns the source cloud's `UnsupportedOperationException` for all
KMS operations.

## Out of intersection (Phase 19)

- **Sign / Verify** (asymmetric keys) — noted follow-on; key-spec surface
  diverges more across clouds (RSA / EC variants, signing algorithms).
- **GenerateDataKey / envelope encryption** — data-key wrapping; deferred.
- **GetPublicKey** — asymmetric only.
- **Key aliases** (`CreateAlias` / GCP key naming / Azure key names) —
  naming model differs; the domain addresses keys by ID.
- **Key policies / grants / IAM** — authorization is out of scope.
- **Custom key stores / external/HSM key material import** — provider-specific.
- **Multi-Region keys** (AWS) — replication has no clean cross-cloud shape.

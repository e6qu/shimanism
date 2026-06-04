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

- **GCP Cloud KMS** has a project/location/keyRing/cryptoKey/version hierarchy the flat domain doesn't model. `keyRings` are a GCP-only container with no cross-cloud analog — the frontend accepts `keyRings.create`/`get` as synthetic success (out of intersection) so clients can proceed. A `cryptoKey` maps to a domain key; the user-chosen `cryptoKeyId` becomes the domain key ID. Decrypt addresses a specific cryptoKey (key from the URL path). Rotation is `cryptoKeys.patch` with `rotationPeriod`.
- **Azure Key Vault keys** are asymmetric (RSA/EC) for standard vaults, unlike AWS/GCP symmetric. The shim's encrypt/decrypt treats the ciphertext as **opaque bytes** (the SDK round-trips it without inspecting), so the inmem backend's AES-GCM and a real vault's RSA-OAEP are interchangeable at the shim boundary. The key name is the domain key ID; byte values ride the wire as base64url (handled by the azkeys serde). Delete maps to `ScheduleKeyDeletion` (soft-delete).
- **Cross-cloud Decrypt** is why `domain.Decrypt` takes an optional keyID: AWS symmetric omits it (key ref in the ciphertext), GCP/Azure pass the key from the request path.

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

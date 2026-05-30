# Cross-cloud normalization standard

> The shim is a *protocol translator*. Where the three clouds + the K8s peer agree on shape and semantics, translation is trivial. Where they diverge, the shim publishes a **normalization rule**: a deterministic, stateless translation with documented trade-offs. This file enumerates every such rule. Rules are part of the cross-cloud contract — users see the same rule on every shim deployment, in every direction it applies.

## Rule template

Every rule below uses this shape:

- **Asymmetry** — what differs across clouds, in concrete terms.
- **Rule** — what the shim does, deterministically. Always stateless (no shim-side memory; any state lives in the destination cloud).
- **Trade-off** — the observable cost, named upfront.
- **Reference** — code location + test that exercises the rule.

If a rule can't be made deterministic + stateless, the asymmetry stays *out of intersection*. The shim returns the source cloud's "not supported" error in the source cloud's error envelope (per [AGENTS.md § Fidelity to the source cloud's API](../AGENTS.md#fidelity-to-the-source-clouds-api-is-p0)).

## Normalization rules

### N1 — Secret value-less create (AWS / GCP → Azure)

**Asymmetry.** AWS Secrets Manager and GCP Secret Manager allow creating a secret without an initial value. Their Terraform providers split into `_secret` (name-only) + `_secret_version` (value) for this reason. Azure Key Vault's data plane has no value-less create — `SetSecret` is the only create operation and it requires `value`.

**Rule.** When the source cloud creates a secret without a value, the shim writes an Azure-native **empty-string** as a placeholder. The first subsequent `PutSecretValue` calls `SetSecret` with the real value, which Azure stores as a new version (becoming the current one).

**Trade-off.** Azure ends up with an extra version-1 carrying the empty placeholder. The real value is version 2 (the latest, returned by `GetSecretValue` at stage `AWSCURRENT`). The extra version is observable via `ListSecretVersions` but not via standard value reads. Stateless: Azure holds the placeholder; the shim doesn't track pending names.

**Reference.** `services/secrets/backends/azure/azure.go::CreateSecret` (the `opt.InitialValue == nil` branch); exercised end-to-end by `TestSockerless_E2E_AWSSecrets_Through_Shim_ApplyTF_BackendAzure` + `..._GCPSecrets_..._BackendAzure` in `services/secrets/conformance/sockerless_test.go`. Filed at the user-visible cross-cloud contract layer in `services/secrets/APPLY_INTERSECTION.md`.

### N2 — Secret version identity (GUID ↔ monotonic)

**Asymmetry.** AWS Secrets Manager and Azure Key Vault identify versions by GUID. GCP Secret Manager identifies versions by monotonic integer.

**Rule.** The domain layer (`internal/secrets/domain`) uses `uint64` monotonic versions. Per-cloud backends translate:

- AWS backend: stores version-GUIDs from AWS; the domain Version is the position in the version list (1 = first).
- GCP backend: passes the monotonic value through directly.
- Azure backend: enumerates versions sorted by `CreatedOn` ascending; the domain Version is the 1-based position. Per-cloud frontends emit version IDs in the source cloud's shape:
  - AWS frontend emits deterministic GUIDs `00000000-0000-0000-0000-00000000000N`.
  - GCP frontend emits the monotonic integer directly.
  - Azure frontend emits the underlying Azure GUID.

**Trade-off.** Two SetSecret calls within the same millisecond on Azure may have identical `CreatedOn` timestamps; the sort.Stable ordering then depends on Azure's pager response order. In practice Azure's pager returns versions descending-by-time; after the shim's ascending sort, sub-millisecond ties resolve to insertion-order which usually matches creation order. Tests cover the common case where Apply has > 0 ms between operations.

**Reference.** `services/secrets/backends/azure/azure.go::listVersions`, `lookupVersion`. AWS / GCP frontends in `internal/secrets/frontends/{aws_secretsmanager,gcp_secretmanager}`.

### N3 — Tags vs labels (GCP constraint enforcement)

**Asymmetry.** AWS and Azure accept arbitrary key/value tag strings. GCP labels are constrained: lowercase alphanumeric + `_-`, 63-char max keys, 63-char max values; international characters not allowed.

**Rule.** The domain layer (`*.Tags` on every service that has them) accepts arbitrary string keys/values. The GCP-backend translator (`gcpLabels` in each service's GCP backend) passes the tags through directly; non-conforming keys are silently allowed to be rejected by the GCP API rather than being silently dropped or transformed. The user gets the GCP API's native error.

**Trade-off.** Cross-cloud Apply with tags that contain uppercase / special characters fails at the GCP backend with the source cloud's error envelope. Users who care about cross-cloud-portable tags should constrain their input to the GCP-compatible subset.

**Reference.** `services/secrets/backends/gcp/gcp.go::gcpLabels`; same pattern in storage / queue / pubsub GCP backends.

### N4 — Description encoding (AWS / Azure / GCP)

**Asymmetry.** AWS Secrets Manager and Azure Key Vault have a first-class `description` field on secrets. GCP Secret Manager doesn't — descriptions don't exist as a field.

**Rule.** When the backend is GCP, the shim encodes `description` into a reserved label `shim-description`. On read-back, the GCP backend extracts this label as the description. The reserved-label key is part of the cross-cloud contract.

**Trade-off.** Users see a `shim-description` label in raw GCP API responses (outside the shim). Users querying GCP directly will see this label.

**Reference.** `services/secrets/backends/gcp/gcp.go::CreateSecret` description-as-label encoding.

### N5 — Queue ↔ topic+subscription (GCP)

**Asymmetry.** AWS SQS and Azure Service Bus have a single "queue" abstraction: messages sent to the queue are consumed by pulling readers. GCP Pub/Sub fundamentally requires `topic + subscription` — a message can only be received by a subscription, not directly from a topic.

**Rule.** The GCP queue backend creates **one topic + one subscription per domain queue**, both named after the queue. Send → publish to the topic; Receive → pull from the subscription. Per-cloud frontends present the source-cloud's queue-shaped API; the shim manages the pair internally.

**Trade-off.** GCP-side observers see two resources (a topic + a subscription) per shim-managed queue. Cross-cloud users who care about resource counts on GCP need to account for this.

**Reference.** `services/queue/backends/gcp/gcp.go::CreateQueue` (the `topic + subscription` creation pair).

### N6 — Region / location naming (canonical strings)

**Asymmetry.** AWS uses region codes like `us-east-1`. GCP uses regions like `us-central1` (no hyphen) or zones like `us-central1-a`. Azure uses `eastus` (no separator) or `East US` (display name).

**Rule.** The domain layer treats `Region` as an opaque string. Frontends accept whatever region format the source cloud's SDK emits; backends pass through the same string to the destination cloud's SDK. The shim doesn't attempt to translate region codes — cross-cloud Apply that uses a source-cloud-specific region is the caller's responsibility (or the user adapts their Terraform).

**Trade-off.** Pure cross-cloud Apply (AWS source terraform with `region = "us-east-1"` → Azure backend) will fail with the destination cloud's region-not-found error unless the user maps regions explicitly in their Terraform.

**Reference.** No translation code; the absence of code IS the rule. Tests using cross-cloud cells use destination-compatible region names in the source HCL.

### N7 — Storage version identity (S3 GUID / GCS generation / Azure snapshot)

**Asymmetry.** AWS S3 object versions are GUIDs. GCS object generations are int64. Azure Blob versions are snapshot timestamps + optional version_ids.

**Rule.** The domain layer uses a `string` version field that round-trips opaquely. Each cloud's frontend emits the source-cloud-shaped value; each backend translates to/from the destination cloud's representation. The shim doesn't try to encode version identity bidirectionally — version IDs from one cloud don't decode against another cloud's API.

**Trade-off.** Cross-cloud version-specific reads (e.g. AWS Terraform asking for a specific S3 version_id against an Azure backend) fail with a NotFound — the version ID is meaningless on the destination cloud. Users who care about specific historical versions across migration should record the destination-cloud-shaped version in their state.

**Reference.** `services/storage/backends/{aws,gcs,azureblob}/`.

### N8 — Storage object metadata vs tags split

**Asymmetry.** All three clouds distinguish *user metadata* (key/value pairs returned on every Get) from *tags* (key/value pairs used for billing / access policies). AWS S3 has separate `x-amz-meta-*` headers + `TagSet`. GCS has metadata + labels (on the bucket only). Azure Blob has metadata + indexed blob tags.

**Rule.** The domain layer carries both `Metadata` and `Tags` maps separately. Each backend translates to its native split (or coalesces if the cloud doesn't distinguish). Where the destination cloud has fewer tag categories, the lesser-priority map gets stored as labels with a `shim-*` prefix (see N4 for the equivalent secrets pattern).

**Trade-off.** Round-trip fidelity may flatten one of the two maps into the other in cells where the destination cloud doesn't distinguish. Documented per-service.

**Reference.** `internal/storage/domain/domain.go` (Metadata + Tags fields); per-cloud backends.

### N9 — Secrets soft-delete grace period (cloud-property, not call-level)

**Asymmetry.** AWS Secrets Manager and Azure Key Vault both implement *soft delete* — a deleted secret enters a recovery window before permanent removal. The semantics + configuration differ:

- **AWS Secrets Manager:** `DeleteSecret` takes `RecoveryWindowInDays` (7–30 days, default 30). `DeleteSecret(ForceDeleteWithoutRecovery=true)` purges immediately.
- **Azure Key Vault:** soft-delete retention is a **vault-level property** (`soft_delete_retention_days`), set at vault creation and applied to every secret in the vault. `DeleteSecret` initiates the recovery window; `PurgeDeletedSecret` performs the immediate hard delete.
- **GCP Secret Manager:** no soft-delete. `DeleteSecret` is permanent.
- **Vault (KV v2):** soft-delete at the version level; the secret itself can be metadata-deleted with `force`.

**Rule.** The domain interface (`domain.Secrets.DeleteSecret(ctx, name, force bool)`) takes a **boolean force** — no per-call grace period. The two clouds' soft-delete retention is treated as a **cloud-deployment property**, not a per-call argument:

- `force=false` → invoke the destination cloud's native soft-delete (whatever its retention is). On GCP this is identical to `force=true` because no soft-delete exists.
- `force=true` → immediate hard delete. AWS sets `ForceDeleteWithoutRecovery`; Azure calls `DeleteSecret` then polls `PurgeDeletedSecret`; GCP / inmem delete directly.

**Trade-off.** The grace-period **duration** isn't portable across clouds:

- AWS users who relied on `recovery_window_in_days = 7` get whatever the Azure vault was configured with on Azure-backend deployments.
- GCP-backend deployments never have a recovery window; a deleted secret can't be undeleted via the shim.

Users who care about specific retention windows configure them at the **destination cloud** level (vault config on Azure; not configurable per-call on AWS through the shim) and document the difference for their cross-cloud Apply scenarios.

**Reference.** `services/secrets/backends/{aws,azure,gcp,inmem,vault}/{aws,azure,gcp,inmem,vault}.go::DeleteSecret`. Domain interface in `internal/secrets/domain/domain.go`.

## How rules are added

When 14.E-style cross-cloud work surfaces a new asymmetry:

1. Determine whether a deterministic + stateless translation exists. If not, declare the asymmetry out-of-intersection and let the shim return the source cloud's "not supported" error.
2. Implement the rule in the relevant backend (or domain layer if cross-cutting).
3. Add an entry to this file with all four sections (asymmetry / rule / trade-off / reference).
4. Cross-reference from the affected service's `APPLY_INTERSECTION.md`.
5. Cite the relevant test that exercises the rule end-to-end.

The contract: every cross-cloud translation rule is published, named, and exercised. No hidden translations.

## Rules under audit (open items for Phase 15)

Items still pending audit + rule documentation:

- **Queue visibility timeout vs lock duration vs ack deadline** — semantic alignment needed. AWS SQS `VisibilityTimeout` (≤ 12 h), GCP Pub/Sub `ackDeadlineSeconds` (≤ 10 min), Azure Service Bus lock duration (≤ 5 min). Per-cloud bounds differ; rule must document caller-side clamping or destination-cloud-bound surfacing.
- **RDBMS engine version naming** — AWS `postgres 16.1` vs GCP `POSTGRES_16` vs Azure `16`.
- **RDBMS connection string format** — per-cloud emission shape.
- **Cache cluster mode** — sharded vs single-node across clouds.
- **Functions runtime → container image mapping** — Lambda runtime translates to Cloud Run / Container Apps container; mapping table.
- **API Gateway stages vs configs vs products** — semantic alignment.

Each will land as a follow-on 15.A PR as the rule is audited + documented.

Closed audit items:

- ~~Soft-delete grace period~~ — covered by **N9** (cloud-deployment property, not call-level).

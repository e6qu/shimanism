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

### N10 — Queue visibility timeout vs lock duration vs ack deadline

**Asymmetry.** All three clouds have a "time before an in-flight message becomes redeliverable" concept, but the bounds + names differ wildly:

- **AWS SQS:** `VisibilityTimeout` — 0 s to **12 hours** (43 200 s). Default 30 s.
- **GCP Pub/Sub:** `ackDeadlineSeconds` on subscription — **10 s to 600 s** (10 min). Default 10 s.
- **Azure Service Bus:** `LockDuration` — 5 s to **5 minutes** (300 s), passed as ISO 8601 duration. Default 60 s.

**Rule.** The domain layer carries `VisibilityTimeoutSeconds` (int seconds). Backends:

- **AWS** backend passes the value through to the SQS `VisibilityTimeout` attribute. SQS rejects values > 43 200 with `InvalidAttributeValue`.
- **GCP** backend defaults `0 → 10` (GCP's minimum, treated as defaulting unset, not as silent mutation) and **fails** on user-set values > 600 with `domain.InvalidArgument` — surfaced through the source cloud's error envelope. The Pub/Sub topic created during the failed `CreateQueue` is rolled back so the user doesn't see a half-created resource.
- **Azure** backend formats the value as ISO 8601 and passes it to `LockDuration`. Service Bus rejects values > 300 with its own validation error.

**Trade-off.** Cross-cloud Apply with AWS-shape `VisibilityTimeout = 3600` against a GCP backend fails fast with a clear error rather than silently running at 600 s. Users adapt their config (lower the timeout to ≤ 600 s for portability) or accept that the cell is out-of-intersection at that value.

**Why not clamp?** Earlier code did clamp silently. That fits the source cloud's "always succeed" semantics on its own API but violates [PHILOSOPHY.md](../PHILOSOPHY.md) "never lie" and [AGENTS.md § Fidelity to the source cloud's API](../AGENTS.md#fidelity-to-the-source-clouds-api-is-p0): the shim must surface real cross-cloud semantic mismatches in the source cloud's error vocabulary, not silently fix them. This rule was tightened in 15.B (the N10 clamp-vs-fail decision).

**Reference.** `services/queue/backends/gcp/gcp.go::CreateQueue` + `SetQueueAttributes` (the `ack > 600` branches return `domain.InvalidArgument`).

### N11 — RDBMS engine version naming

**Asymmetry.** Each cloud has its own naming convention for database engine versions:

- **AWS RDS:** `engine = "postgres"`, `engine_version = "16.1"` (major.minor decimal).
- **GCP Cloud SQL:** `database_version = "POSTGRES_16"` (uppercase + underscore + major-only).
- **Azure Database for PostgreSQL Flexible Server:** `version = "16"` (major-only integer).

**Rule.** The domain layer (`internal/rdbms/domain`) carries `Engine` as a canonical enum (`EnginePostgres`, `EngineMySQL`, ...) and `EngineVersion` as an opaque string. Backends translate:

- **GCP** backend has an explicit `gcpEngineVersion(engine, version)` helper that adds the `POSTGRES_` / `MYSQL_` prefix if not already present. Default version if empty: `POSTGRES_15` / `MYSQL_8_0`.
- **AWS** and **Azure** backends pass the version string through to their respective API fields; the cloud's own validation rejects unsupported values.

**Trade-off.** Cross-cloud version-string portability is lossy:

- AWS-shape `engine_version = "16.1"` cross-cloud-applied against GCP becomes `POSTGRES_16.1`, which Cloud SQL rejects (GCP only accepts major-version strings like `POSTGRES_16`).
- Azure-shape `version = "16"` cross-cloud-applied against AWS works (AWS accepts `"16"` as a valid major version).
- GCP-shape `database_version = "POSTGRES_16"` cross-cloud-applied against AWS/Azure: the shim's GCP frontend translates this to domain `Engine=Postgres, EngineVersion="POSTGRES_16"` → AWS backend passes through `"POSTGRES_16"` as engine_version → RDS rejects.

For portable cross-cloud Apply, users should use **major-version-only** version strings (`"16"`) — that's the form that round-trips losslessly across all three clouds. The shim doesn't transform user-supplied minor versions to/from major-only; that would silently change semantics.

**Reference.** `services/rdbms/backends/gcp/gcp.go::gcpEngineVersion` + `domainEngineFromGCP`. AWS / Azure backends pass through directly.

### N12 — RDBMS connection identity (host + port, not connection string)

**Asymmetry.** Each cloud's RDBMS service exposes connection details differently:

- **AWS RDS:** hostname (e.g. `mydb.abc123.us-east-1.rds.amazonaws.com`) + port.
- **GCP Cloud SQL:** connection name (`project:region:instance`) used by Cloud SQL Auth Proxy, **plus** IP address(es) + port for direct connections.
- **Azure Database for PostgreSQL Flexible Server:** FQDN (`mydb.postgres.database.azure.com`) + port.

**Rule.** The domain layer carries `Host` and `Port` as separate fields. Backends extract them from the cloud-native API response (AWS `Endpoint.Address` / `Endpoint.Port`, GCP `ipAddresses[]`, Azure `fullyQualifiedDomainName`) and present them via `domain.Instance`. The shim does **not** synthesize a connection string. Each cloud's Terraform provider / SDK constructs the connection format from host + port + auth credentials in its own way.

**Trade-off.** Cross-cloud users who copy a hand-built connection string verbatim across cloud-shape providers will hit mismatches (e.g. AWS-shape state recording `host:port` and GCP-shape state expecting `project:region:instance`). The shim publishes host + port; users adapt the connection-string assembly in their downstream apps.

**Reference.** `internal/rdbms/domain/domain.go` (Host + Port fields); per-cloud backends extract from each API's native response.

### N13 — Cache node tier (opaque per-cloud)

**Asymmetry.** Cache sizing varies by cloud both in vocabulary and in shape:

- **AWS ElastiCache:** node types like `cache.t3.micro`, `cache.r6g.large`.
- **GCP Memorystore:** tier enum (`BASIC`, `STANDARD_HA`) plus `memorySizeGb`.
- **Azure Cache for Redis:** SKU family + capacity (`Basic C0`, `Standard C1`, `Premium P3`).

**Rule.** The domain layer treats `NodeType` as an opaque string. Each backend passes through to its native API field; the cloud's own validation rejects unrecognized values with its own error envelope. The shim does not attempt to map sizing across cloud schemes — sizing is too tightly coupled to per-cloud pricing, performance characteristics, and feature gating to normalise meaningfully.

**Trade-off.** Cross-cloud Apply with a hard-coded `node_type = "cache.t3.micro"` against a GCP backend fails with GCP's "invalid tier" error. Users have to adapt the size string per target.

**Reference.** `internal/cache/domain/domain.go::NodeType`; per-cloud backends pass through.

### N14 — Functions: container-image canonical form

**Asymmetry.** AWS Lambda historically supports two packaging modes: **language runtimes** (`runtime = "python3.12"`, etc.) and **container images** (`package_type = "Image"`). GCP Cloud Run is container-image-only. Azure Container Apps is container-image-only.

**Rule.** The shim's functions domain layer represents only **container images** (`domain.CreateOptions.Image` carries the image URI). Lambda backend uses `PackageType = Image` with `ImageUri`. Cloud Run + Container Apps backends use the image natively. A user who wants to run a "language runtime" Lambda must wrap their code as a container image (Lambda's `Image` package type accepts user-built or AWS-provided base images — `public.ecr.aws/lambda/python:3.12` etc.).

**Trade-off.** Lambdas using language-runtime packaging (`runtime = "python3.12"` without `image_uri`) can't be expressed in the cross-cloud domain. The shim's Lambda backend creates only `PackageType = Image` functions; users with existing language-runtime Lambdas convert them by switching to the AWS-provided runtime base images before migrating.

**Reference.** `internal/functions/domain/domain.go::CreateOptions.Image`; `services/functions/backends/{aws,azure,knative}/...`.

### N15 — API Gateway declarative-replace routing table (collapses stages / configs / products)

**Asymmetry.** Each cloud's API Gateway has a different mid-level abstraction between "the gateway resource" and "the routes":

- **AWS API Gateway:** stages (`$default`, `dev`, `prod`, …) — each stage carries its own deployment with the route set frozen at deploy time.
- **GCP API Gateway:** API configs — versioned route specs that get attached to a gateway. Deploying a new config replaces the route table atomically.
- **Azure API Management:** products + subscriptions — products group APIs for billing / access control; the route table is the union of APIs in active products.

These mechanisms exist for *different reasons* (AWS stages = environment separation, GCP configs = versioned rollback, Azure products = access control), and don't translate cleanly to each other.

**Rule.** The shim's API Gateway domain (`internal/apigateway/domain/domain.go`) collapses all three into a single abstraction: a **`Gateway` with a `Routes` slice**, mutated by the **`DeployGateway(spec)`** operation that **atomically swaps the routing table**. Each backend implements "atomically" differently — AWS by creating a new stage + deployment, GCP by creating a new config + redirect, Azure by patching the product/API associations — but the visible behaviour is consistent: all-or-nothing route swap.

This is a **deliberate flattening**, not an opaque pass-through. The shim takes a strong opinion that the user-visible cross-cloud API Gateway abstraction is just a routing table; the cloud-specific mid-tier resources are implementation detail that doesn't escape the domain layer.

**Trade-off.** Three meaningful features become out-of-intersection:

- **Environment separation via stages.** AWS users who run `dev` + `prod` stages on one Gateway resource can't express the same shape against GCP / Azure backends through the shim. Workaround: separate Gateway resources per environment.
- **Versioned rollback via GCP configs.** GCP users who roll back to a prior config via the API can't do so through the shim's domain. The shim treats every `DeployGateway` as the new ground truth.
- **Access control via APIM products.** Azure users who organise APIs into billable products lose that grouping; the shim presents a flat route table.

For these use cases users either go to the destination-cloud's native API directly (bypassing the shim) or model the access control / environment separation at a higher layer.

**Reference.** `internal/apigateway/domain/domain.go::Gateway` + `DeployGateway`. Per-cloud `DeployGateway` implementations in `services/apigateway/backends/{aws,gcp,azure}/`.

### N16 — Connection-based data-plane frontends: achievable, not yet built

**Asymmetry.** Several shimmed services have data planes that aren't HTTP — they're persistent-connection protocols with their own wire formats and state machines:

| Service | Wire protocol | Server-side Go library examples |
|---|---|---|
| Azure Service Bus | AMQP 1.0 / TLS | `github.com/Azure/go-amqp` (used by sockerless's Azure sim) |
| Redis / ElastiCache / Memorystore | RESP | several Go libraries (e.g. `tidwall/redcon`) |
| PostgreSQL / RDS / Cloud SQL | PG wire | `github.com/jackc/pgx/v5/pgproto3` (server-side framing) |
| MySQL / RDS / Cloud SQL | MySQL wire | `github.com/go-mysql-org/go-mysql/server` |
| Kafka / MSK | Kafka wire | `github.com/twmb/franz-go` server primitives |

Today the shim handles the **control plane** (admin / lifecycle / CreateInstance / CreateNamespace) for all of these, but **not the data plane**. The user's app connects directly to the destination cloud's data-plane endpoint (e.g. Redis IP:port, AMQP host:port) — the shim is bypassed for actual messages / queries / cache operations.

**Rule.** Connection-based data-plane shimming is **in-intersection by design but not built today**. Architecturally these frontends are the same shape as HTTP frontends (e.g. `azure_blob` / `azure_keyvault`): receive in the source cloud's wire protocol, translate to `domain.*` operations, let the existing backend layer dispatch to whichever destination cloud.

**The pattern is already half-shipped.** The shim's *backends* for connection-based destinations already use cloud-native client libraries — `services/queue/backends/azure/azure.go` uses `azservicebus.NewClient` (an AMQP 1.0 client), `services/secrets/backends/gcp/gcp.go` uses the gRPC `cloud.google.com/go/secretmanager/apiv1` client, etc. The shim doesn't reimplement these wire formats; it consumes them via the cloud's published Go SDK. Adding a *frontend* for a connection-based protocol is the mirror image: pick a Go server-side library for the wire format (server-mode of `go-amqp` for AMQP, `pgproto3` for PG wire, etc.), wire it up to the existing `domain.*` interface, and the rest of the stack (backend dispatch, normalization rules, conformance) composes unchanged.

The work is therefore "plug in a Go server library for the wire format" + "map decoded ops to `domain.*` calls" — **not** "implement a wire protocol from scratch."

What's blocking each protocol:
- **AMQP 1.0** — `go-amqp` supports server mode; sockerless already runs it. Effort comparable to adding an HTTP frontend.
- **RESP** — simplest of the bunch; text-based; multiple Go libs.
- **PG / MySQL wire** — protocols documented; server-side libs exist but auth flows (SCRAM, TLS, password-based) need careful matching to per-cloud expectations.
- **Kafka** — substantial state machine, but franz-go primitives carry most of it.

The shim doesn't have these frontends because the admin-plane scope has been the priority. Building any of them is a Phase 15.E (or 16) candidate, weighed against demand vs. effort.

**Trade-off (today).** Cross-cloud Apply via a shim data-plane frontend doesn't exist for any connection-based service today. Users either keep app code on the destination cloud's native protocol, or wait for the data-plane frontend to land. The Apply paths that DO compose — through the shim's backend translation, with the user's app talking the destination cloud's protocol via the connection string — are exercised by PRs #60 / #61 for Service Bus and by the existing direct-connection lanes for cache / rdbms.

**Rule when one of these frontends lands.** Same rule as HTTP frontends: receive in source-cloud shape, translate to `domain.*`, dispatch through backend translation, surface errors in the source cloud's error envelope. Cross-protocol cells (e.g. AMQP → SQS HTTP) follow the standard `domain.*` round-trip. Same-protocol same-cloud cells (e.g. AMQP source → AMQP destination at sockerless / real Azure) could be implemented as TCP-level proxies with auth-handshake interception if richer translation isn't needed — that's a per-frontend choice when one is built.

**Reference.** Sockerless's Azure SB AMQP server (`/tmp/sockerless/simulators/azure/servicebus_amqp.go`) is the concrete proof-of-concept that AMQP 1.0 in Go server-side is straightforward. The shim's existing HTTP frontends are the structural template.

### N17 — DNS zone visibility (one domain field, per-cloud dispatch)

**Asymmetry.** Each cloud splits public vs. private DNS zones differently:

- **AWS Route 53:** one resource type (`HostedZone`) with a `VPC` association list. Empty `VPC` → public; non-empty → private.
- **GCP Cloud DNS:** one resource type (`managedZone`) with a `visibility` field (`public` / `private`) + a `privateVisibilityConfig.networks[]` for the bound VPCs.
- **Azure DNS:** **two resource types** — `Microsoft.Network/dnszones` (public) and `Microsoft.Network/privateDnsZones` (private) — with different ARM paths and slightly different schemas.

`hashicorp/azurerm` mirrors Azure's split with `azurerm_dns_zone` and `azurerm_private_dns_zone` as separate Terraform resources.

**Rule.** The shim's domain layer (`internal/dns/domain`) carries `Zone.Visibility = Public | Private` as a single enum. Backends dispatch on it:

- **AWS** backend: `Visibility=Public` → `CreateHostedZone` with no `VPC`; `Visibility=Private` → `CreateHostedZone` with the supplied `VPC` associations from `CreateZoneOptions.PrivateVPCs`.
- **GCP** backend: passes `Visibility` through to `managedZones.visibility`.
- **Azure** backend: **one backend** dispatches between `Microsoft.Network/dnszones` (public) and `Microsoft.Network/privateDnsZones` (private) at call time. The backend's outbound wire calls match Azure's published two-resource split; the domain hides the split.

**Trade-off.** Azure-side observers see two resource types depending on visibility; that's part of Azure's published API and the shim doesn't try to flatten it on the wire — only at the domain layer where cross-cloud uniformity matters.

**Why one Azure backend rather than two?** Putting the split in the backend layer (one backend with `Visibility` dispatch) keeps the *normalisation rule* explicit and testable — there's one place where the cross-cloud "private vs public" semantic is decided. Two backends would fragment the rule across more code and require a higher-layer dispatch on `Visibility` anyway. Same pattern as N8 (storage metadata-vs-tags split is one backend with the split internal) and N5 (queue ↔ topic+subscription is one GCP backend dispatching on whether a queue/topic operation is involved).

**Reference.** `internal/dns/domain/domain.go::ZoneVisibility`. `services/dns/backends/inmem/inmem.go` shows the pattern for the per-cloud backends to follow. Per-cloud backends (AWS Route 53 / GCP Cloud DNS / Azure DNS + Private DNS) land in follow-on 15.D PRs.

## How rules are added

When 14.E-style cross-cloud work surfaces a new asymmetry:

1. Determine whether a deterministic + stateless translation exists. If not, declare the asymmetry out-of-intersection and let the shim return the source cloud's "not supported" error.
2. Implement the rule in the relevant backend (or domain layer if cross-cutting).
3. Add an entry to this file with all four sections (asymmetry / rule / trade-off / reference).
4. Cross-reference from the affected service's `APPLY_INTERSECTION.md`.
5. Cite the relevant test that exercises the rule end-to-end.

The contract: every cross-cloud translation rule is published, named, and exercised. No hidden translations.

## Rules under audit (open items for Phase 15)

The first-pass audit is complete: every implicit normalisation the shim implements today has a published rule (N1–N15). New asymmetries surfaced by Phase 15.C (NoSQL key-value) and 15.D (DNS) will add rules to this file as they land.

~~**Open sub-question on N10:**~~ Resolved in 15.B (the GCP queue backend now fails with `domain.InvalidArgument` on `VisibilityTimeoutSeconds > 600` instead of silently clamping). N10 above documents the final rule.

~~**Open sub-question on N13:**~~ Resolved in 15.B: **keep opaque pass-through**. Adding a normalised `small`/`medium`/`large` enum with per-cloud mapping would require maintaining three mapping tables (one per cloud) that need updating whenever a cloud changes SKUs / pricing tiers / regional availability. The ergonomic gain — letting users write `tier = "small"` portably — is real but small (sizing isn't fully portable anyway: memory, IOPS, network bandwidth, and price differ across `cache.t3.micro` / `BASIC m=1GB` / `Basic C0 250MB`). Defer building the enum until cross-cloud Apply for Cache becomes a common scenario; today's opaque pass-through honestly tells the user "your value didn't fit the destination cloud" rather than mapping to something approximate. The shim's `domain.cache.NodeType` stays an opaque string.

### Phase 15.B investigation: terraform-aws `has_secret_string_wo` drift

`TestCrossCloudApply_Roundtrip_SecretsAWStoAzure` (the in-process variant of N1's translation) is `t.Skip`'d. Root cause investigation:

- **Schema (verified via `terraform providers schema -json` on `hashicorp/aws` v5.100.0):** `aws_secretsmanager_secret_version` declares both `secret_string` (optional + sensitive, stored in state) and `secret_string_wo` (write-only) with companions `secret_string_wo_version` + `has_secret_string_wo` (computed bool).
- **Drift mechanism:** When the resource is created using the regular `secret_string` path (no `_wo`), the provider's Read function doesn't populate `has_secret_string_wo` from the cloud API. Terraform then computes the indicator as `(known after apply)` on every refresh, which surfaces as `+ has_secret_string_wo = (known after apply) # forces replacement` on the `_secret_version` resource — the resource appears to need replacement on every plan-after-apply.
- **`lifecycle.ignore_changes` doesn't work** — terraform explicitly warns "the attribute `has_secret_string_wo` is decided by the provider alone and therefore there can be no configured value to compare with. Including this attribute in ignore_changes has no effect."
- **Sockerless variants are unaffected** — they use the same HCL but pass because they check the value in the destination cloud's store directly, not via a second `terraform plan` exit-code check.
- **Shim is innocent** — the translation rule N1 round-trips the value correctly. The drift is entirely in terraform-aws's schema + Read implementation.

Workaround options considered:

1. Switch HCL to `secret_string_wo` (write-only path). Changes the test scenario; doesn't validate the regular `secret_string` round-trip that real users would write.
2. File upstream at `hashicorp/terraform-provider-aws` asking that Read populate `has_secret_string_wo` correctly. **Pending user approval per [AGENTS.md § Never file external issues without explicit approval](../AGENTS.md#never-file-external-issues-without-explicit-approval).**
3. Keep the test skipped + rely on the sockerless variants for authoritative validation. Current state.

Closed audit items:

- ~~Soft-delete grace period~~ — covered by **N9** (cloud-deployment property, not call-level).
- ~~Queue visibility timeout~~ — covered by **N10** (with an open sub-question on GCP-side clamping vs failing).
- ~~RDBMS engine version naming~~ — covered by **N11** (major-version-only is the portable form).
- ~~RDBMS connection string format~~ — covered by **N12** (host + port at domain; no shim-side connection-string synthesis).
- ~~Cache cluster mode~~ — covered by **N13** (opaque `NodeType` pass-through; sub-question resolved in 15.B in favour of keeping it opaque).
- ~~Service Bus cross-cloud via AMQP frontend~~ — covered by **N16** (deliberately out-of-intersection; AMQP listener at the shim is multi-PR Phase-16+ work, not built).
- ~~Functions runtime → container image~~ — covered by **N14** (container-image canonical form; language-runtime Lambdas are out of intersection).
- ~~API Gateway stages vs configs vs products~~ — covered by **N15** (deliberate flattening into a declarative-replace routing table; stages / configs / products are implementation detail that doesn't escape the domain).

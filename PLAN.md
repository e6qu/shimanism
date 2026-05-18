# shimanism — Roadmap

> **Goal:** Build protocol-translation shims that let unmodified cloud SDKs / CLIs / Terraform providers run against AWS, GCP, Azure, or Kubernetes-native backends — by pointing them at a shim endpoint instead of the original service.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Guiding principles

1. **Spec is the contract.** Each shimmed service has a canonical published spec (AWS Smithy, GCP protobuf, Azure OpenAPI/ARM). Server-side wire layer is generated from that spec; hand-written code is translation logic only.
2. **Fidelity to the source API.** The shim speaks the cloud's API exactly. Error shapes, response headers, status codes, async semantics — all match. Where the call can't be honored against the chosen backend, return the source cloud's own error in its own vocabulary. Never fabricate success.
3. **Real backends, not emulators.** Translated calls drive a real, comparable service: another cloud's equivalent, a Kubernetes operator, or a self-hosted system. The shim itself holds no state of record.
4. **Conformance from day one.** Every shimmed operation is exercised in the same commit by (a) the cloud's official SDK, (b) its CLI, and (c) its Terraform provider, against every backend in scope. No "land it and add tests later."
5. **Intersection only.** Shim the features common to AWS / GCP / Azure / the chosen K8s peer. Out-of-intersection features fail loud with the source cloud's own error.
6. **Kubernetes is a first-class backend.** Every service has a K8s peer on equal footing with the three clouds. If no suitable open-source peer exists, shimanism builds it.
7. **No fakes, no fallbacks, no degraded modes.** If a dependency is required, it is required.
8. **One source spec, multiple adapters.** Each service has one front-door spec per cloud-source-protocol; many backend adapters. Codegen regenerates from upstream specs; agents own translation tables.
9. **Single-branch rule.** All in-flight work for one phase on one branch; many commits, one PR. User merges.
10. **Continuity always.** STATUS / WHAT_WE_DID / DO_NEXT / BUGS update at every significant chunk.
11. **One service per phase.** Each phase ships one shimmed service end-to-end against every backend in scope, with full conformance.

## Locked-in decisions

| # | Decision | Value |
|---|----------|-------|
| 1 | Implementation language | **Go** |
| 2 | Spec sources | Pull upstream, never fork: AWS Smithy JSON from `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models`; GCP protobuf from `googleapis/googleapis`; Azure OpenAPI from `Azure/azure-rest-api-specs`. |
| 3 | Codegen | spec → typed Go server stubs (handlers, request/response types, error envelopes). Hand-written code restricted to per-operation `translate.go`. |
| 4 | Backend abstraction | Per-service `Backend` interface in domain terms. Cross-service generalization is premature. |
| 5 | Test fidelity rings | Per-PR: recorded interactions + unit. Nightly: live cloud accounts. Pre-release: vendor SDK integration suites against shim. |
| 6 | Deployment | Single Go binary + Helm chart. SaaS deferred. |
| 7 | Repo layout | Monorepo. `services/<service>/` per shim; shared `internal/codegen/`, `internal/harness/`. |
| 8 | License | AGPL-3.0. |
| 9 | Passthrough mode | Ship per service when there's a real reason (auth interception, observability injection). |
| 10 | Agent permissions for spec updates | Human-in-loop on upstream-spec change. Agents propose; humans review translation-table delta. |

## Phase structure

Each shimmed service is its own phase. Foundation work (codegen pipeline, conformance harness, CI matrix) is absorbed into **Phase 1** — built alongside its first real user (S3) rather than as standalone infrastructure with no immediate consumer.

| Block | Phases | Source protocol | Services |
|---|---|---|---|
| AWS source row | 1 – 8 | AWS SDK + CLI + Terraform | Storage · Secrets · Queue · Pub/Sub · RDBMS CP · Redis CP · Functions · Gateway |
| GCP source row | 9 (sub-phases 9.1 – 9.8) | GCP SDK + gcloud + Terraform | same 8 services |
| Azure source row | 10 (sub-phases 10.1 – 10.8) | Azure SDK + az + Terraform | same 8 services |

Within a phase, sub-phases land as separate PRs.

## Phase 1 — Object storage (S3-source)

Source: **S3** (AWS Signature V4, XML responses, multipart, presigned URLs).
Backends: S3 passthrough · MinIO · GCS · Azure Blob.

**Why first:** largest API surface; richest auth + content semantics; MinIO is a free truth oracle. Phase 1 also carries the foundation work (codegen + harness + CI matrix), so later phases are mostly translation-table additions.

### Sub-phases

| Sub | Status | Headline |
|---|---|---|
| **1.1** | ◐ | Repo skeleton: Go module, Makefile, Go CI lane. |
| **1.2** | ◻ | Spec ingestion: fetch + cache AWS Smithy JSON for S3. |
| **1.3** | ◻ | Codegen pilot: Smithy → Go server stub for one operation (`ListBuckets`). |
| **1.4** | ◻ | Conformance harness skeleton: SDK + CLI + Terraform drivers against an `EchoService` returning canonical AWS S3 shape. All three drivers pass against the no-op. |
| **1.5** | ◻ | First real backend: `ListBuckets` → MinIO. Validates plumbing end-to-end (same protocol, no translation lies — control case). |
| **1.6** | ◻ | `ListBuckets` → GCS. First real cross-cloud translation. |
| **1.7** | ◻ | `ListBuckets` → Azure Blob. |
| **1.8** | ◻ | `PutObject` + `GetObject` (single-part) across all four backends. |
| **1.9** | ◻ | Multipart upload (`CreateMultipartUpload` / `UploadPart` / `CompleteMultipartUpload`). |
| **1.10** | ◻ | Presigned URLs. |
| **1.11** | ◻ | Bucket lifecycle (`CreateBucket`, `DeleteBucket`, `HeadBucket`, `ListObjects(V2)`, `DeleteObject`, `HeadObject`, `CopyObject`). |
| **1.12** | ◻ | Phase 1 closer: full conformance lane green against all four backends; Terraform `aws_s3_bucket` + `aws_s3_object` drive every backend via `endpoints { s3 = ... }`. |

**Exit criteria:** Terraform HCL written for AWS S3 provisions and exercises a GCS-backed bucket end-to-end via `endpoints { s3 = "..." }`, with no resource churn or fabricated responses. Equivalents for MinIO and Blob.

### Architecture: cross-cloud routing

Routing between cloud A's frontend and cloud B's backend uses a **neutral domain interface** between the wire-protocol codec and the cloud-specific backend. See [`doc/CROSS_CLOUD_ROUTING.md`](doc/CROSS_CLOUD_ROUTING.md) for the architecture, terminology (frontend / backend / opposite-shape), the 3 × 4 matrix, the per-cloud library list (each backend imports the destination cloud's official Go SDK), and the streaming-throughout performance contract.

The implementation order is:

- **1.5.0** (domain refactor): introduce `internal/storage/domain/` interface; refactor `internal/storage/frontends/aws_s3/` as the wire→domain adapter; refactor `services/storage/backends/inmem/` to implement `domain.Storage` directly. Streaming-friendly: `httpPayload` blob members become `io.Reader` (in) / `io.ReadCloser` (out); object bodies never buffer.
- **1.5.1** (MinIO): first real backend on the domain interface. S3-compatible passthrough.
- **1.5.2** (AWS passthrough): `backends/aws/` for completeness.
- **1.6** (GCS): first cross-cloud translation backend.
- **1.7** (Azure Blob): same.
- **1.8** (K8s peer): MinIO-in-cluster via operator (or equivalent).

## Phase 2 — Secrets (Secrets Manager-source)

Source: AWS Secrets Manager.
Backends: Secrets Manager passthrough · Vault · GCP Secret Manager · Azure Key Vault (secrets surface only).

Simpler API; validates the platform pattern doesn't accidentally depend on object-storage specifics. Reuses codegen + harness from Phase 1; should ship in a fraction of Phase 1's time.

**Sub-phases (sketch):** spec ingest · codegen · `GetSecretValue` × 4 backends · `PutSecretValue` · `CreateSecret` · `DeleteSecret` · `ListSecrets` · version management · closer.

**Exit criteria:** `aws secretsmanager get-secret-value` and Terraform provider drive all four backends correctly.

## Phase 3 — Queue (SQS-source)

Source: AWS SQS.
Backends: SQS passthrough · NATS JetStream · GCP Pub/Sub (pull mode) · Azure Service Bus queue.

Fidelity challenges: visibility timeouts, FIFO ordering, message attributes, dead-letter queues.

**Exit criteria:** an SDK-using worker drains an SQS-shaped queue identically when the backend is each of the four.

## Phase 4 — Pub/Sub (SNS-source)

Source: AWS SNS (with SQS-shaped subscriptions for delivery).
Backends: SNS+SQS passthrough · NATS core · GCP Pub/Sub · Azure Service Bus topics.

Shares auth + messaging infrastructure with Phase 3.

**Exit criteria:** `aws sns publish` to a topic with subscriptions on every backend type fans out correctly; subscribers receive the canonical SNS message envelope.

## Phase 5 — Managed RDBMS (RDS-source, control plane only)

Source: AWS RDS (Postgres + MySQL engines).
Backends: RDS passthrough · CloudNativePG / MySQL Operator (K8s) · Cloud SQL Admin · Azure DB Admin.

**Different shape from Phases 1-4:** no data-plane proxying. Shim translates control-plane API calls (`CreateDBInstance`, snapshot, restore) and returns connection metadata. Clients connect directly to the real Postgres / MySQL via wire protocol.

**Exit criteria:** `aws rds create-db-instance --endpoint-url=...` provisions a CloudNativePG cluster in K8s; the returned connection details let `psql` connect to the real PG and run queries.

## Phase 6 — Managed Redis (ElastiCache-source, control plane only)

Source: AWS ElastiCache.
Backends: ElastiCache passthrough · Redis Operator (K8s) · GCP Memorystore Admin · Azure Cache for Redis Admin.

Same shape as Phase 5: control-plane only; data plane is wire-protocol RESP — direct client connection.

**Exit criteria:** `aws elasticache create-cache-cluster` provisions a Redis Operator instance; returned endpoint accepts a `redis-cli` connection.

## Phase 7 — Functions (Lambda-source)

Source: AWS Lambda (container image deployment path).
Backends: Lambda passthrough · Knative (K8s) · Cloud Run · Azure Container Apps.

Translation challenges: deployment metadata, event payload normalization (S3 / SQS / SNS events → canonical HTTP envelope), VPC integration.

**Exit criteria:** a SAM template deploys to Knative + Cloud Run through the shim and serves traffic; AWS Lambda Powertools sees the expected event shape.

## Phase 8 — API Gateway (API Gateway HTTP API v2-source)

Source: AWS API Gateway HTTP API v2.
Backends: API Gateway passthrough · Envoy Gateway (K8s) · GCP API Gateway · Azure API Management (Consumption tier).

Declarative-replace model: `deploy(gateway_spec)` swaps the entire routing table atomically.

**Exit criteria:** Terraform `aws_apigatewayv2_api` + routes + integrations deploy correctly through the shim to Envoy Gateway; published URL serves the configured routes.

## Phase 9 — GCP source row (horizontal expansion)

For each of the eight services, add a GCP-source adapter. Backends unchanged. Re-run conformance with `gcloud`, the GCP Go SDK, the GCP Terraform provider.

This is where the codegen pipeline pays back: each sub-phase is mostly a translation-table addition, not new architecture.

| Sub | Service | Source |
|---|---|---|
| 9.1 | Object storage | GCS |
| 9.2 | Secrets | GCP Secret Manager |
| 9.3 | Queue | GCP Pub/Sub (pull) |
| 9.4 | Pub/Sub | GCP Pub/Sub |
| 9.5 | RDBMS | Cloud SQL Admin |
| 9.6 | Redis | GCP Memorystore Admin |
| 9.7 | Functions | Cloud Run Admin |
| 9.8 | API Gateway | GCP API Gateway |

**Exit criteria:** `gcloud storage cp` with `--api-endpoint-overrides` works against an Azure-Blob-backed bucket through the shim, just as `aws s3 cp` did in Phase 1.

## Phase 10 — Azure source row (horizontal expansion)

| Sub | Service | Source |
|---|---|---|
| 10.1 | Object storage | Azure Blob |
| 10.2 | Secrets | Azure Key Vault (secrets) |
| 10.3 | Queue | Azure Service Bus queue (decide AMQP-vs-REST fidelity tier here) |
| 10.4 | Pub/Sub | Azure Service Bus topics |
| 10.5 | RDBMS | Azure Database for PG/MySQL (ARM) |
| 10.6 | Redis | Azure Cache for Redis (ARM) |
| 10.7 | Functions | Azure Container Apps / Functions (ARM) |
| 10.8 | API Gateway | Azure API Management (ARM) |

**Exit criteria:** Azure SDK + `az` CLI + AzureRM Terraform provider all drive the shim against every backend for every service.

## Open questions (decide before they block work)

- Single org-wide deployment vs per-tenant — affects auth model.
- Where do live cloud test accounts live; who pays.
- Coding-agent permissions for upstream spec-version bumps: auto-PR or human-in-loop?
- AMQP fidelity tier for Azure Service Bus (Phase 10.3 + 10.4) — REST-only initially, or AMQP from the start?

## Closed phases (PR index)

| PR | Phase | Headline |
|---|---|---|
| #1 | (bootstrap) | Repo created. Branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |
| #2 | (bootstrap) | Continuity docs (PLAN, STATUS, WHAT_WE_DID, DO_NEXT, BUGS, AGENTS, CLAUDE→AGENTS symlink) + Phase-0 CI checks (branch-rebased, symlinks-resolve, continuity-docs-present) wired into the main-branch ruleset as required status checks. Merged 2026-05-18 at `4549a90`. |

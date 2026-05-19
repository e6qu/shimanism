# shimanism — Roadmap

> **Goal:** Build protocol-translation shims that let unmodified cloud SDKs / CLIs / Terraform providers run against AWS, GCP, Azure, or Kubernetes-native backends — by pointing them at a shim endpoint instead of the original service.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Guiding principles

1. **Spec is the contract.** Each shimmed service has a canonical published spec (AWS Smithy, GCP protobuf, Azure OpenAPI/ARM). Server-side wire layer is generated from that spec; hand-written code is translation logic only.
2. **Fidelity to the source API.** The shim speaks the cloud's API exactly. Error shapes, response headers, status codes, async semantics — all match. Where the call can't be honored against the chosen backend, return the source cloud's own error in its own vocabulary. Never fabricate success.
3. **Real backends, not emulators.** Translated calls drive a real, comparable service: another cloud's equivalent, a Kubernetes operator, or a self-hosted system. The shim itself holds no state of record.
4. **Conformance from day one.** Every shimmed operation is exercised in the same commit by (a) the cloud's official SDK, (b) its CLI, and (c) its Terraform provider, against every backend in scope. No "land it and add tests later."
5. **Intersection only.** Shim the features common to AWS / GCP / Azure / the chosen K8s peer. Out-of-intersection features fail loud with the source cloud's own error.
6. **Kubernetes is a first-class backend.** Every service has a K8s peer on equal footing with the three clouds. When a suitable third-party OSS peer exists (MinIO for storage, Vault for secrets, NATS / CloudNativePG / Knative / etc.), use it. When none fits, the in-tree [`peers/shimakit/`](peers/shimakit/) framework provides the common-denominator primitives (versioned named bytes + metadata + soft-delete + list + multi-namespace) on top of which a concrete `shima<service>` peer (e.g. `shimasecret`, `shimastore`) is built — one Store interface, namespace-scoped so the same framework serves multiple shim services across deployments.
7. **No fakes, no fallbacks, no degraded modes.** If a dependency is required, it is required.
8. **One source spec, multiple adapters.** Each service has one front-door spec per cloud-source-protocol; many backend adapters. Codegen regenerates from upstream specs; agents own translation tables.
9. **Single-branch rule.** All in-flight work for one phase on one branch; many commits, one PR. User merges.
10. **Continuity always.** STATUS / WHAT_WE_DID / DO_NEXT / BUGS update at every significant chunk.
11. **One service per phase, every frontend × every backend.** Each phase ships one shimmed service end-to-end across **all three source-cloud frontends (AWS / GCP / Azure)** translating into **all four backends (AWS / GCP / Azure / K8s peer)** — the full 3 × 4 matrix. **Each frontend is tested by its own cloud's official tooling** (the AWS frontend by `aws-sdk-go-v2` + `aws` CLI + `hashicorp/aws` Terraform provider; the GCS frontend by `cloud.google.com/go/storage` + `gcloud` CLI + `hashicorp/google` provider; the Azure frontend by `azure-sdk-for-go/sdk/storage/azblob` + `az` CLI + `hashicorp/azurerm` provider) **against every backend cloud's real service.** That's 3 frontends × 3 driver types × 4 backends = **36 driver-backend combinations** per service, all green before a phase closes. No "AWS-source first, GCP-source row later." A service is not done until any cloud's tooling can drive it against any backend.

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
| 11 | **Reuse-over-reinvention** | Lean on the cloud's official spec + official Go SDK whenever they fit. Each frontend's wire layer is generated from the cloud's canonical spec (AWS Smithy → custom emitter; GCP Discovery doc → `google.golang.org/api` raw types where pragmatic; Azure OpenAPI → `oapi-codegen` or equivalent). Wire types are imported from the official SDK when they round-trip cleanly server-side; emitted from the spec when SDK types fight server-side handling. Auth verification uses the cloud's official verifier (e.g. `aws-sdk-go-v2/aws/signer/v4`). The shim's job is to **be** the cloud's API surface, not to maintain a parallel one. See [AGENTS.md § Reuse over reinvention](AGENTS.md#reuse-over-reinvention). |
| 12 | **Stateless shim** | The shim binary holds no state of record. All persistent state lives in the backend. No sidecar database, no shim-managed key/value namespace, no in-process cache that the shim treats as authoritative. Per-request scratch (computing a response by reading upstream) is fine; persisting anything across requests in the shim is not. Cross-cloud shape mappings that need a stable identifier (e.g. monotonic version index ↔ Azure GUID) are derived at request time from data the backend already keeps. A stateless shim scales horizontally, restarts without warmup, never causes split-brain. If a feature can't be implemented statelessly it's out of intersection — return the cloud's "not supported" error. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless). |
| 13 | **In-tree K8s peer when OSS doesn't fit** | The K8s-peer slot for every shimmed service prefers a third-party OSS project (MinIO for storage, Vault for secrets, NATS for queues, CloudNativePG for RDBMS, Knative for functions, Envoy Gateway for API GW). When no OSS project fits cleanly, the in-tree framework at [`peers/shimakit/`](peers/shimakit/) provides the common-denominator primitives — versioned, named binary objects with structured metadata, multi-namespace addressing, soft-delete lifecycle — and concrete peers built on top of it are named `shima<service>` (e.g. `shimasecret`, `shimastore`, `shimaqueue`). One framework, many possible concrete peers; each composes the same primitives instead of re-implementing them. Released as separate Go modules so operators can pin / deploy / upgrade each independently of the shim binary. |

## Phase structure

Each shimmed service is its own phase. Foundation work (codegen pipeline, conformance harness, CI matrix) is absorbed into **Phase 1** — built alongside its first real user (object storage) rather than as standalone infrastructure with no immediate consumer.

Per principle 11, every phase ships the full N × N matrix in one phase. There are no "source rows" of horizontal expansion later; horizontal expansion happens **inside** each phase.

| Phase | Service | Frontends (source clouds) | Backends |
|---|---|---|---|
| 1 | Object storage | AWS S3, GCS, Azure Blob | AWS S3, GCS, Azure Blob, K8s peer (MinIO) |
| 2 | Secrets | AWS Secrets Manager, GCP Secret Manager, Azure Key Vault | same four + Vault as K8s peer |
| 3 | Queue | AWS SQS, GCP Pub/Sub (pull), Azure Service Bus queues | same three clouds + NATS JetStream as K8s peer |
| 4 | Pub/Sub | AWS SNS, GCP Pub/Sub, Azure Service Bus topics | same three clouds + NATS core as K8s peer |
| 5 | Managed RDBMS (control plane) | AWS RDS, Cloud SQL Admin, Azure DB Admin | same three clouds + CloudNativePG / MySQL Operator as K8s peer |
| 6 | Managed Redis (control plane) | AWS ElastiCache, GCP Memorystore Admin, Azure Cache for Redis Admin | same three clouds + Redis Operator as K8s peer |
| 7 | Functions | AWS Lambda, GCP Cloud Run, Azure Container Apps | same three clouds + Knative as K8s peer |
| 8 | API Gateway | AWS API Gateway v2, GCP API Gateway, Azure API Management | same three clouds + Envoy Gateway as K8s peer |

Within a phase, sub-phases land as separate commits on a single branch. Codegen for each frontend's spec format (Smithy / Discovery+protobuf / OpenAPI v3) is part of the per-phase work — Phase 1 establishes the AWS Smithy pipeline; Phase 1's GCP and Azure sub-phases establish the GCP Discovery and Azure OpenAPI pipelines, which then carry forward to Phases 2-8 as reusable infrastructure.

## Phase 1 — Object storage

Frontends: **AWS S3** (SigV4, REST-XML), **GCS** (OAuth2 bearer, JSON, resumable uploads), **Azure Blob** (SharedKey / SAS, REST + XML for some paths).
Backends: AWS S3 · GCS · Azure Blob · K8s peer (MinIO-in-cluster).

**Why first:** largest API surface; richest auth + content semantics; MinIO is a free truth oracle for the K8s peer. Phase 1 also carries the foundation work — codegen for all three spec formats (Smithy 2.0, GCP Discovery, Azure OpenAPI v3), the conformance harness, the CI matrix — so Phases 2-8 are mostly translation-table additions on top of established infrastructure.

### Sub-phases

| Sub | Status | Headline |
|---|---|---|
| **1.1** | ✅ | Repo skeleton: Go module, Makefile, Go CI lane. |
| **1.2** | ✅ | Spec ingestion: fetch + cache AWS Smithy JSON for S3. |
| **1.3** | ✅ | Codegen pilot: Smithy → Go server stubs (all 107 ops; subsequently scoped down). |
| **1.4** | ✅ | Conformance harness: SDK + CLI + Terraform drivers against the shim. |
| **1.5.0** | ✅ | Domain refactor: `internal/storage/domain/` neutral interface; AWS S3 frontend adapter; streaming codegen. |
| **1.5.1** | ✅ | MinIO backend (S3-compatible control case; lives at `services/storage/backends/minio/`). |
| **1.5.2** | ✅ | AWS S3 passthrough backend. |
| **1.6** | ✅ | GCS backend — first cross-shape translation. |
| **1.7** | ✅ | Azure Blob backend. |
| **1.8** | ✅ | K8s peer: runnable `cmd/shim` + `deploy/k8s/peer/` MinIO + shim manifests + Dockerfile. |
| **1.9** | ✅ | CopyObject cross-cloud nuances (Azure fail-loud poll loop). |
| **1.10** | ✅ | Multipart ETag parity via `domain.MultipartETag`. |
| **1.11** | ✅ | Presigned URL conformance test. |
| **1.12** | ✅ | BUG-1 fix: router `ForbiddenQueries` + GetObjectTagging / GetObjectAcl object probes. |
| **1.13** | ◐ | CI conformance matrix (minio / gcs / azureblob lanes). |
| **1.14** | ◻ | **GCS frontend.** Spec ingest (Discovery doc / protobuf) → GCS-shaped server stubs → adapter wrapping `domain.Storage` → conformance via `cloud.google.com/go/storage` SDK + `gcloud` CLI + `hashicorp/google` Terraform provider against all four backends. |
| **1.15** | ◻ | **Azure Blob frontend.** Spec ingest (Azure OpenAPI v3) → Azure-Blob-shaped server stubs (XML for blob list, REST + JSON for control) → adapter wrapping `domain.Storage` → conformance via `azure-sdk-for-go/sdk/storage/azblob` SDK + `az` CLI + `hashicorp/azurerm` Terraform provider against all four backends. |
| **1.16** | ◻ | Phase 1 closer: full conformance lane green for **all 3 frontends × 4 backends × 3 driver types = 36 driver-backend combinations**. Terraform `aws_s3_bucket`, `google_storage_bucket`, `azurerm_storage_container` each provision against every backend through endpoint overrides. |

**Exit criteria.** For every (frontend, backend) pair in the 3 × 4 matrix, all three driver types are green:

| Frontend | SDK | CLI | Terraform provider |
|---|---|---|---|
| AWS S3 | `aws-sdk-go-v2/service/s3` | `aws` | `hashicorp/aws` (`aws_s3_bucket`, `aws_s3_object`) |
| GCS | `cloud.google.com/go/storage` | `gcloud storage` | `hashicorp/google` (`google_storage_bucket`, `google_storage_bucket_object`) |
| Azure Blob | `azure-sdk-for-go/sdk/storage/azblob` | `az storage blob` | `hashicorp/azurerm` (`azurerm_storage_container`, `azurerm_storage_blob`) |

Each row's drivers are configured (endpoint override / `--api-endpoint-overrides` / Terraform `endpoints { ... }`) to hit the shim. The shim is configured (`shim storage -backend=<cloud>`) to translate to each of the four backends in turn. **36 driver-backend conformance lanes total**, every one of them green, before Phase 1 closes.

### Architecture: cross-cloud routing

Routing between cloud A's frontend and cloud B's backend uses a **neutral domain interface** between the wire-protocol codec and the cloud-specific backend. See [`doc/CROSS_CLOUD_ROUTING.md`](doc/CROSS_CLOUD_ROUTING.md) for the architecture, terminology (frontend / backend / opposite-shape), the 3 × 4 matrix, the per-cloud library list (each backend imports the destination cloud's official Go SDK), and the streaming-throughout performance contract.

The implementation order is:

- **1.5.0** (domain refactor): introduce `internal/storage/domain/` interface; refactor `internal/storage/frontends/aws_s3/` as the wire→domain adapter; refactor `services/storage/backends/inmem/` to implement `domain.Storage` directly. Streaming-friendly: `httpPayload` blob members become `io.Reader` (in) / `io.ReadCloser` (out); object bodies never buffer.
- **1.5.1** (MinIO): first real backend on the domain interface. S3-compatible passthrough.
- **1.5.2** (AWS passthrough): `backends/aws/` for completeness.
- **1.6** (GCS): first cross-cloud translation backend.
- **1.7** (Azure Blob): same.
- **1.8** (K8s peer): MinIO-in-cluster via operator (or equivalent).

## Phase 2 — Secrets

Frontends: **AWS Secrets Manager**, **GCP Secret Manager**, **Azure Key Vault** (secrets surface only — Key Vault's certificate / key APIs are out of intersection).
Backends: same three clouds + **Vault** as the K8s peer.

Simpler API than storage; validates the platform pattern doesn't accidentally depend on object-storage specifics. Reuses the codegen pipelines for all three spec formats and the conformance harness from Phase 1; should ship in a fraction of Phase 1's time.

**Sub-phases (sketch per frontend × backend matrix):** Smithy / Discovery / OpenAPI ingest · codegen for the three frontends · per-cloud auth wiring · `GetSecretValue` × 4 backends · `PutSecretValue` · `CreateSecret` · `DeleteSecret` · `ListSecrets` · version management · K8s peer (Vault deployment manifests) · closer.

**Exit criteria:** any of `aws secretsmanager get-secret-value` / `gcloud secrets versions access` / `az keyvault secret show` drives every backend correctly, plus the matching Terraform resource for each.

## Phase 3 — Queue

Frontends: **AWS SQS**, **GCP Pub/Sub (pull mode)**, **Azure Service Bus queue**.
Backends: same three clouds + **NATS JetStream** as the K8s peer.

Fidelity challenges: visibility timeouts, FIFO ordering, message attributes, dead-letter queues. Each frontend has its own model of in-flight vs visible vs dead-letter; the domain interface lives at the intersection.

**Exit criteria:** an SDK-using worker (any of the three) drains a queue identically when the backend is each of the four.

## Phase 4 — Pub/Sub

Frontends: **AWS SNS** (with SQS-shaped subscriptions for delivery), **GCP Pub/Sub**, **Azure Service Bus topics**.
Backends: same three clouds + **NATS core** as the K8s peer.

Shares auth + messaging infrastructure with Phase 3.

**Exit criteria:** any of `aws sns publish` / `gcloud pubsub topics publish` / `az servicebus topic create+send` fans out to a topic with subscriptions on every backend type correctly; subscribers receive the canonical message envelope for whichever frontend they used.

## Phase 5 — Managed RDBMS (control plane only)

Frontends: **AWS RDS**, **Cloud SQL Admin**, **Azure DB Admin** (Postgres + MySQL engines for each).
Backends: same three clouds + **CloudNativePG / MySQL Operator** (K8s) as the K8s peer.

**Different shape from Phases 1-4:** no data-plane proxying. The shim translates control-plane API calls (`CreateDBInstance` / `instances.insert` / `Servers_Create`, snapshot, restore) and returns connection metadata. Clients connect directly to the real Postgres / MySQL via wire protocol.

**Exit criteria:** any of `aws rds create-db-instance` / `gcloud sql instances create` / `az postgres flexible-server create` provisions a CloudNativePG cluster in K8s through the shim; the returned connection details let `psql` connect to the real PG and run queries.

## Phase 6 — Managed Redis (control plane only)

Frontends: **AWS ElastiCache**, **GCP Memorystore Admin**, **Azure Cache for Redis Admin**.
Backends: same three clouds + **Redis Operator** (K8s) as the K8s peer.

Same shape as Phase 5: control-plane only; data plane is wire-protocol RESP — direct client connection.

**Exit criteria:** any of `aws elasticache create-cache-cluster` / `gcloud redis instances create` / `az redis create` provisions a Redis Operator instance through the shim; returned endpoint accepts a `redis-cli` connection.

## Phase 7 — Functions

Frontends: **AWS Lambda** (container image deployment path), **GCP Cloud Run / Cloud Functions Gen 2**, **Azure Container Apps / Functions** (ARM, container path).
Backends: same three clouds + **Knative** (K8s) as the K8s peer.

Translation challenges: deployment metadata, event payload normalization (cross-cloud events → canonical HTTP envelope), VPC / network integration.

**Exit criteria:** a deployment (SAM template / `gcloud run deploy` / `az containerapp create`) deploys to Knative + every cloud backend through the shim and serves traffic; the function sees the expected event shape for whichever frontend originated the deployment.

## Phase 8 — API Gateway

Frontends: **AWS API Gateway HTTP API v2**, **GCP API Gateway**, **Azure API Management** (Consumption tier).
Backends: same three clouds + **Envoy Gateway** (K8s) as the K8s peer.

Declarative-replace model: `deploy(gateway_spec)` swaps the entire routing table atomically.

**Exit criteria:** Terraform `aws_apigatewayv2_api` + `google_api_gateway_api` + `azurerm_api_management` each deploy routes + integrations correctly through the shim to Envoy Gateway and to every cloud backend; published URLs serve the configured routes.

## Open questions (decide before they block work)

- Single org-wide deployment vs per-tenant — affects auth model.
- Where do live cloud test accounts live; who pays. (Blocks the per-cloud SDK / CLI / Terraform real-backend conformance lanes.)
- Coding-agent permissions for upstream spec-version bumps: auto-PR or human-in-loop?
- AMQP fidelity tier for Azure Service Bus (Phase 3.x + 4.x) — REST-only initially, or AMQP from the start?
- Codegen pipelines for the non-Smithy spec formats (GCP Discovery / Azure OpenAPI v3): build in-house alongside the existing Smithy emitter, or generate via official spec → Go tooling (oapi-codegen, etc.) and adapt the output to our handler shape? Phase 1.14 / 1.15 forces the call.

## Closed phases (PR index)

| PR | Phase | Headline |
|---|---|---|
| #1 | (bootstrap) | Repo created. Branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18 at `e5cc262`. |
| #2 | (bootstrap) | Continuity docs (PLAN, STATUS, WHAT_WE_DID, DO_NEXT, BUGS, AGENTS, CLAUDE→AGENTS symlink) + Phase-0 CI checks (branch-rebased, symlinks-resolve, continuity-docs-present) wired into the main-branch ruleset as required status checks. Merged 2026-05-18 at `4549a90`. |

# shimanism — Roadmap

> **Goal:** Build protocol-translation shims that let unmodified cloud SDKs / CLIs / Terraform providers run against AWS, GCP, Azure, or Kubernetes-native backends — by pointing them at a shim endpoint instead of the original service.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md).

## Guiding principles

1. **Spec is the contract.** Each shimmed service has a canonical published spec (AWS Smithy, GCP protobuf, Azure OpenAPI/ARM). Server-side wire layer is generated from that spec; hand-written code is translation logic only.
2. **Fidelity to the source API.** The shim speaks the cloud's API exactly. Error shapes, response headers, status codes, async-operation semantics — all match. Where the call can't be honored against the chosen backend, return the source cloud's own error in its own vocabulary. Never fabricate success.
3. **Real backends, not emulators.** Translated calls drive a real, comparable service: another cloud's equivalent, a Kubernetes operator, or a self-hosted system. The shim itself holds no state of record.
4. **Conformance from day one.** Every shimmed operation is exercised in the same commit by (a) the cloud's official SDK, (b) its CLI, and (c) its Terraform provider, against every backend in scope for that operation. No "land it and add tests later."
5. **Intersection only.** Shim the features common to AWS / GCP / Azure / the chosen K8s peer. Out-of-intersection features fail loud with the source cloud's own error.
6. **Kubernetes is a first-class backend.** Every service has a K8s peer on equal footing with the three clouds. If no suitable open-source peer exists, shimanism builds it.
7. **No fakes, no fallbacks, no degraded modes.** If a dependency is required, it is required. If a translation can't be honest, the call fails. See [PHILOSOPHY.md](PHILOSOPHY.md).
8. **One source spec, multiple adapters.** Each service has one front-door spec per cloud-source-protocol; many backend adapters. The codegen pipeline regenerates wire stubs from upstream specs; agents own the translation tables.
9. **Single-branch rule.** All in-flight work for one phase lands on one branch; many commits, one PR. User merges.
10. **Continuity always.** STATUS / WHAT_WE_DID / DO_NEXT / BUGS update at every significant chunk, not just at phase boundaries. Bug found = file the BUG before fixing.

## Pre-phase decisions to lock in before Phase 0

These shape everything downstream and should be confirmed before the foundation goes in.

| # | Decision | Default recommendation |
|---|----------|------------------------|
| 1 | Implementation language | Go |
| 2 | Spec sources | Pull upstream: AWS `aws-sdk-go-v2/codegen/sdk-codegen/aws-models` (Smithy JSON); GCP `googleapis/googleapis` (protobuf); Azure `Azure/azure-rest-api-specs` (OpenAPI). Never fork. |
| 3 | Codegen pipeline | spec → typed Go server stubs (handlers, request/response types, error envelopes). Hand-written code restricted to per-operation `translate.go`. |
| 4 | Backend abstraction | Per-service `Backend` interface in domain terms; not platform-wide. Cross-service generalization is premature. |
| 5 | Test fidelity rings | Per-PR: recorded interactions + unit. Nightly: live cloud accounts. Pre-release: vendor SDK integration suites against the shim endpoint. |
| 6 | Deployment | Single Go binary + Helm chart. SaaS deferred. |
| 7 | Repo layout | Monorepo. `services/<service>/` per shim; shared codegen, auth handlers, conformance harness. |
| 8 | License | AGPL-3.0 (already chosen). Watch for upstream SDK re-export issues. |
| 9 | Passthrough mode (AWS-source → AWS-backend) | Ship for Phase 1 (object storage) — useful for auth interception / observability. Re-evaluate per service. |
| 10 | Coding-agent permissions for spec updates | Human-in-loop on upstream-spec change. Agents propose; humans review the translation-table delta. |

## Phase 0 — Foundation

No shimmed service yet. Builds the platform every later phase reuses.

**Deliverables:**
- Spec ingestion pipeline (AWS Smithy initially; GCP / Azure deferred to their source-row phases).
- Codegen: spec → Go server stubs.
- **Conformance harness**: a test runner that
  - points an official cloud SDK at an arbitrary endpoint;
  - drives the same calls against passthrough, cross-cloud, and K8s backends;
  - captures requests/responses and diffs them;
  - supports SDK (Go + Python + Node at minimum), CLI, and Terraform provider as drivers.
- CI matrix wiring the harness to every PR.
- Skeleton repo layout; per-service module template; coding-agent task templates.

**Exit criteria:** the harness passes against a no-op `EchoService` adapter that returns the canonical cloud's success envelope shape. No real cloud calls translated yet — the test pipeline is hot.

**Why a separate phase:** every later phase reuses this. A weak foundation here means every service re-pays the cost.

## Phase 1 — Object storage (the pattern run)

Source protocol: S3 (AWS Signature V4 auth, XML responses, multipart upload, presigned URLs).
Backends: S3 passthrough · **MinIO** (reference impl — speaks S3 natively) · GCS · Azure Blob.

**Why first:** largest API surface to validate the platform; richest auth + content semantics; MinIO is a free-of-cost truth oracle for "what a faithful S3 looks like."

**Conformance:** `boto3`, `aws s3 cp`, Terraform `aws_s3_bucket` + `aws_s3_object`, AWS Go SDK integration tests — each run against all four backends through the shim.

**Exit criteria:** Terraform HCL written for AWS S3 provisions and exercises a GCS-backed bucket end-to-end via `endpoints { s3 = "..." }`, with no resource churn or fabricated responses. Equivalents for MinIO and Blob.

## Phase 2 — Secrets

Source: AWS Secrets Manager.
Backends: Secrets Manager passthrough · Vault · GCP Secret Manager · Azure Key Vault (secrets surface only).

Simpler API; validates the platform pattern doesn't accidentally depend on object-storage specifics. Should take a fraction of Phase 1's time.

**Exit criteria:** `aws secretsmanager get-secret-value` and the Terraform provider drive all four backends correctly.

## Phase 3 — Queue + Pub/Sub (paired)

Source: AWS SQS, AWS SNS.
Backends per [PHILOSOPHY.md § Eight Charges](PHILOSOPHY.md#the-eight-charges).

Paired because they share async-messaging infrastructure (auth, DLQ semantics, retry).
Big lift in protocol fidelity (visibility timeouts, FIFO ordering, message attributes).

**Exit criteria:** an SDK-using worker drains an SQS-shaped queue identically when the backend is NATS JetStream, Pub/Sub pull, and Service Bus.

## Phase 4 — Control-plane shims: Managed RDBMS + Managed Redis

Different shape: *no data-plane proxying.* The shim translates control-plane API calls (`CreateDBInstance`, snapshot, restore) and returns connection metadata. Clients connect directly to the real Postgres / MySQL / Redis instance.

Source: AWS RDS, AWS ElastiCache.
Backends: passthrough · CloudNativePG / MySQL Operator · Cloud SQL Admin · Azure DB Admin (and Redis equivalents).

**Exit criteria:** `aws rds create-db-instance --endpoint-url=...` provisions a CloudNativePG cluster in K8s; the returned connection details let `psql` connect to the real PG and run queries.

## Phase 5 — Functions + API gateway

Source: AWS Lambda (container image deployment), AWS API Gateway HTTP API v2.
Backends: Knative / Cloud Run / Azure Container Apps for functions; Envoy Gateway / GCP API Gateway / APIM (Consumption) for gateway.

Most complex phase. Function shim translates deployment + event payloads (S3 / SQS / SNS → canonical HTTP envelope). Gateway shim translates routing config with a strict declarative-replace model.

**Exit criteria:** a SAM template deploys to Knative + Envoy Gateway through the shim and serves traffic; AWS Lambda Powertools sees the expected event shape.

## Phase 6 — GCP source protocols (horizontal expansion)

For each of the eight services, add a GCP-source adapter. Backends are unchanged. Re-run conformance with `gcloud`, the GCP Go SDK, and the GCP Terraform provider.

This is where the codegen pipeline pays back: each service is a translation-table addition, not new architecture.

**Exit criteria:** `gcloud storage cp` with `--api-endpoint-overrides` works against an Azure-Blob-backed bucket through the shim, just as `aws s3 cp` did in Phase 1.

## Phase 7 — Azure source protocols (horizontal expansion)

Same shape as Phase 6 for Azure. AMQP for Service Bus is the new wrinkle; decide AMQP-vs-REST-only fidelity tier here (the AMQP coverage gap is the only known per-protocol fidelity tradeoff).

**Exit criteria:** Azure SDK + `az` CLI + AzureRM Terraform provider all drive the shim against all four backends for all eight services.

## Open questions (decide before they block work)

- Single org-wide deployment vs per-tenant — affects auth model.
- Where do live cloud test accounts live; who pays.
- Passthrough mode per service: ship by default, or only where there's a real reason?
- Coding-agent permissions for upstream spec-version bumps: auto-PR or human-in-loop?

## Closed phases (PR index)

| PR | Phase | Headline |
|---|---|---|
| #1 | (bootstrap) | Repo created. Branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce terminology. README.md with goals / non-goals / MVP service matrix. Merged 2026-05-18. |

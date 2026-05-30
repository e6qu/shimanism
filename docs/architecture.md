# Architecture

shimanism's primary purpose: **reroute cloud services, one at a time** (see [docs/migration.md](migration.md) for the end-to-end story). The architecture below is what makes that possible: a wire-protocol translation layer that lets your existing AWS / GCP / Azure code talk to a different cloud without rewriting anything. For runnable commands, see the [complete end-to-end examples](end-to-end-examples.md), including [standalone sockerless simulator examples](end-to-end-examples.md#optional-local-simulator-testing-with-sockerless).

shimanism is three layers:

1. **Frontends** — speak the source cloud's published wire protocol (S3 XML, GCS REST/JSON, Azure ARM, AWS Smithy JSON / Query / awsJson1_0, etc.). One frontend per (service × cloud) tuple.
2. **Domain** — the neutral interface between frontends and backends. One Go interface per shimmed service (`domain.Storage`, `domain.Secrets`, etc.). Defines the cross-cloud intersection.
3. **Backends** — translate domain calls into the destination's real API. One backend per (service × destination) tuple (`aws`, `gcp`, `azure`, an in-tree K8s peer, plus `inmem` for tests).

```
┌──────────────────────────────────────────────────────────────────┐
│ Frontends (one per source cloud — speak its wire protocol)       │
│                                                                  │
│   ┌────────────┐  ┌────────────┐  ┌────────────┐                 │
│   │  AWS S3    │  │  GCS REST  │  │ Azure Blob │ …               │
│   │  XML+SigV4 │  │  JSON      │  │  REST      │                 │
│   └─────┬──────┘  └─────┬──────┘  └─────┬──────┘                 │
│         │               │               │                        │
└─────────┼───────────────┼───────────────┼────────────────────────┘
          ▼               ▼               ▼
        ┌──────────────────────────────────────┐
        │   Domain   (intersection-only)       │
        │                                      │
        │   type Storage interface {           │
        │     CreateBucket(...)                │
        │     PutObject(...)                   │
        │     ...                              │
        │   }                                  │
        └────────────┬─────────────────────────┘
                     │
┌────────────────────┼─────────────────────────────────────────────┐
│ Backends (one per destination — call its real API)               │
│         ┌──────────┴───────────┬──────────────────┐              │
│         ▼                      ▼                  ▼              │
│   ┌────────────┐         ┌────────────┐    ┌────────────┐        │
│   │  aws (S3)  │         │  gcs       │    │  azure_blob│ …      │
│   │  passthru  │         │  passthru  │    │  passthru  │        │
│   └────────────┘         └────────────┘    └────────────┘        │
│                                                                  │
│   ┌────────────┐                                                 │
│   │  inmem     │  ← used for tests + dev; not for production     │
│   └────────────┘                                                 │
└──────────────────────────────────────────────────────────────────┘
```

The frontends and backends are independent. Any frontend can compose with any backend — that's the whole product. One running shim instance = one frontend + one backend; running multiple instances in parallel gets you a many-to-many matrix. AWS-shape Terraform → GCS bucket data. Azure SDK → AWS S3 bucket data. AWS Secrets Manager SDK → HashiCorp Vault. Etc.

## Repo layout (where to look for what)

```
shimanism/
├── cmd/                          ← entry points
│   ├── shim/                       the actual server you run (subcommands: storage, secrets, queue, pubsub, …)
│   ├── codegen/                    AWS Smithy → Go server stubs
│   ├── azure-codegen/              Azure OpenAPI v2 → Go server stubs (8-stage preprocessor + oapi-codegen)
│   ├── gcp-codegen/                GCP Discovery → Go routing layer (reuses google.golang.org/api wire types)
│   ├── inject-provenance/          stamps _provenance keys into vendored specs
│   └── shimctl/                    operational CLI
│
├── internal/                     ← frontends + domain interfaces
│   ├── <svc>/                      one per shimmed service
│   │   ├── domain/                   the neutral interface every backend implements
│   │   └── frontends/                one per source cloud (aws_s3, gcs, azure_blob, …)
│   │       └── <cloud>/adapter.go    glues generated server stubs to domain calls
│   ├── awsjson/                    AWS awsJson1.0/1.1 protocol harness
│   ├── awsquery/                   AWS Query protocol harness
│   ├── restxml/                    AWS REST-XML protocol harness
│   ├── sigv4verifier/              SigV4 verifier (server-side)
│   ├── gcpbearer/                  GCP OAuth2/ID-token verifier
│   ├── azurebearer/                Azure Entra-ID JWT verifier
│   ├── azuresharedkey/             Azure SharedKey verifier (Storage-only)
│   ├── clientconfig/               per-cloud client-config helpers used by tests
│   ├── codegen/                    emitter library imported by cmd/{codegen,azure-codegen,gcp-codegen}
│   └── harness/                    test harnesses
│
├── services/                     ← spec, generated stubs, backends, conformance
│   └── <svc>/
│       ├── spec/                     vendored upstream specs (Smithy / OpenAPI / Discovery)
│       │   └── SOURCES.md            provenance: upstream repo + commit SHA per file
│       ├── gen/                      `make codegen` output (regenerable, committed)
│       │   ├── <cloud>_<svc>.gen.go    types, server interface, route table
│       │   └── …
│       ├── backends/                 hand-written; one per destination
│       │   ├── aws/      translate.go + adapter
│       │   ├── gcs/      translate.go + adapter
│       │   ├── azureblob/ translate.go + adapter
│       │   ├── inmem/    in-process backend (tests/dev only)
│       │   └── minio/    or vault/, nats/, … — the K8s peer where applicable
│       ├── conformance/              SDK + CLI + Terraform tests, one dir per (frontend, driver)
│       ├── codegen.json              tells the AWS Smithy emitter which operations to generate
│       ├── azure-codegen.json        same for Azure
│       ├── gcp-codegen.json          same for GCP
│       ├── INTERSECTION.md           per-operation in/out-of-intersection audit
│       ├── APPLY_INTERSECTION.md     Terraform-apply-time audit for cross-cloud routes
│       └── OPERATIONS.md             operation index
│
├── peers/                        ← in-tree Kubernetes peers (separate Go modules)
│   └── shimakit/                   framework providing common-denominator primitives
│
├── scripts/                      ← fetch-{aws,azure,gcp}-*.sh + the sockerless E2E driver
├── docs/                         ← all contributor + deep-dive documentation (this file lives here)
└── PHILOSOPHY.md AGENTS.md PLAN.md STATUS.md DO_NEXT.md WHAT_WE_DID.md BUGS.md
```

A few details worth knowing on first read:

- **All docs live under `docs/`.** Contributor-facing overviews (`architecture.md`, `codegen.md`, `testing.md`, `getting-started.md`, per-service pages under `services/`) sit alongside the deep-dive references (`codegen-pipelines.md`, `verifiers.md`, `cross-cloud-routing.md`, `compatible-licenses.md`, `dependency-policy.md`, `sockerless-validation.md`). Start at [docs/README.md](README.md).
- **`gen/` is committed.** The generated code is in version control so that (a) CI can `make codegen-check` and fail merges on undetected drift, (b) `go build` works without invoking codegen, and (c) PR diffs show the impact of spec changes alongside translation-layer edits.
- **`spec/` is vendored, not forked.** Files are downloaded by the `scripts/fetch-*.sh` helpers; their provenance is recorded in `SOURCES.md` and stamped into each spec's `_provenance` top-level key by `cmd/inject-provenance`. Renovate's custom manager watches the SHAs.
- **`internal/<svc>/frontends/<cloud>/adapter.go`** is where the generated server interface meets the hand-written domain layer. It is the smallest interesting file for understanding how a request flows end-to-end — read one to orient.
- **`services/<svc>/backends/<cloud>/translate.go`** is the *only* file you hand-write per operation in production code. Generated stubs above; SDK calls below; translation in between.

## Minimal common footprint

The cross-cloud intersection of every shimmed service collapses to one short Go interface. The whole catalog is 75 methods:

| Service | Domain interface | Methods | File |
|---|---|---|---|
| storage | `domain.Storage` | 16 | [`internal/storage/domain/domain.go`](../internal/storage/domain/domain.go) |
| queue | `domain.Queues` | 12 | [`internal/queue/domain/domain.go`](../internal/queue/domain/domain.go) |
| pubsub | `domain.Pubsub` | 12 | [`internal/pubsub/domain/domain.go`](../internal/pubsub/domain/domain.go) |
| rdbms | `domain.RDBMS` | 11 | [`internal/rdbms/domain/domain.go`](../internal/rdbms/domain/domain.go) |
| secrets | `domain.Secrets` | 8 | [`internal/secrets/domain/domain.go`](../internal/secrets/domain/domain.go) |
| cache | `domain.Cache` | 6 | [`internal/cache/domain/domain.go`](../internal/cache/domain/domain.go) |
| functions | `domain.Functions` | 5 | [`internal/functions/domain/domain.go`](../internal/functions/domain/domain.go) |
| apigateway | `domain.APIGateway` | 5 | [`internal/apigateway/domain/domain.go`](../internal/apigateway/domain/domain.go) |

Compared to the generated frontend surface (`services/storage/gen/aws_s3.gen.go` alone is ~31 000 lines covering every documented S3 operation), the domain is intentionally tiny. Anything that does not fit the intersection is not in the domain, and the frontend returns the source cloud's own *operation-not-supported* envelope. See [PHILOSOPHY.md § The Intersection](../PHILOSOPHY.md#the-circle) for the principle and the per-service `INTERSECTION.md` files for the per-operation audit that classifies every wire operation as in-intersection, naturally-unset, or out-of-intersection.

## What lives in each layer

| Layer | Contains | Allowed to do |
|---|---|---|
| **Frontend** | Wire-protocol HTTP routing, request/response decoders, error envelope shaping, per-cloud quirks (e.g. AWS query-XML compatibility headers, GCP operation polling endpoints, Azure async-operation URLs) | Translate wire ↔ domain. **Cannot** call backends directly — must go through the domain. |
| **Domain** | One interface per service, plus typed errors, plus shared types (`Bucket`, `Secret`, etc.) | Define the intersection. **Stateless** — no in-process maps that backends or frontends read across requests. |
| **Backend** | Calls to the real destination (`aws-sdk-go-v2`, `cloud.google.com/go/...`, Azure SDK, NATS client, etc.) | Call destination APIs. Translate domain options to destination shapes. Surface destination errors back through the domain's typed-error vocabulary. |

The `internal/<service>/` tree holds the domain + frontends. The `services/<service>/` tree holds the backends + conformance tests + the codegen manifest + the per-service `OPERATIONS.md` / `INTERSECTION.md` / `APPLY_INTERSECTION.md`.

## Statelessness

**The shim binary holds no state of record.** ([AGENTS.md § The shim is stateless](../AGENTS.md#the-shim-is-stateless).)

- No sidecar storage. No SQLite, no Redis, no shim-managed namespace.
- No in-process cache treated as authoritative.
- Multipart-upload coordination state lives in the destination (GCS multipart parts go in GCS itself under `.uploads/<id>/`, etc.). Not in the shim.
- Cross-cloud shape translations that need a stable mapping (e.g. Azure's GUID version IDs ↔ the monotonic integer the AWS frontend exposes) are *derived at request time* by listing versions and sorting by creation timestamp. No translation table in the shim.
- Async-operation polling (`Operations.Get` for GCP, status polling for AWS / Azure) encodes `(opType, target)` into the Operation Name so a polling client resolves status by re-reading the underlying resource. No shim-side operation table.

A stateless shim scales horizontally (any replica answers any request), restarts cleanly (no warmup, no recovery), and can't lie (every answer comes from the backend that actually owns the data).

If a feature can't be implemented statelessly, it's out of intersection — return the source cloud's `OperationNotSupported` envelope. Don't add state to make it work.

## Intersection-only scope

shimanism only shims the operations and feature flags that exist semantically across AWS + GCP + Azure + the chosen K8s peer for each service. A feature in one cloud only isn't portable and isn't eligible.

When a call lands on an out-of-intersection feature, the shim returns the source cloud's own "not supported" error envelope — `NotImplementedException`, `OperationNotSupported`, `InvalidParameterValue`, etc. Never fabricated success. Never a generic 500. Never silent degradation.

This is the [PHILOSOPHY.md § The Intersection](../PHILOSOPHY.md#the-circle) constraint made testable. Per-service [`INTERSECTION.md`](../services/storage/INTERSECTION.md) audits classify every wire-level operation as:

1. **In intersection (real work)** — dispatches to a domain call against a real backend.
2. **Feature genuinely unset** — returns the source cloud's *real* "unset" envelope (e.g. `NOT_FOUND` for an absent sub-resource).
3. **Out of intersection** — returns the source cloud's *real* "not supported" envelope.

A fourth implicit category — "returns something plausible without doing real work" — is by definition a fake and either gets removed or filed as a [BUGS.md](../BUGS.md) entry.

A fifth category — "in intersection, but the destination cloud represents the semantic differently" — gets a *published normalization rule* under [normalizations.md](normalizations.md). Each rule is deterministic, stateless, and exercised by a named test. New cross-cloud asymmetries either get a rule or get classified as category 3 (out of intersection).

## Kubernetes is the fourth backend

Every shimmed service has a K8s peer on equal footing with AWS / GCP / Azure. When a suitable open-source K8s-native peer exists (MinIO for storage, Vault for secrets, NATS JetStream for queue/pubsub, CloudNativePG for Postgres, Redis Operator for cache, Knative for functions, Envoy Gateway for API gateway), the shim uses it.

When no suitable peer exists, shimanism builds one. The framework lives at [`peers/shimakit/`](../peers/) and provides the common-denominator primitives every shimmed service reduces to: named versioned binary objects, per-object metadata, soft-delete + force-delete, list with prefix + pagination, multi-namespace addressing. Concrete peers built on shimakit follow the `shima<service>` naming convention.

## Fidelity to the source cloud

The shim's front door speaks the cloud's published API exactly:

- Response shapes match.
- Error envelopes match (XML for S3 errors, JSON for most others, ARM problem-details for Azure).
- HTTP status codes match.
- Async-operation semantics match (Operations.Get endpoints, ETag headers, long-poll behavior).
- Path templates, query-parameter names, header names — match.

**Current state:** the codegen pipeline runs three lanes — AWS Smithy via `cmd/codegen`, Azure OpenAPI v2 via `cmd/azure-codegen` (with an 8-stage preprocessor), and GCP Discovery via `cmd/gcp-codegen` (routing-only; wire types reuse `google.golang.org/api/<svc>/v1` per reuse-over-reinvention). `make codegen` regenerates every spec-driven file across all 8 services × 3 lanes. AWS frontends ship fully migrated through their generated stubs; Azure has `azure_keyvault` migrated as the reference impl with the remaining 7 + all 8 GCP frontends using hand-written dispatch on top of the generated gen inventory (the inventory acts as the spec-drift contract for those). Per-operation `translate.go` files map source-API requests to backend domain operations. See [docs/codegen.md](codegen.md) for the overview and [`docs/codegen-pipelines.md`](../docs/codegen-pipelines.md) for full pipeline detail.

## Cross-cloud routing in practice

The end-state is composable: AWS S3 frontend on top of a GCS backend, all running in one shim binary, with the bucket living in actual GCS.

```sh
shim storage \
  -frontend=aws_s3 \
  -backend=gcs \
  -gcs-project=my-project \
  -addr=:9001
```

A user's `aws s3 mb s3://thing` → SigV4-signed XML → shim's AWS S3 frontend decodes → `domain.Storage.CreateBucket(name, region)` → GCS backend's `Buckets.insert` against real `storage.googleapis.com` → the bucket exists in GCS, the CLI thinks it created an S3 bucket.

That's the whole architecture.

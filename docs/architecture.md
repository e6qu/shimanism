# Architecture

shimanism's primary purpose: **reroute cloud services, one at a time** (see [docs/migration.md](migration.md) for the end-to-end story). The architecture below is what makes that possible: a wire-protocol translation layer that lets your existing AWS / GCP / Azure code talk to a different cloud without rewriting anything.

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

The frontends and backends are independent. Any frontend can compose with any backend — that's the whole product. AWS-shape Terraform → GCS data. Azure SDK → AWS S3 data. NATS CLI → AWS SQS data. Etc.

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

Server stubs are generated from the upstream spec (Smithy for AWS, OpenAPI for Azure, Discovery doc / protobuf for GCP). Hand-written code is restricted to per-operation `translate.go` files that map source-API requests to backend domain operations.

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

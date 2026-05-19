# shimakit — the K8s-peer framework

> A single open-source, K8s-native framework that fills the K8s-peer
> slot for shimmed services whose existing OSS peer doesn't fit, all
> in one toolkit. Built around the **common denominator** every
> shimmed service reduces to: versioned, named binary objects with
> structured metadata and a soft-delete lifecycle.
>
> Distinct from `services/<svc>/backends/` — those wrap *third-party*
> backends (MinIO, Vault, real cloud SDKs). `peers/` is for components
> we ship under the shim brand when no third party fits the
> [PHILOSOPHY.md § The Fourth Wall](../../PHILOSOPHY.md#the-fourth-wall)
> rule.

## Naming convention

- **`shimakit`** is the framework — the `Store` interface in
  [`peer.go`](peer.go) and the shared primitives (auth verification,
  routing, storage plumbing) every shim-built peer composes from.
- Concrete per-service peers built on top of shimakit are named
  **`shima<service>`** — e.g. `shimasecret` for a secrets peer,
  `shimastore` for object-storage, `shimaqueue` for queues. Each
  lives at `peers/shima<service>/` when it ships, with its own
  binary, its own deploy manifests, and its own `go.mod`.
- Phase 1 and Phase 2 used MinIO and Vault — no concrete
  `shima<service>` peer has shipped yet. The first one ships when
  a service phase surfaces a real OSS gap.

## The rule

Every shimmed service has a K8s peer on equal footing with the three
clouds (AWS / GCP / Azure). Phase 1 (object storage) and Phase 2
(secrets) used existing OSS peers — MinIO and HashiCorp Vault. When a
future phase hits a service whose K8s-native equivalent doesn't exist
yet, **we build it here, under `peers/shima<service>/`, on top of
this framework, AGPL-3.0 alongside the rest of shimanism**.

## Inclusion criteria

A `shima<service>` peer lands only when:

1. The matching service phase needs a K8s-native peer to honour the
   N × N matrix, AND
2. No suitable OSS component exists today (or every candidate fails
   a hard requirement like license, scope, or activity), AND
3. The peer **uses no shim-side state of record** (per
   [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless))
   — the peer is itself a self-contained service that holds the data
   it manages, but the shim sitting in front of it adds no extra
   storage layer.

If an OSS peer exists and meets the requirements, we use it
(referenced from `services/<svc>/backends/<name>/`) — not here.

## Why one framework and not one peer per service from scratch

Phase 1 used MinIO as its K8s peer. Phase 2 used HashiCorp Vault.
Both are real OSS projects that fit the shimmed-service contract
cleanly — perfect.

Some future service phases hit gaps where no OSS K8s-native peer is
a clean fit: AWS Lambda's custom-runtime + Layers model doesn't
fully map to Knative; AWS IAM has no clean OSS equivalent at all;
cross-cloud managed-database control planes diverge enough that any
single operator covers only a slice. The temptation is to build a
bespoke peer per service from zero each time.

Don't do that. Every shimmed-service intersection reduces to **the
same small set of primitives**:

| Primitive | What it stores |
|---|---|
| Named, versioned binary objects | every storage / secret / queue message / config entry / function package, … |
| Per-object structured metadata (string→string map) | tags, labels, content-types, stage labels, custom_metadata |
| Soft-delete lifecycle (live → deleted → purged) | every "delete with recovery window" semantic across the four clouds |
| List operations with prefix + pagination | every list-objects / list-secrets / list-queue-messages |

That's the whole `peer.go` interface. Every concrete `shima<service>`
peer composes it into a service-shaped HTTP front (S3 / Vault / SQS /
Lambda / …) without re-implementing the byte plumbing each time.

## Scope of the framework

In scope:
- The `Store` interface (versioned named bytes + metadata).
- Multi-namespace addressing so one underlying storage layer serves
  many `shima<service>` deployments.
- Soft-delete + force-delete lifecycle with a configurable recovery
  window per namespace.
- List + prefix + monotonic-version semantics.
- Pluggable storage layer (filesystem, S3-compatible, etcd, …) —
  one shared interface, many implementations.
- Stateless front-end: the framework's *own* state lives in its
  chosen storage layer; the request handlers carry nothing across
  calls.

Explicitly out of scope for the framework:
- Auth / identity management — the framework accepts whatever the
  ingress layer authenticated (the shim sitting in front of it does
  the cloud-shape validation; the peer trusts the ingress).
- Per-service semantics: nothing in `shimakit` knows about S3,
  Vault, SQS, Lambda, etc. The shim service's frontend handles the
  per-cloud shape; the framework is generic. Per-service code lives
  in the `shima<service>` binary that consumes the framework.
- Replication, multi-region, HA: deferred to the chosen storage
  layer (each storage implementation decides how to do it).
- Encryption-at-rest: same — delegated to the storage layer.

The framework doesn't try to be a cloud. It tries to be a sufficient
*backing toolkit* for the shim's K8s-peer slot, no more.

## When this gets built

Not yet. Phase 1 and Phase 2 didn't need it — MinIO and Vault are
fine. The first service phase that hits an unfilled K8s-peer slot is
when we add the in-tree implementation of `shima<service>` on top of
shimakit. Phase 7 (functions) is the likeliest candidate to surface
this gap, but we'll decide per phase based on the actual OSS
landscape at that point.

Until then this directory holds:
- The design contract ([`peer.go`](peer.go) — the `Store` interface
  every implementation satisfies, and the contract every consumer
  relies on).
- This README.

The Go module ([`go.mod`](go.mod)) is intentionally separate from the
root shim module so the framework can be released, deployed, and
pinned independently. The shim itself imports this module only when
it needs to use a `shima<service>` peer as a *backend* of a service —
and that import is optional (other backends — real cloud SDKs,
MinIO, Vault, etc. — still work without it).

## Layout (when filled in)

```
peers/
├── shimakit/                — the framework (this directory)
│   ├── README.md
│   ├── peer.go              — the Store interface + contract types
│   ├── go.mod               — separate module
│   ├── store/               — pluggable storage backends (when added)
│   │   ├── memory/          — in-process (test fixture)
│   │   ├── filesystem/      — local disk (single-node deployments)
│   │   └── …                — etcd / S3-compatible / others as needed
│   └── server/              — HTTP handler primitives (when added)
└── shima<service>/          — one directory per concrete peer
    ├── README.md            — what + why + scope
    ├── go.mod               — separate module (depends on shimakit)
    ├── cmd/shima<service>/  — binary entry point
    ├── internal/            — service-specific Go packages
    └── deploy/k8s/          — Helm chart / kustomization for in-cluster install
```

# shimanism — the K8s peer

> A single open-source, K8s-native service that fills the K8s-peer slot
> for shimmed services whose existing OSS peer doesn't fit, all in one
> binary. Built around the **common denominator** every shimmed service
> reduces to: versioned, named binary objects with structured metadata
> and a soft-delete lifecycle.

## Why one package and not one peer per service

Phase 1 (object storage) used MinIO as its K8s peer. Phase 2 (secrets)
used HashiCorp Vault. Both are real OSS projects that fit the
shimmed-service contract cleanly — perfect.

Some future service phases hit gaps where no OSS K8s-native peer is a
clean fit: AWS Lambda's custom-runtime + Layers model doesn't fully map
to Knative; AWS IAM has no clean OSS equivalent at all; cross-cloud
managed-database control planes diverge enough that any single
operator covers only a slice. The temptation is to build one bespoke
peer per service: `shimanism-functions-peer`, `shimanism-iam-peer`,
etc.

Don't do that. Every shimmed-service intersection reduces to **the same
small set of primitives**:

| Primitive | What it stores |
|---|---|
| Named, versioned binary objects | every storage / secret / queue message / config entry / function package, ... |
| Per-object structured metadata (string→string map) | tags, labels, content-types, stage labels, custom_metadata |
| Soft-delete lifecycle (live → deleted → purged) | every "delete with recovery window" semantic across the four clouds |
| List operations with prefix + pagination | every list-objects / list-secrets / list-queue-messages |

That's enough for every K8s peer we'll need to ship. One service
binary covers all of them. Each shim service points at a different
**namespace** inside this peer; the K8s peer doesn't care whether the
namespace's contents are S3 keys, secrets, queue messages, or Lambda
packages — it stores opaque bytes + metadata and lets the shim's
service-specific frontend interpret them.

This file says **what shimanism the peer is**. The Go interface in
`peer.go` says **what it must do**. Both are short on purpose.

## Scope

In scope:
- Versioned, named binary objects with size + creation timestamp.
- Per-object structured metadata (`map[string]string`).
- Multi-namespace addressing so one peer serves many shim services.
- Soft-delete + force-delete lifecycle with a configurable recovery
  window per namespace.
- List + prefix + monotonic-version semantics.
- HTTP API (one route table; small surface).
- Stateless front-end: the peer's *own* state lives in its chosen
  storage layer (pluggable: filesystem, S3, etcd, ...); the request
  handlers carry nothing across calls.

Explicitly out of scope:
- Auth / identity management — the peer accepts whatever the
  ingress layer authenticated (the shim sitting in front of it does
  the cloud-shape validation; the peer trusts the ingress).
- Per-service semantics: nothing in the peer knows about S3, Vault,
  SQS, Lambda, etc. The shim service's frontend handles the
  per-cloud shape; the peer is generic.
- Replication, multi-region, HA: deferred to the chosen storage
  layer (each storage implementation decides how to do it).
- Encryption-at-rest: same — delegated to the storage layer.

The peer doesn't try to be a cloud. It tries to be a sufficient
*backing store* for the shim's K8s-peer slot, no more.

## When this gets built

Not yet. Phase 1 and Phase 2 didn't need it — MinIO and Vault are
fine. The first service phase that hits an unfilled K8s-peer slot is
when we add the in-tree implementation. Phase 7 (functions) is the
likeliest candidate to surface this gap, but we'll decide per phase
based on the actual OSS landscape at that point.

Until then this directory holds:
- The design contract ([`peer.go`](peer.go) — the `Store` interface
  every implementation satisfies, and the contract every consumer
  relies on).
- This README.

The Go module ([`go.mod`](go.mod)) is intentionally separate from the
root shim module so the peer can be released, deployed, and pinned
independently. The shim itself imports this module only when it needs
to use shimanism as a *backend* of a service — and that import is
optional (other backends — real cloud SDKs, MinIO, Vault, etc. —
still work without it).

## Layout (when filled in)

```
peers/shimanism/
├── README.md            — this file
├── peer.go              — the Store interface + contract types
├── go.mod               — separate module
├── cmd/shimanism/       — binary entry point
├── internal/server/     — HTTP handler
├── internal/store/      — pluggable storage backends
│   ├── memory/          — in-process (test fixture)
│   ├── filesystem/      — local disk (single-node deployments)
│   └── ...              — etcd / S3-compatible / others as needed
└── deploy/k8s/          — Helm chart + kustomization
```

## Naming

The exported binary is `shimanism`. In K8s manifests it surfaces as
`shimanism.<namespace>.svc.cluster.local`. The brand prefix exists so
the peer is unambiguous in `ps`, container image names, and operator
documentation — distinct from the upstream shim binary (`shim`) that
sits in front of it.

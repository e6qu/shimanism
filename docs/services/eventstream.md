# Event streaming

Event streaming is a Kafka-shaped service: ordered partitions, append-only
records, offset reads, and consumer-group committed offsets. It is not a
replacement for Phase 4 pub/sub fan-out.

Phase 20 is in progress. The current implementation has the neutral domain
contract, the real inmem append-only log backend, the shared Kafka TCP frame
codec, the first Kafka request dispatcher for real metadata, topic lifecycle,
produce/fetch, and offsets, the GCP Managed Kafka control-plane frontend, and
the current AWS MSK control-plane frontend slice.

## Frontends

| Frontend | Wire protocol | Status |
|---|---|---|
| AWS MSK | MSK restJson1 control plane + Kafka data plane | Current slice implements cluster lifecycle, bootstrap discovery, topic lifecycle, and AWS SDK + Kafka client conformance over `domain.Streams`. |
| GCP Managed Kafka | Discovery REST control plane + Kafka data plane | Topic lifecycle frontend implemented over `domain.Streams`; real Kafka client produce/fetch conformance passes against the same backend. |
| Azure Event Hubs | ARM control plane + Event Hubs Kafka endpoint | Planned. |

## Backends

| Backend | Real destination | Status |
|---|---|---|
| `inmem` | Process-local append-only partition log | Implemented for tests/local development. |
| `aws` | Real Amazon MSK / Kafka endpoint | Planned. |
| `gcp` | Real Managed Service for Apache Kafka | Planned. |
| `azure` | Real Azure Event Hubs Kafka endpoint | Planned. |
| `strimzi` | Strimzi Kafka on Kubernetes | Planned K8s peer. |

## Intersection Contracts

- **[`services/eventstream/INTERSECTION.md`](../../services/eventstream/INTERSECTION.md)** — portable operation set and out-of-intersection features.
- **[`docs/phase-20-scoping.md`](../phase-20-scoping.md)** — detailed Phase 20 scoping, K8s peer choice, and sub-phase plan.

## Conformance

Conformance is active for the first slices: Kafka handler tests drive real Kafka
frames through the dispatcher, the GCP Managed Kafka topic-control plane is
driven by the official REST SDK, and both the GCP and AWS SDK rows drive the TCP
data plane with `franz-go/pkg/kgo` after creating a topic through the source
control plane. Later slices must add CLI/Terraform and the remaining cloud
frontends/backends.

## Known Gaps

- Kafka request dispatch exists for the first data-plane slice. Unsupported
  Kafka features return explicit Kafka errors rather than synthetic success.
- Azure control-plane frontend and real connected backends are not implemented
  yet.
- Consumer-group protocol behavior beyond committed offset storage still needs
  a real Kafka implementation plan.

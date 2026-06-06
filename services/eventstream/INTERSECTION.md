# Event Streaming Intersection

Phase 20 covers ordered, partitioned event streams. It is distinct from
Phase 4 pub/sub: this service is Kafka-shaped, with explicit partitions,
monotonic offsets, append reads/writes, and consumer-group committed offsets.

The shim is stateless. Topic metadata, records, retained offsets, and committed
consumer offsets live in the backend: a real cloud Kafka/Event Hubs service,
Strimzi Kafka, or the inmem backend used for tests and local development.

## In Intersection

| Capability | AWS MSK | GCP Managed Kafka | Azure Event Hubs | K8s / Strimzi |
|---|---|---|---|---|
| Cluster create/get/list/delete | MSK cluster APIs | Managed Kafka cluster APIs | Event Hubs namespace ARM APIs | `Kafka` custom resources |
| Bootstrap discovery | `GetBootstrapBrokers` | cluster bootstrap endpoint | namespace Kafka endpoint | Strimzi bootstrap service |
| Topic create | Kafka `CreateTopics` | `topics.create` or Kafka `CreateTopics` | `eventhubs.createOrUpdate` | `KafkaTopic` custom resources |
| Topic describe/list/delete | Kafka metadata/admin APIs | `topics.get/list/delete` or Kafka APIs | Event hub ARM APIs | `KafkaTopic` custom resources |
| Produce/fetch | Kafka `Produce` / `Fetch` | Kafka `Produce` / `Fetch` | Kafka endpoint `Produce` / `Fetch` | Kafka `Produce` / `Fetch` |
| Offset bounds | Kafka `ListOffsets` | Kafka `ListOffsets` | Kafka endpoint `ListOffsets` | Kafka `ListOffsets` |
| Consumer offsets | Kafka `OffsetCommit` / `OffsetFetch` | Kafka offset APIs | Kafka endpoint offset APIs | Kafka offset APIs |

## Portable Topic Options

- Partition count.
- Retention duration.
- Source-cloud tags or labels where the source API exposes them.

Replication factor is not portable as a user-controlled setting. AWS MSK and
Strimzi expose broker replication; Azure Event Hubs owns replication inside the
managed service. Frontends must reject unsupported replication options with the
source cloud's own error vocabulary instead of inventing a translation.

## Out Of Intersection

- Schema registry, Kafka Connect, stream processing, MirrorMaker, and managed
  connectors.
- ACLs, quotas, per-cloud private networking, encryption policy, and monitoring
  configuration.
- Arbitrary Kafka topic configuration keys beyond explicitly documented
  normalizations.
- Shim-managed cluster catalogs, topic maps, offset maps, or sidecar storage.
- Test-only Kafka protocol success paths. A Kafka frontend must parse and answer
  real Kafka requests or fail with honest Kafka/source-cloud errors.

## Current 20.A Foundation

The first implementation slice defines:

- `internal/eventstream/domain`: neutral topic, partition-log, fetch, offset,
  and consumer-offset contracts.
- `services/eventstream/backends/inmem`: a real in-process append-only partition
  log for tests and local development.

The first Kafka data-plane dispatcher is registered as an internal runtime, GCP
Managed Kafka topic lifecycle is implemented, and the current AWS MSK slice adds
cluster lifecycle/bootstrap discovery plus topic lifecycle. Topic/log/offset
state is explicitly cluster-scoped in `domain.Streams`. There is no fake Kafka
server: every successful Kafka or REST topic response must come from
`domain.Streams`, and out-of-intersection features must return
Kafka/source-cloud errors.

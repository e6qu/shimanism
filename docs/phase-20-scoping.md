# Phase 20 — Event Streaming: Scoping

> Pre-implementation audit for ordered, partitioned event streams. Read
> [PLAN.md § Phase 20](../PLAN.md#phase-20--event-streaming) for the premise and
> [AGENTS.md](../AGENTS.md) for the rules.

**Premise.** This phase is not a second pub/sub service. Phase 4 covered fan-out
messaging. Phase 20 targets the Kafka-shaped intersection: ordered partitions,
offset-based consumption, producer append, consumer groups, and topic lifecycle.

## 0. Service Shape

Phase 20 has two planes:

- **Control plane** — cloud-specific management APIs for clusters/namespaces and
  topics/event hubs.
- **Data plane** — Kafka binary TCP protocol for produce, fetch, metadata, offsets,
  and consumer groups.

The shim should not hand-roll a Kafka codec. The first implementation PR should
reuse generated Kafka protocol types from `github.com/twmb/franz-go/pkg/kmsg` or
an equivalent pure-Go, license-compatible protocol package after checking
`docs/dependency-policy.md`. This phase needs a real Kafka protocol frontend, not
test-only fakes.

Proposed layout:

```text
internal/eventstream/
  domain/domain.go
  kafkawire/                 # shared Kafka TCP protocol runtime
  frontends/
    aws_msk/
    gcp_managedkafka/
    azure_eventhubs/
services/eventstream/
  spec/
  gen/
  backends/
    inmem/                   # real append-only partition log for tests
    aws/
    gcp/
    azure/
    strimzi/
  conformance/
  INTERSECTION.md
```

## 1. Source APIs

| Cloud | Control plane | Topic resource | Data plane |
|---|---|---|---|
| AWS | Amazon MSK API: clusters at `/v1/clusters`; create/get/list/delete cluster APIs | Kafka protocol `CreateTopics` / `DeleteTopics`; MSK does not provide topic CRUD as an MSK REST resource | Kafka protocol on broker endpoints |
| GCP | Managed Service for Apache Kafka REST API (`managedkafka.googleapis.com`) | `v1.projects.locations.clusters.topics` create/get/list/patch/delete | Kafka protocol on cluster bootstrap endpoints |
| Azure | ARM `Microsoft.EventHub/namespaces` | ARM `namespaces/{namespace}/eventhubs/{eventHub}` | Event Hubs Kafka endpoint |
| K8s | Strimzi `Kafka` custom resources | Strimzi `KafkaTopic` custom resources | Kafka protocol on Strimzi bootstrap service |

Sources:

- AWS MSK API reference: <https://docs.aws.amazon.com/msk/1.0/apireference/clusters.html>
- GCP Managed Service for Apache Kafka REST API: <https://docs.cloud.google.com/managed-service-for-apache-kafka/docs/reference/rest>
- Azure Event Hubs Kafka endpoint overview: <https://learn.microsoft.com/azure/event-hubs/azure-event-hubs-apache-kafka-overview>
- Azure Event Hubs ARM namespace/event hub REST APIs: <https://learn.microsoft.com/rest/api/eventhub/>
- Apache Kafka protocol: <https://kafka.apache.org/protocol/>
- Strimzi operator overview/topic management: <https://strimzi.io/docs/operators/latest/full/overview>

## 2. Intersection

### Cluster / namespace lifecycle

| Domain op | AWS MSK | GCP Managed Kafka | Azure Event Hubs | K8s Strimzi |
|---|---|---|---|---|
| CreateCluster | `CreateCluster` | `clusters.create` | `namespaces.createOrUpdate` | create `Kafka` CR |
| DescribeCluster | `DescribeCluster` | `clusters.get` | `namespaces.get` | read `Kafka` CR/status |
| ListClusters | `ListClusters` | `clusters.list` | `namespaces.list` by resource group or subscription | list `Kafka` CRs |
| DeleteCluster | `DeleteCluster` | `clusters.delete` | `namespaces.delete` | delete `Kafka` CR |
| BootstrapBrokers | `GetBootstrapBrokers` | cluster bootstrap endpoint | namespace FQDN + Kafka port | Strimzi bootstrap service |

`ListClusters` on Azure is ARM-scoped (`by resource group` or subscription); the
frontend should expose the source cloud's list semantics and map to the domain
filter. No shim-side cluster catalog is allowed.

### Topic / event hub lifecycle

| Domain op | AWS MSK | GCP Managed Kafka | Azure Event Hubs | K8s Strimzi |
|---|---|---|---|---|
| CreateTopic | Kafka `CreateTopics` | `topics.create` or Kafka `CreateTopics` | `eventhubs.createOrUpdate` | create `KafkaTopic` CR |
| DescribeTopic | Kafka `Metadata` / configs | `topics.get` | `eventhubs.get` | read `KafkaTopic` CR/status |
| ListTopics | Kafka `Metadata` | `topics.list` | `eventhubs.listByNamespace` | list `KafkaTopic` CRs |
| DeleteTopic | Kafka `DeleteTopics` | `topics.delete` or Kafka `DeleteTopics` | `eventhubs.delete` | delete `KafkaTopic` CR |

Topic options in intersection:

- `partitionCount`
- `retentionMs` / retention duration
- opaque tags/labels on control-plane resources where the source API has tags

Out of the first intersection:

- replication factor as a portable user input. AWS MSK and Strimzi expose broker
  replication; Azure Event Hubs does not expose Kafka replication factor because
  the event hub is the service-managed log.
- arbitrary Kafka topic config keys. Only normalize explicitly documented keys.
- ACLs, quotas, schema registry, Kafka Connect, stream processing, MirrorMaker,
  private networking, encryption policy, and per-cloud monitoring/logging.

### Data plane

Minimum Kafka APIs for the first slice:

| Kafka API | Needed for |
|---|---|
| `ApiVersions` | official clients discover broker support |
| `Metadata` | topic/partition discovery |
| `Produce` | append records |
| `Fetch` | read records by partition/offset |
| `ListOffsets` | earliest/latest offset queries |
| `FindCoordinator` | consumer-group clients |
| `JoinGroup` / `SyncGroup` / `Heartbeat` / `LeaveGroup` | basic consumer groups |
| `OffsetCommit` / `OffsetFetch` | committed offsets |
| `CreateTopics` / `DeleteTopics` | AWS-source topic lifecycle and Kafka admin clients |

The inmem backend for this phase must be a real append-only partition log:

- records are stored per topic + partition in offset order.
- offset assignment is monotonic.
- fetch by offset returns actual records, not canned success.
- committed consumer offsets are backend state.
- retention can be implemented as backend-owned state pruning; the shim itself
  remains stateless.

## 3. K8s Peer Decision

Use **Strimzi Kafka** as the K8s peer, not an in-tree `shimaqueue` over
`shimakit`.

Reasoning:

- Strimzi runs real Apache Kafka on Kubernetes and manages clusters through
  Kubernetes custom resources.
- Strimzi's Topic Operator manages topics through `KafkaTopic` resources; that
  gives the control plane a real Kubernetes object to read/write.
- The Kafka data plane is then the actual Strimzi broker service. The shim can
  speak Kafka to it exactly as it speaks to AWS/GCP/Azure backends.
- `shimakit` is intentionally named, versioned object storage. Kafka is an
  ordered partition log with consumer offsets; forcing it into `shimakit` would
  create the wrong abstraction and likely require fake coordination state.

Concrete backend shape:

- `services/eventstream/backends/strimzi` uses Kubernetes dynamic/typed clients
  for `Kafka` and `KafkaTopic` control-plane resources.
- Data-plane operations use a Kafka client against the Strimzi bootstrap service.
- The backend does not maintain a sidecar topic or offset map. Kafka/Strimzi own
  the log and consumer offsets.

## 4. Frontend / Driver Matrix

| Frontend | SDK | CLI | Terraform |
|---|---|---|---|
| AWS MSK | `github.com/aws/aws-sdk-go-v2/service/kafka` + Kafka client | `aws kafka` + Kafka CLI | `hashicorp/aws` MSK cluster resources plus Kafka provider where needed |
| GCP Managed Kafka | `google.golang.org/api/managedkafka/v1` or generated client | `gcloud managed-kafka` | `hashicorp/google` managed Kafka resources |
| Azure Event Hubs | `github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub` + Kafka client | `az eventhubs` | `hashicorp/azurerm` Event Hubs resources |

For data-plane conformance, use a real Kafka client surface in addition to the
cloud control-plane tools. Go should be canonical for SDK data-plane tests; CLI
coverage can use Kafka's own CLI tools or a small Go driver where cloud CLIs do
not expose Kafka produce/fetch.

## 5. Auth And Error Honesty

Control-plane auth follows the existing verifier pattern:

- AWS MSK control plane: SigV4.
- GCP Managed Kafka control plane: GCP Bearer.
- Azure Event Hubs control plane: Azure Bearer / ARM problem-details.

Kafka data-plane auth is more nuanced and should not be faked:

- AWS MSK supports multiple auth modes; the first implementation should choose
  one explicitly and reject unsupported control-plane modes with the source
  cloud's error shape and unsupported Kafka data-plane modes with Kafka protocol
  errors.
- Azure Event Hubs Kafka commonly uses SASL/PLAIN with the namespace connection
  string or OAuth. Generated SAS tokens are not supported for the Kafka endpoint
  per Microsoft docs, so do not accept them as a hidden fallback.
- GCP Managed Kafka auth must follow the published client/bootstrap behavior when
  implemented.

If a data-plane auth mode cannot be verified honestly in the shim, that mode is
out of the first intersection.

## 6. Codegen And Spec Inputs

| Cloud | Spec input | Generator |
|---|---|---|
| AWS MSK | Smithy JSON from `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models/kafka.json` | `cmd/codegen` restJson lane |
| GCP Managed Kafka | Discovery document at `https://managedkafka.googleapis.com/$discovery/rest?version=v1` | `cmd/gcp-codegen` |
| Azure Event Hubs | Azure REST API specs for `Microsoft.EventHub` | `cmd/azure-codegen` |
| Kafka data plane | Apache Kafka protocol definitions / `franz-go` generated protocol package | hand-written runtime using generated protocol types |
| Strimzi | Kubernetes CRDs installed by Strimzi | dynamic client or vendored typed client only after license/release-age check |

Do not vendor Kafka protocol JSON or generated code until the first
implementation PR decides the library. The scoping PR records the decision path
only.

## 7. Sub-Phase Plan

| Track | Scope | Exit criteria |
|---|---|---|
| 20.A | Scoping (this doc), `INTERSECTION.md`, domain interfaces, inmem append-only log, Kafka wire runtime skeleton | Go Kafka client can produce/fetch against inmem through the shim for one topic/partition |
| 20.B | Topic lifecycle and minimal data plane behind first frontend, likely GCP because its REST topic API is explicit | SDK + Kafka client conformance for create topic, produce, fetch, delete |
| 20.C | AWS MSK frontend/control plane and Kafka admin mapping for topic lifecycle | AWS SDK/CLI/Terraform control-plane rows plus Kafka data-plane row |
| 20.D | Azure Event Hubs frontend/control plane and Kafka endpoint auth mapping | Azure SDK/CLI/Terraform control-plane rows plus Kafka data-plane row |
| 20.E | Strimzi connected backend and full matrix closeout | K8s peer uses real Kafka; no shim-owned log state outside the backend |

## 8. Kafka Runtime Dependency Decision

Use `github.com/twmb/franz-go/pkg/kmsg` for generated Kafka request/response
types.

Decision checks:

- `kmsg` is pure Go and exposes generated request/response bodies with
  `ReadFrom` / `AppendTo`; the shim still owns TCP framing and dispatch.
- Version `v1.13.1` was published on 2026-04-06, clearing the 48-hour release-age
  rule in `docs/dependency-policy.md`.
- License is BSD-3-Clause, which is allowlisted in
  `docs/compatible-licenses.md`.
- The package is a protocol-type dependency, not a fake broker. It does not
  create successful Kafka behavior by itself.

Do not add `kfake` or any test-only broker abstraction to the shim runtime. Tests
may use official Kafka clients to drive the shim, but the shim's server path must
decode real Kafka frames and either perform real backend work or return honest
Kafka/source-cloud errors.

## 9. Open Questions For 20.B

- Should the first data-plane conformance test use `franz-go/pkg/kgo`, the
  Apache Kafka CLI, or both?
- Which Kafka API versions should the shim advertise in `ApiVersions` for the
  initial implementation? Prefer the smallest set that official clients accept.
- Is consumer-group support required in the first data-plane PR, or can the first
  PR prove produce/fetch and leave groups for the next slice? If deferred,
  clients that require group coordination must receive honest Kafka errors, not
  fake group success.

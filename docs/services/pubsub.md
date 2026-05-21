# Pub/sub

Fanout messaging: one producer, every subscription receives every message. Compare to [queue](queue.md), which is point-to-point.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS SNS + slim SQS-receive | awsQuery XML (SNS) + awsJson1_0 (SQS receive) | SNS publish for write; minimal SQS receive surface for fanout-to-queue read. |
| GCP Pub/Sub (fanout) | REST + JSON | Topic + subscription model native. |
| Azure Service Bus topics | REST | Per-sub session optional. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real SNS + SQS receive | Passthrough. |
| `gcp` | Real GCP Pub/Sub | Passthrough. |
| `azure` | Real Azure Service Bus topics | Passthrough. |
| `nats` | NATS JetStream + core | The K8s peer. `Durable=true` → JetStream durable consumer; `Durable=false` → core NATS fanout. |
| `inmem` | Process-local | Tests + local dev. |

## Topic vs Subscription

Pub/sub has *two* resource families (Topic + Subscription) where queue has one. Domain reflects this:

```go
CreateTopic(name) / DeleteTopic(name) / ListTopics / HeadTopic
CreateSubscription(topic, sub) / DeleteSubscription(sub) / ListSubscriptions / HeadSubscription
Publish(topic, opt) → fans out to every Subscription on that Topic
Receive(sub, opt) / Ack(sub, handle) / ChangeVisibility(sub, handle, sec)
```

## Intersection contracts

- **[`services/pubsub/OPERATIONS.md`](../../services/pubsub/OPERATIONS.md)** — operation list.
- **[`services/pubsub/INTERSECTION.md`](../../services/pubsub/INTERSECTION.md)** — per-frontend classification.
- **[`services/pubsub/APPLY_INTERSECTION.md`](../../services/pubsub/APPLY_INTERSECTION.md)** — Apply contract:
  - Topic Create: `name`. Everything else (FIFO, KMS, archive_policy, message_retention_duration cross-cloud) is out-of-contract.
  - Subscription Create: `topic`, `name`, `ack_deadline_seconds`. `durable=false` against always-durable backends (AWS/GCP/Azure) returns `InvalidParameter`. Filter policies / push subscriptions / dead-letter all out-of-contract.

## AWS SNS SetTopicAttributes

The hashicorp/aws `aws_sns_topic` resource issues `SetTopicAttributes` for each schema-default attribute after CreateTopic. The shim's AWS SNS frontend accepts known-acceptable attribute names as no-ops (matching real-SNS behavior since the shim's `GetTopicAttributes` already echoes the canonical defaults the provider checks against — Policy default-statement, EffectiveDeliveryPolicy default-object, DisplayName empty, etc). For unknown attribute names with non-default values, returns honest `InvalidParameter`.

## Conformance

- `TestPubsubMatrix_*` — (frontend × backend × driver) cells.
- `TestTerraform_AWSPubsub_Apply_NoDrift` — AWS SNS topic Apply through inmem.
- `TestCrossCloudImport_Roundtrip_PubsubAWStoGCP` (Phase 9.13).
- `TestCrossCloudApply_Roundtrip_PubsubAWStoGCP` (Phase 10.7) — documented-skip (WaitForStateEqual cross-cloud asymmetry, same shape as queue).

## Known gaps

- `aws_sns_topic_subscription` with SQS endpoint: the queue-side `SetQueueAttributes` gap (BUG-2) closed in Phase 10.3, but the pubsub frontend deliberately omits the full SQS-admin surface (the cross-cloud pubsub intersection is "publish + receive," not "operate a queue from the pubsub plane"). Cell remains documented-skip until the pubsub frontend exposes that surface or fixtures wire `aws_sqs_queue` explicitly.
- Filter policies, dead-letter, push subscriptions all out-of-contract.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/pubsub/MIGRATION.md](../../services/pubsub/MIGRATION.md)
- Related: [docs/services/queue.md](queue.md) (point-to-point counterpart).

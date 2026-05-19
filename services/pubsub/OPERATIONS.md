# Pub/Sub — operation and feature mapping

> The intersection footprint shimanism's `pubsub` service can cover, across the four backends in scope:
> **AWS SNS** (with SQS-shaped delivery endpoints), **GCP Pub/Sub** (multi-subscription fanout), **Azure Service Bus topics**, **NATS core** (subject-based pub/sub, not JetStream) as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle).
>
> The shim itself is stateless — topics, subscriptions, and delivery state live in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## Pub/Sub vs Queue — what's different from Phase 3

Phase 3's `queue` service is **point-to-point**: one queue, many consumers, each message goes to exactly one consumer. Phase 4's `pubsub` service is **fanout**: one topic, many subscriptions, each subscription receives a copy of every message published to the topic.

Two consequences for the domain model:

1. **Topic ≠ Subscription.** A topic accepts publishes; a subscription holds a per-consumer delivery queue. Many subscriptions can be attached to one topic.
2. **Receive is per-subscription.** `Receive(topic, ...)` is meaningless; `Receive(subscription, ...)` is the contract. Each subscription has its own ack-deadline / visibility-timeout.

The K8s peer choice differs accordingly: Phase 3 uses **NATS JetStream** (persistent, ack-tracked) for queue semantics; Phase 4 uses **NATS core** (in-memory, fan-out by subject pattern) for pub/sub semantics. (When durable subscriptions are needed on the NATS peer, JetStream consumers act as "subscribers" attached to a stream — covered as an extension below.)

## The intersection — 10 operations

The set of operations every backend supports in roughly equivalent form. These are the only ops the shim implements; the AWS/GCP/Azure frontend codegen scopes its manifest to this list.

| Domain op | AWS SNS / SQS | GCP Pub/Sub | Azure Service Bus topics | NATS core (+ JetStream for durable subs) |
|---|---|---|---|---|
| **CreateTopic**(name) | `SNS.CreateTopic` | `Publisher.CreateTopic` | `TopicAdmin.CreateTopic` | `JetStream.AddStream` (subjects = topic) |
| **DeleteTopic**(name) | `SNS.DeleteTopic` | `Publisher.DeleteTopic` | `TopicAdmin.DeleteTopic` | `JetStream.DeleteStream` |
| **ListTopics**(prefix?) | `SNS.ListTopics` | `Publisher.ListTopics` | `TopicAdmin.ListTopics` | `JetStream.StreamNames` |
| **CreateSubscription**(topic, sub, opts) | `SNS.Subscribe(SQS endpoint)` — backing SQS queue auto-created by the shim | `Subscriber.CreateSubscription` | `SubscriptionAdmin.CreateSubscription` | `JetStream.AddConsumer` (filter subject = topic) |
| **DeleteSubscription**(sub) | `SNS.Unsubscribe` + `SQS.DeleteQueue` | `Subscriber.DeleteSubscription` | `SubscriptionAdmin.DeleteSubscription` | `JetStream.DeleteConsumer` |
| **ListSubscriptions**(topic?) | `SNS.ListSubscriptionsByTopic` | `Subscriber.ListSubscriptions` | `SubscriptionAdmin.ListSubscriptions` | `JetStream.ConsumersInfo` (filter by stream) |
| **Publish**(topic, body, attrs) | `SNS.Publish` (fanned out to all subs) | `Publisher.Publish` | `Sender.SendMessage` (to topic) | `JetStream.Publish` (fanned out by subject) |
| **Receive**(sub, opts) | `SQS.ReceiveMessage` (on the backing queue) | `Subscriber.Pull` | `Receiver.Receive` (subscription-scoped) | `Consumer.Fetch` |
| **Ack**(sub, receipt) | `SQS.DeleteMessage` | `Subscriber.Acknowledge` | `Receiver.Complete` | `Msg.Ack` |
| **ChangeVisibility**(sub, receipt, timeout) | `SQS.ChangeMessageVisibility` | `Subscriber.ModifyAckDeadline` | `Receiver.RenewMessageLock` | `Msg.InProgress` |

The AWS frontend exposes SNS for the publish side; the receive side is exposed via the existing Phase 3 SQS frontend, since SNS subscriptions deliver to SQS queues. When a Phase 4 `CreateSubscription` lands on the AWS frontend, the shim creates a backing SQS queue (Phase 3 inmem semantics for the inmem backend, or a real SQS queue against the AWS backend) and registers the SNS subscription with `Protocol="sqs"` and `Endpoint=<arn-of-backing-queue>`.

A multi-call sequence in one cloud counts as a single domain op when the second call is mechanical (AWS's "Subscribe→SQS endpoint" pair, Azure's "topic in a namespace already created"). The shim's frontend adapter orchestrates whatever the cloud requires so the domain op is atomic from the caller's perspective.

## Receipt handles

Carries forward Phase 3's design: opaque shim-side strings, no shim-side index, native ↔ opaque mapping in each backend adapter.

| Cloud | Native receipt token |
|---|---|
| AWS SNS (delivery via SQS) | `SQS.ReceiptHandle` (passes through) |
| GCP Pub/Sub | `AckId` (passes through) |
| Azure Service Bus | `<messageID>|<lockToken>` composite (same encoding as Phase 3) |
| NATS core | not applicable — core NATS doesn't ack; durable subs use JetStream reply subject (same as Phase 3) |

## Message attributes

Same `map[string]string` largest-common-denominator rule as Phase 3. Typed AWS / Azure attributes get coerced to strings; types outside `String` are out of intersection.

## What's emphatically out of intersection

These are real per-cloud features that don't translate cleanly:

- **AWS SNS:** FIFO topics, message filtering (`FilterPolicy`), encryption (`KmsMasterKeyId`), HTTP/HTTPS/Lambda/email/SMS subscription protocols (only SQS-protocol subscriptions are in scope; others have no GCP/Azure/NATS equivalent), delivery retry policies, dead-letter topics.
- **GCP Pub/Sub:** ordering keys, push subscriptions, exactly-once delivery, BigQuery / Cloud Storage subscription types, schema registries, filters.
- **Azure Service Bus topics:** topic subscription filters/rules, dead-letter forwarding, auto-forwarding chains, sessions, duplicate detection.
- **NATS:** mirror streams, replay policies, KV-as-subscription patterns.

## Backend choice notes

- **NATS core for fanout.** Subject-based publish/subscribe is true fanout — every subscriber to a subject receives every message published to it. This matches the pub/sub model exactly. The trade-off: core NATS is in-memory and non-durable; subscribers receive only messages published while they're connected.
- **NATS JetStream for durable subscriptions.** When the domain `CreateSubscription` is called with a durable flag, the K8s peer uses JetStream consumers attached to a stream (carries the Phase 3 ack semantics). The shim's NATS backend toggles between core and JetStream depending on the subscription type at create time.
- **AWS dual-protocol surface.** The AWS frontend speaks SNS for publish, SQS for receive. Subscriptions in the shim's domain map 1:1 to SNS subscriptions with a backing SQS queue; the receive side reuses Phase 3's SQS handler unchanged.

## Sub-phase plan (Phase 4)

| Sub | Headline |
|---|---|
| 4.0 | Scope + intersection mapping (this doc) + sub-phase plan. |
| 4.1 | Vendor AWS SNS Smithy spec. |
| 4.2 | Domain interface (`internal/pubsub/domain/`): `Topics`, `Subscriptions`, `Publish`, `Receive`, `Ack`, `ChangeVisibility`. Opaque receipt handles. Caps mirror Phase 3 (600s visibility, 20s wait). |
| 4.3 | inmem backend + AWS SNS frontend (publish side) + SDK conformance for the SNS publish + SQS receive flow. |
| 4.4 | NATS-core backend (K8s peer) with optional JetStream-consumer durable subs. |
| 4.5 | AWS SNS+SQS passthrough backend. |
| 4.6 | GCP Pub/Sub fanout backend (multi-subscription). |
| 4.7 | Azure Service Bus topics backend. |
| 4.8 | GCP Pub/Sub frontend (fanout-aware: separate topic + subscription resource paths). |
| 4.9 | Azure Service Bus topics REST frontend. |
| 4.10 | Conformance matrix (`TestPubsubMatrix_*`) iterating every backend factory. |
| 4.11 | CLI conformance — `aws sns`, `gcloud pubsub topics+subscriptions`, `az servicebus topic`. |
| 4.12 | Terraform conformance where the provider admits endpoint override. |
| 4.13 | `cmd/shim pubsub` subcommand (default :9300). |
| 4.14 | CI lane `conformance-nats-core` (or reuse `conformance-nats` if the same NATS container covers both). |
| 4.15 | Phase 4 closer: matrix green; documented skips; STATUS / DO_NEXT / WHAT_WE_DID refreshed. |

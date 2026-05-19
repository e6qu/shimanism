# Queue — operation and feature mapping

> The intersection footprint shimanism's `queue` service can cover, across the four backends in scope:
> **AWS SQS**, **GCP Pub/Sub (pull mode)**, **Azure Service Bus queue**, **NATS JetStream** as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle) for why.
>
> The shim itself is stateless — every message, queue, and visibility-extension lives in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## The intersection — 8 operations

The set of operations every backend supports in roughly equivalent form. These are the only ops the shim implements; the AWS/GCP/Azure frontend codegen scopes its manifest to this list.

| Domain op | AWS SQS | GCP Pub/Sub (pull) | Azure Service Bus | NATS JetStream |
|---|---|---|---|---|
| **CreateQueue**(name, opts) | `CreateQueue` (attributes include VisibilityTimeout, MessageRetentionPeriod) | `CreateTopic` + `CreateSubscription` (the shim creates both in one call) | `CreateQueue` (LockDuration, MaxDeliveryCount) | `JetStream.AddStream` + `AddConsumer` (pull consumer) |
| **DeleteQueue**(name) | `DeleteQueue` | `DeleteSubscription` + `DeleteTopic` | `DeleteQueue` | `JetStream.DeleteStream` |
| **ListQueues**(prefix?) | `ListQueues` (filter on prefix) | `ListTopics` (subscription per topic) | `ListQueues` | `JetStream.StreamNames` |
| **SendMessage**(queue, body, attrs) | `SendMessage` | `Publish` to the topic | `SendMessage` | `JetStream.Publish` |
| **ReceiveMessages**(queue, opts) | `ReceiveMessage` (MaxNumberOfMessages, WaitTimeSeconds, VisibilityTimeout) | `Subscription.Pull` (return up to N) | `Receiver.Receive` (PeekLock mode) | `Consumer.Fetch(N)` |
| **DeleteMessage**(queue, receipt) | `DeleteMessage` (ReceiptHandle) | `Subscription.Acknowledge` (AckId) | `Receiver.Complete` (LockToken) | `Msg.Ack` |
| **ChangeVisibility**(queue, receipt, timeout) | `ChangeMessageVisibility` (ReceiptHandle, VisibilityTimeout) | `ModifyAckDeadline` (AckId, AckDeadlineSeconds) | `Receiver.RenewMessageLock` | `Msg.InProgress` (with deadline; NATS uses different model — see version mapping below) |
| **GetQueueAttributes**(queue) | `GetQueueAttributes` (ApproximateNumberOfMessages, …) | `Subscription.Get` | `GetQueueRuntimeProperties` | `JetStream.StreamInfo` + `ConsumerInfo` |

A multi-call sequence in one cloud counts as a single domain op when the second call is mechanical (GCP's "topic + subscription as a pair", Azure's "queue create needs a namespace it's already in"). The shim's frontend adapter orchestrates whatever the cloud requires so the domain op is atomic from the caller's perspective.

## Receipt handles

The four systems hand out different opaque tokens after a receive that the consumer must present back to ack / extend / delete. The domain uses **opaque string** receipt handles. Each backend adapter maps native ↔ opaque.

| Cloud | Native receipt token | Lifetime |
|---|---|---|
| AWS SQS | `ReceiptHandle` (base64-encoded string, ~200 chars) | until visibility timeout expires |
| GCP Pub/Sub | `AckId` (string) | until ack deadline expires |
| Azure Service Bus | `LockToken` (UUID) + message reference | until lock duration expires |
| NATS JetStream | message reply subject (per-message) | until ack timeout |

**Domain rule:** the shim emits an opaque token. For AWS and GCP, the token IS the native handle (passed through unchanged). For Azure, the token is a `<base64-of-message-id>:<lock-token>` composite so the adapter can re-find the message reference without shim-side state. For NATS, the token is the message's `reply_subject` (NATS-native; round-trips without translation).

The shim doesn't index receipt handles. Each Delete/ChangeVisibility/etc. call talks directly to the backend with the native token reconstructed from the opaque shim handle.

## Message attributes

Every backend supports per-message user metadata, in different shapes:

| Cloud | Attribute container | Type system |
|---|---|---|
| AWS SQS | `MessageAttributes` | named values, each typed (`String`, `Number`, `Binary`) |
| GCP Pub/Sub | `attributes` | flat `map<string, string>` |
| Azure Service Bus | `ApplicationProperties` | named values, each a typed scalar |
| NATS JetStream | message `Header` | flat `map<string, string>` (similar to HTTP headers) |

**Domain rule:** attributes are `map[string]string` (the largest common denominator). AWS SQS' typed attributes get coerced to strings (Number+Binary types are out of intersection at this phase — return source cloud's `MissingRequiredParameter` / `InvalidParameterValue` if a typed attribute can't round-trip). Azure's typed properties same treatment.

## Visibility / ack-deadline semantics

| Cloud | Default lock | Configurable per-receive? | Max | On expiry |
|---|---|---|---|---|
| AWS SQS | 30s | yes (`VisibilityTimeout`) | 12h | message re-becomes visible |
| GCP Pub/Sub | per-subscription `AckDeadlineSeconds` (default 10s, max 600s) | not per-receive; modify via `ModifyAckDeadline` after receive | 600s (10m) | re-delivered |
| Azure Service Bus | per-queue `LockDuration` | extend via `RenewMessageLock`; not per-receive | 5m | unlock + re-deliver |
| NATS JetStream | per-consumer `AckWait` | extend via `Msg.InProgress` | unbounded | re-delivered |

**Domain rule:** `ReceiveMessages(opts)` accepts a `VisibilityTimeout int` (seconds) that overrides the queue default when the cloud supports it. GCP and Azure don't honour per-receive overrides — the override is silently ignored on those backends (documented; not an error, because the queue default is still applied). The 10-minute GCP cap is the most restrictive; the shim's domain caps at 600 seconds on all backends to keep behaviour uniform.

## Wait time (long polling)

| Cloud | Per-receive wait | Max | Semantics |
|---|---|---|---|
| AWS SQS | `WaitTimeSeconds` | 20s | block up to N seconds for new messages |
| GCP Pub/Sub | `ReturnImmediately=false` + pulling SDK timeout | none | streaming pull is the recommended path; pull has a 90s soft limit |
| Azure Service Bus | `MaxWaitTime` | 240s | block up to MaxWaitTime |
| NATS JetStream | `Fetch.Expires` | unbounded | block until expiry |

**Domain rule:** `ReceiveMessages(opts)` accepts a `WaitTime int` (seconds). The shim caps at AWS's 20s to keep behaviour uniform across backends. Backends that don't natively support per-receive wait (GCP non-streaming) busy-poll up to the budget; backends with native support pass through.

## What's emphatically out of intersection

These are real per-cloud features, but they don't translate to a meaningful equivalent on the other clouds. When a request targets one of these, the shim returns the source cloud's own "not supported" error.

**AWS SQS only:**
- FIFO queues (`.fifo` suffix + `MessageGroupId` + `MessageDeduplicationId`)
- Dead-letter queues (`RedrivePolicy`)
- Server-side encryption with KMS keys
- Queue policies (resource-based access control)
- Long-polling receive with `ReceiveMessageWaitTimeSeconds` queue-default
- Message timers (`DelaySeconds`)

**GCP Pub/Sub only:**
- Ordering keys (FIFO-ish per-key)
- Filters (subscription-side message filtering)
- Dead-letter topics
- Retry policies (exponential backoff config)
- Push subscriptions (push delivery; only pull is in scope)
- Exactly-once delivery semantics
- BigQuery subscriptions / Cloud Storage subscriptions (out-of-band sinks)
- Snapshots / seek-to-time
- Schema registry

**Azure Service Bus only:**
- Sessions (FIFO per session ID)
- Duplicate detection
- Dead-letter queue + max-delivery-count
- Scheduled messages (`ScheduledEnqueueTimeUtc`)
- Auto-forwarding to another queue
- Topic + subscription model with SQL filter rules (vs the plain queue we shim)
- Partitioned queues
- Premium namespace features (geo-disaster recovery, etc.)

**NATS JetStream only:**
- Subject hierarchies + wildcards
- All non-pull-consumer types (push, ordered-push, deliver-all, deliver-last, etc.)
- Replay policies (instant / original)
- Mirror + sourced streams
- KV / Object stores within JetStream (those belong to other shim services)

## What's emphatically not a shim

Any **control-plane / IAM** operation belongs in a separate phase. The queue shim covers the data-plane surface only:

- AWS SQS queue policies (`SetQueueAttributes` with `Policy` attribute)
- GCP IAM bindings on the subscription / topic
- Azure Service Bus authorization rules / managed identity binding
- NATS auth tokens / accounts

The shim accepts requests that carry the cloud's auth headers but does not validate them at this phase, matching the Phase 1 + 2 posture.

## Mapping summary

| Capability | Coverage |
|---|---|
| Send / receive / ack round-trip across all 4 backends | ✓ |
| Visibility / ack-deadline extension | ✓ (capped at 600s for uniformity) |
| Per-message string attributes | ✓ |
| Per-receive wait time (long polling) | ✓ (capped at 20s) |
| Bulk send / receive batches | ✓ (cap at 10 messages, matching AWS SQS max) |
| FIFO ordering | ✗ (per-cloud semantics diverge too far) |
| Dead-letter queues | ✗ (each cloud's DLQ model is distinct enough to be its own future phase) |
| Encryption-config / KMS | ✗ (cloud-specific) |
| Push delivery | ✗ (only pull-mode is in scope) |
| IAM / queue policies | ✗ (separate phase) |

The **8-op intersection** with the receipt-handle + attributes + visibility-extension primitives covers the standard producer-consumer pattern that 90 %+ of real queue usage maps to.

# Pub/Sub — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md).

## Resource scope

Pub/sub has **two resource families** — Topic and Subscription — unlike queue's single family.

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_sns_topic` | AWS `CreateTopic` / `GetTopicAttributes` / `SetTopicAttributes` / `DeleteTopic` | `CreateTopic` / `HeadTopic` / (Set — partially out) / `DeleteTopic` |
| `aws_sns_topic_subscription` | AWS `Subscribe` / `GetSubscriptionAttributes` / `Unsubscribe` | `CreateSubscription` / `HeadSubscription` / `DeleteSubscription` |
| `google_pubsub_topic` | GCP `topics.create/get/patch/delete` | `CreateTopic` / `HeadTopic` / (patch limited) / `DeleteTopic` |
| `google_pubsub_subscription` (push or pull) | GCP `subscriptions.create/get/patch/delete` | `CreateSubscription` / `HeadSubscription` / (patch limited) / `DeleteSubscription` |
| `azurerm_servicebus_topic` | Azure `Create Topic` / `Get Topic` / `Update Topic` / `Delete Topic` | same |
| `azurerm_servicebus_subscription` | Azure `Create Subscription` / ... | same |

## Apply contract — topic resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `name` | ✅ | All backends. |
| `fifo_topic` (AWS), `content_based_deduplication` (AWS) | ◇ | Strict ordering + dedup is AWS-shape-only. Out of intersection; shim returns `OperationNotSupportedException` against non-AWS. AWS-to-AWS passthrough honors. |
| `kms_master_key_id` / `kms_*` / encryption-at-rest | ◇ | Per-backend; not translated. Out of contract. |
| `signature_version`, `tracing_config`, `archive_policy`, `application_failure_feedback_role_arn`, etc. (AWS) | ◇ | AWS-specific. Out of contract. |
| `tags` / `labels` | ⚠ | **No write path through the domain today** — `domain.CreateTopicOptions.Attributes` is reserved for forward-compat. Same posture as queue (BUG-12-shaped). Phase 10.3 candidate. |
| `message_retention_duration` (GCP topic) | ⚠ | GCP-only; AWS / Azure / NATS don't have topic-level retention. Out of intersection cross-cloud. GCP-to-GCP passthrough honors; others return `OperationNotSupportedException`. |
| `schema_settings`, `ingestion_data_source_settings` (GCP) | ◇ | GCP-only. Out of contract. |

### Update — topic

`SetTopicAttributes` and GCP `topics.patch` — same posture as queue. Most attributes are immutable cross-cloud:

- `name`: `ForceNew` everywhere.
- `tags` / `labels`: blocked by domain gap (no write path). Phase 10.3 candidate.

There's no clean in-place update for topic-level attributes in the current intersection. **Apply Update for topics returns "no changes" if HCL is unchanged, or `OperationNotSupportedException` if HCL changes an out-of-contract attribute.**

### Delete — topic

`DeleteTopic` removes the topic and **all its subscriptions** (per domain contract: "Returns NoSuchTopic if the topic doesn't exist"; the four backends all cascade-delete subscriptions on topic delete). Synchronous everywhere.

## Apply contract — subscription resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `topic_arn` / `topic` (parent) | ✅ | All backends. Returns `NoSuchTopic` if parent doesn't exist. |
| `name` (sub name) | ✅ | All backends. |
| `ack_deadline_seconds` / `visibility_timeout_seconds` | ✅ | `domain.Subscription.AckDeadlineSeconds`. Capped at 600s by domain. |
| `durable` (NATS-aware) | ⚠ | Used by NATS adapter to toggle between core fanout (Durable=false) and JetStream consumers (Durable=true). AWS / GCP / Azure subscriptions are always durable; the field is silently ignored on those backends (per domain doc). Provider HCL that sets `durable = false` against AWS / GCP / Azure backends is **honored** (because those backends are always durable, "durable=false" cannot be honored, so the shim returns the source cloud's `InvalidParameter` envelope rather than silently accepting). |
| `endpoint` / `protocol` (AWS SNS push) | ◇ | AWS-specific push subscriptions (email / SMS / HTTP / SQS / Lambda). Not in the pull-fanout intersection. Out of contract. AWS-to-AWS passthrough may honor through Track A. |
| `filter_policy` (AWS), `filter` (GCP) | ◇ | Message filtering. Per-backend syntax differs materially (AWS JSON DSL vs GCP CEL-like). Out of contract; honest translation requires its own phase. |
| `dead_letter_policy` / `redrive_policy` | ◇ | Out of contract. |
| `push_config` (GCP) | ◇ | GCP push subscriptions. Not in pull-fanout intersection. Out of contract. |
| `message_retention_duration` (GCP), `retain_acked_messages` (GCP) | ◇ | GCP-specific subscription semantics. Out of contract. |
| `enable_message_ordering` (GCP) | ◇ | Out of contract (ordering keys are GCP-shaped). |
| `expiration_policy` (GCP) | ◇ | GCP-only auto-cleanup. Out of contract. |

### Update — subscription

In-place across all backends:

- `ack_deadline_seconds` / `visibility_timeout_seconds` — honored.
- `durable` — `ForceNew` across NATS (toggling between core and JetStream is recreation, not patch); ignored / InvalidParameter elsewhere as above.

ForceNew everywhere:
- `topic_arn` / `topic` (reparenting).
- `name`.
- `durable` against NATS backend.

### Delete — subscription

`DeleteSubscription` synchronous everywhere. Removes from the backend's delivery list; in-flight messages handled per backend (AWS: returned to topic for redelivery on other subs; GCP: discarded; Azure: discarded; NATS: discarded).

### `aws_sns_topic_subscription` BUG-2 ripple

The AWS SNS frontend's subscription create path requires `SetQueueAttributes` (BUG-2) when subscribing to an SQS endpoint via shim → AWS SNS topic → shim queue. Phase 9 ◇-skipped this cell. Phase 10 keeps the skip until 10.3 closes BUG-2.

## Out of contract

- All push-subscription configs (AWS protocol/endpoint, GCP push_config, Azure forwarding).
- Filter policies (both AWS and GCP).
- Dead-letter / redrive.
- Schema / ingestion (GCP).
- Resource-policy / topic-policy (AWS).
- Encryption-at-rest config.
- AWS SNS FIFO.

## What this contract commits the shim to

1. Accept the in-contract Create attributes for topic + subscription; round-trip through Read with no drift on all five backend cells.
2. Reject out-of-contract attributes with the source cloud's real error envelope.
3. Reject `durable = false` against AWS / GCP / Azure backends with `InvalidParameter` (honest — those backends can't honor non-durable).
4. Reject `fifo_topic = true` against non-AWS backends with `OperationNotSupportedException`.
5. Reject GCP `message_retention_duration` against non-GCP backends with `OperationNotSupportedException`.
6. Update path: subscription `ack_deadline_seconds` in-place; everything else is `ForceNew` or out-of-contract.
7. Delete cascades: topic delete removes all subscriptions atomically across the intersection.

## Known open BUGs gating this contract

- [BUG-2](../../BUGS.md): blocks `aws_sns_topic_subscription` cells that pair an SNS topic with an SQS endpoint via shim. Same skip-with-pointer posture until 10.3.
- [BUG-12](../../BUGS.md): blocks topic-level tag write.

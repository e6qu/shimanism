# Queue — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md).

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_sqs_queue` | AWS `CreateQueue` / `GetQueueUrl` / `GetQueueAttributes` / `SetQueueAttributes` / `DeleteQueue` | `CreateQueue` / `HeadQueue` / **see BUG-2** / `DeleteQueue` |
| `google_pubsub_subscription` (when configured pull-style) | GCP `subscriptions.create/get/patch/delete` | `CreateQueue` / `HeadQueue` / (patch) / `DeleteQueue` |
| `azurerm_servicebus_queue` | Azure `Create Queue` / `Get Queue` / `Update Queue` / `Delete Queue` | same |

Out of scope: AWS dead-letter queue policy (`aws_sqs_queue_redrive_policy` / `redrive_allow_policy`), Azure forward-to + duplicate-detection windows, GCP push-config subscriptions (these are Pub/Sub, in services/pubsub).

## Apply contract — queue resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `name` | ✅ | All backends. AWS FIFO suffix (`.fifo`) → backends must reject if they can't honor FIFO; see fidelity note. |
| `visibility_timeout_seconds` | ✅ | `domain.QueueAttributes.VisibilityTimeoutSeconds`. AWS / GCP / Azure / NATS all honor; defaults differ (0 means "use backend default"). |
| `message_retention_seconds` | ⚠ | AWS / Azure honor natively. GCP Pub/Sub: subscription-side retention is *separate* from message lifetime — the shim maps to `messageRetentionDuration` on the Pub/Sub subscription, which is **semantically close, not identical** (GCP retention is from publish time; AWS / Azure is from enqueue). NATS: stream-level retention. Cross-cloud fidelity tradeoff is documented per cell; not faked. |
| `max_message_size` | ✅ | Cross-cloud floor is 256 KiB (AWS limit). Backends accepting smaller caps surface their native error if HCL declares higher. |
| `delay_seconds` | ⚠ | AWS / Azure honor natively. GCP + NATS treat as 0; **shim returns `OperationNotSupportedException` if HCL declares `delay_seconds > 0` against a GCP or NATS backend** (rather than silently dropping). |
| FIFO attributes (`fifo_queue`, `content_based_deduplication`, `deduplication_scope`, `fifo_throughput_limit`) | ◇ | AWS-specific ordering semantics. Cross-cloud FIFO is **out of intersection for Phase 10**; shim returns `OperationNotSupportedException` if HCL declares `fifo_queue = true` against a non-AWS backend. AWS frontend → AWS backend honors. |
| `kms_master_key_id` / `kms_data_key_reuse_period_seconds` / `sqs_managed_sse_enabled` | ◇ | Encryption-at-rest config. Per-backend; not translated. Out of contract. |
| `tags` | ⚠ | **See BUG-12.** Tag *read* is wired honest-empty so import works; tag *write* paths aren't backed in the queue domain. Out of contract for Phase 10 unless 10.3 fixes BUG-12. |
| `policy` (AWS resource-policy JSON) | ◇ | AWS-specific. Out of contract. |
| `redrive_policy` / `redrive_allow_policy` | ◇ | Dead-letter is AWS-shape-only across the intersection. Out of contract. |

### FIFO fidelity note

AWS SQS FIFO queues guarantee strict per-`MessageGroupId` ordering + exactly-once-via-deduplication. GCP Pub/Sub has ordering keys but not the same exactly-once contract; Azure Service Bus has sessions; NATS JetStream has `OrderedConsumer`. Each is *similar but not identical*. **Decision:** AWS FIFO is out of intersection for Phase 10 Apply. Frontend HCL with `fifo_queue = true` against any non-AWS-passthrough backend returns the source cloud's `OperationNotSupportedException` (or 400 with reason for GCP). AWS-to-AWS passthrough honors.

### Update

`SetQueueAttributes` is the AWS-shape Update path. **Blocked by BUG-2** — not wired through the domain or any backend as of Phase 10.0-A. Until BUG-2 closes (candidate scope for 10.3):

- Provider HCL changes to `visibility_timeout_seconds`, `message_retention_seconds`, `max_message_size`, `delay_seconds` cause Terraform to plan an update, the shim returns the source cloud's `InvalidParameterValue` (or `OperationNotSupportedException`) envelope.
- **Tests for queue Apply Update are ◇-skipped with a pointer to BUG-2** until it closes. This is honest; matches Phase 9's posture.

`name` is `ForceNew` everywhere.

### Delete

`DeleteQueue` synchronous across the intersection. Empty-vs-non-empty semantics differ:

- AWS: deletes immediately; in-flight messages discarded.
- GCP: deleting a subscription strands the messages on the parent topic (Pub/Sub semantics). HCL-driven destroy is honest; the user should understand the asymmetry.
- Azure: deletes the queue and all messages.
- NATS: deletes the stream (and consumers).

No async polling. Synchronous everywhere.

## Apply contract — soft-delete

**Dropped per codex review.** Queues don't have a peer concept across AWS / GCP / Azure / NATS. Phase 10 explicitly does not include queue soft-delete in its scope.

## Out of contract

- `aws_sqs_queue_redrive_policy`, `aws_sqs_queue_redrive_allow_policy`.
- `aws_sqs_queue_policy` (resource-based IAM policy).
- `google_pubsub_subscription` push-config (handled in services/pubsub).
- Azure session-enabled queues + duplicate-detection windows.
- All encryption-at-rest config.

## What this contract commits the shim to

1. Accept the in-contract Create attributes; round-trip through Read with no drift on AWS / GCP / Azure / inmem / NATS cells where the attribute is honored natively.
2. **Return `OperationNotSupportedException` for cells where an in-contract attribute can't be honored honestly** (`delay_seconds > 0` against GCP / NATS; `fifo_queue = true` against any non-AWS).
3. Reject out-of-contract attributes with the source cloud's real error envelope.
4. Update path is BUG-2-blocked across Apply; tests for it are ◇-skipped with a pointer until 10.3 closes BUG-2.
5. Tag write is BUG-12-blocked; same skip posture.
6. Delete is honest with the backend's native semantics; no synthesized "drain first" behavior.

## Known open BUGs gating this contract

- [BUG-2](../../BUGS.md): queue `SetQueueAttributes` not wired. **Gates Apply Update.**
- [BUG-12](../../BUGS.md): queue domain tag storage. **Gates tag write.**
- [BUG-3](../../BUGS.md), [BUG-4](../../BUGS.md): NATS-specific, surface at Receive / Delete time, not at Apply — separate concern.

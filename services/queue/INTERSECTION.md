# Queue — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).

## AWS SQS frontend (awsJson1_0)

| Op | Category | Status |
|---|---|---|
| CreateQueue, DeleteQueue, ListQueues, GetQueueUrl, GetQueueAttributes | 1 | ✅ |
| SendMessage, ReceiveMessage, DeleteMessage, ChangeMessageVisibility | 1 | ✅ |
| SendMessageBatch, DeleteMessageBatch | 1 | ✅ |
| **SetQueueAttributes** | 1 — required by `hashicorp/aws aws_sqs_queue` for post-create reconciliation | ❌ **BUG-2** |
| AddPermission, RemovePermission, ListQueueTags, TagQueue, UntagQueue | 1 | ✅ |
| FIFO queue + content-based dedup | 2 — feature unset by default; in scope when caller opts in | ⚠ partial |

## GCP Pub/Sub-as-queue frontend

GCP's subscription-pull model is the closest cross-cloud counterpart to SQS. The frontend exposes the **subscription** surface; topic ops appear under pubsub (Phase 4).

| Op | Category | Status |
|---|---|---|
| Subscriptions.{create,get,delete,list,modifyAckDeadline,acknowledge} | 1 | ✅ |
| Subscriptions.pull, streamingPull | 1 (pull) / 3 (streaming bi-di out) | ✅ pull / ◇ streaming |
| Subscriptions.modifyPushConfig | 3 — out (push delivery vendor-specific) | ◇ |

## Azure Service Bus queues (REST + AMQP)

| Op | Category | Status |
|---|---|---|
| Create/Delete/Get/List Queue (ARM control plane) | 1 | ✅ REST |
| Send / Receive / Complete / Abandon / Defer (data plane) | 1 | ⚠ AMQP data plane not driven by `azservicebus` REST mode — the SDK's primary path is AMQP; REST works only for control plane |
| Dead-letter queue ops | 1 — DLQ is migration-critical | ⚠ partial |
| Sessions, transactions, scheduled messages | 3 — out | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS SQS | GCP Subscription | Azure SB Queue | NATS JS | Status |
|---|---|---|---|---|---|
| Create a queue | CreateQueue | Subscriptions.create | Create Queue | Stream + consumer | ✅ |
| Send a message | SendMessage | (Publisher → topic; subscription delivers) | Send | nats Publish | ✅ |
| Receive (long-poll) | ReceiveMessage | pull | Receive | Consumer Fetch | ✅ |
| Ack | DeleteMessage | acknowledge | Complete | Ack | ⚠ NATS lockToken slash issue → BUG-4 (carried) |
| Visibility timeout | ChangeMessageVisibility | modifyAckDeadline | Renew lock | (re-deliver) | ✅ |
| Reconcile attributes after create | SetQueueAttributes | (set on create) | Update | (recreate) | ❌ **BUG-2** |

## Known gaps

- BUG-2: SetQueueAttributes missing → `aws_sqs_queue` Terraform ◇-skipped.
- BUG-3: NATS context-deadline race → conformance-nats red until backend wraps ctx.
- BUG-4: NATS message-ID slash collides with Azure URL routing.

Phase 9 picks up SetQueueAttributes as a migration-critical op (Terraform users rely on it).

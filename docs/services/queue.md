# Queue

Point-to-point messaging: one producer, many consumers, each message delivered to exactly one consumer.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS SQS | awsJson1_0 (with awsQueryCompatible legacy error codes) | Full Create / Set / Get Attributes lifecycle including hashicorp/aws `WaitForStateEqual`. |
| GCP Pub/Sub (pull) | REST + JSON | A queue maps to a topic + subscription pair sharing the same name. |
| Azure Service Bus queue | REST | Lock-token receipt handles. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS SQS | Passthrough. |
| `gcp` | Real GCP Pub/Sub | Subscription-side retention semantics. |
| `azure` | Real Azure Service Bus | Receipt-token-based ack. |
| `nats` | NATS JetStream | The K8s peer. WorkQueue retention + durable consumer for ack visibility. |
| `inmem` | Process-local | Tests + local dev. |

## Receipt handles

Cross-cloud receipt-handle formats differ (AWS opaque string, GCP `AckId`, Azure `LockToken + MessageId`, NATS `reply_subject`). The shim treats them as opaque — passes through unchanged ([AGENTS.md § stateless](../../AGENTS.md#the-shim-is-stateless)). No shim-side mapping table.

## Intersection contracts

- **[`services/queue/OPERATIONS.md`](../../services/queue/OPERATIONS.md)** — 8 operations covering create/delete/head/list, send/receive/delete/change-visibility messages.
- **[`services/queue/INTERSECTION.md`](../../services/queue/INTERSECTION.md)** — per-frontend op classification.
- **[`services/queue/APPLY_INTERSECTION.md`](../../services/queue/APPLY_INTERSECTION.md)** — Apply contract. Documents:
  - In-contract Create attributes: `name`, `visibility_timeout_seconds`, `message_retention_seconds`, `max_message_size`.
  - `delay_seconds`: AWS / Azure only; GCP / NATS return `OperationNotSupportedException`.
  - FIFO: AWS-only; non-AWS returns `OperationNotSupported`.
  - Tag write: blocked by BUG-12 (queue domain tag storage).
  - Soft-delete: dropped from scope per Phase 10 codex review (no peer concept).

## Update intersection

`domain.Queues.SetQueueAttributes(name, QueueAttributes)` is in-contract across backends (closed BUG-2, which carried 5 phases):

- inmem patches in place.
- AWS calls `SetQueueAttributes`.
- GCP uses `subscriptions.patch` with `updateMask` (visibility + retention; other attrs ignored, documented).
- Azure does `GetQueue → UpdateQueue` read-modify-write.
- NATS updates the stream config + consumer config.

## Read-side attribute surface

The AWS frontend's `GetQueueAttributes` returns all the canonical AWS attribute keys the hashicorp/aws `WaitForStateEqual` polls — including out-of-intersection ones (`Policy`, `RedrivePolicy`, `KmsMasterKeyId`, `FifoQueue`, etc) with honest empty/zero defaults. The defaults aren't fakes; they represent "no extra features configured."

## awsQueryCompatible legacy error codes

SQS uses both `awsJson1_0` AND `awsQueryCompatible`. Clients (including hashicorp/aws's wait functions) sometimes match on the legacy Query error codes via the `x-amzn-query-error` response header. The shim emits both — e.g. `QueueDoesNotExist` in the JSON body + `AWS.SimpleQueueService.NonExistentQueue;Sender` in the header so post-destroy delete-confirmation waits see the expected match.

## Conformance

- `TestQueueMatrix_*` — (frontend × backend × driver) cells.
- `TestTerraform_AWSQueue_Apply_NoDrift` — AWS frontend Apply lifecycle through inmem.
- `TestCrossCloudImport_Roundtrip_QueueAWStoGCPPubsub` (Phase 9.13).
- `TestCrossCloudApply_Roundtrip_QueueAWStoGCPPubsub` (Phase 10.7) — documented-skip (WaitForStateEqual cross-cloud asymmetry).

## Known gaps

- BUG-12 (queue tag storage — `TagQueue` / `UntagQueue` not in domain).
- BUG-15 (GCP queue `message_retention_duration` plan/apply asymmetry — partial fix in tree).
- BUG-3 / BUG-4 (NATS-specific receive/delete paths — orthogonal to Apply).

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/queue/MIGRATION.md](../../services/queue/MIGRATION.md)

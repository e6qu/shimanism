# Queue importer-read contract

> Phase 9 sub-phase 9.2 — captured from a `terraform import aws_sqs_queue` run against the shim's AWS SQS frontend.

## aws_sqs_queue import — observed wire ops

awsJson1_0 protocol; all requests `POST /` with `X-Amz-Target`.

| `X-Amz-Target` | Category | Status | Notes |
|---|---|---|---|
| `AmazonSQS.GetQueueUrl` | 1 | ✅ | Resolves the URL-style import ID to a queue name. |
| `AmazonSQS.GetQueueAttributes` | 1 | ✅ | Returns the attribute map. |
| `AmazonSQS.ListQueueTags` | 2 | ✅ (honest empty) | Domain has no tag storage; returns `{Tags: {}}` per BUG-12. |
| `AmazonSQS.ListMessageMoveTasks` (if newer providers ask) | 3 | ◇ | Out of intersection. |

**Result:** `terraform import` succeeds end-to-end. `terraform plan -detailed-exitcode` returns 2 (diff) because the Terraform schema's default attributes (`message_retention_seconds`, `visibility_timeout_seconds`, etc.) need `SetQueueAttributes` to reconcile — that's BUG-2, expected.

## Pre-fix history

Initial 9.5 run failed on `UnknownOperationException: operation ListQueueTags is not supported by this shim` — the SQS frontend's `X-Amz-Target` dispatcher didn't have the op. Fix landed in the same Phase 9 commit: handler returns empty Tags. BUG-12 tracks the future write-path work.

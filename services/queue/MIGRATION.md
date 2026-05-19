# Queue — migration walkthroughs

> Phase 9 sub-phase 9.2-B. See [INTERSECTION.md](INTERSECTION.md).

## AWS SQS → GCP Pub/Sub-as-subscription

```bash
shim queue --addr=:9200 \
  --frontend=aws_sqs \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=queue --endpoint=http://localhost:9200)"

aws sqs create-queue --queue-name jobs                 # CreateQueue
aws sqs list-queues                                     # ListQueues
aws sqs send-message --queue-url http://localhost:9200/jobs --message-body "..."
aws sqs receive-message --queue-url http://localhost:9200/jobs --wait-time-seconds 5
aws sqs delete-message --queue-url http://localhost:9200/jobs --receipt-handle "..."
```

**Walkthrough holds for runtime operations.** Caveat: `aws_sqs_queue` Terraform requires `SetQueueAttributes` for post-create reconciliation → BUG-2 (carried). Migration-by-CLI works; migration-by-Terraform has one ◇-skipped cell pending BUG-2's fix.

## AWS SQS → NATS JetStream (cloud → K8s peer)

```bash
shim queue --addr=:9200 \
  --frontend=aws_sqs \
  --backend=nats --nats-url=$NATS_URL &
```

**Walkthrough holds with two carried BUGs:**
- BUG-3 — NATS context-deadline requirement causes `TestQueueMatrix` failures unless the backend wraps `ctx`. This is a real shim bug, not a fake; tracked.
- BUG-4 — NATS message-ID with `/` collides with the Azure-frontend URL routing; affects only that specific cross-cell.

## Azure Service Bus → AWS SQS

AMQP data-plane gap: the official `azservicebus` SDK uses AMQP for send/receive, not REST. The shim's Azure SB frontend implements the REST control plane (create/delete/list queue) but the data plane is unreachable through the official SDK without an AMQP listener. Documented in INTERSECTION.md.

**Walkthrough partially holds:** control-plane migration works; data-plane migration requires the AMQP frontend (a future phase).

## Coverage

Most cells green; BUG-2/3/4 carried, all documented in BUGS.md.

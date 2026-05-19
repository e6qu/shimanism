# Pub/Sub — migration walkthroughs

> Phase 9 sub-phase 9.2-B. See [INTERSECTION.md](INTERSECTION.md).

## AWS SNS+SQS → GCP Pub/Sub

```bash
shim pubsub --addr=:9300 --sns-addr=:9300 --sqs-receive-addr=:9301 \
  --frontend=aws_sns \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=pubsub --endpoint=http://localhost:9300)"

aws sns create-topic --name events                                # CreateTopic
aws sns subscribe --topic-arn arn:aws:sns:us-east-1::events \
  --protocol sqs --notification-endpoint arn:aws:sqs:us-east-1::receivers  # Subscribe
aws sns publish --topic-arn arn:aws:sns:us-east-1::events --message "..."  # Publish

# Receive side via SQS-shaped frontend.
aws sqs receive-message --queue-url http://localhost:9301/receivers
```

**Walkthrough holds for publish + receive.** Topic-attribute reconciliation via Terraform (`aws_sns_topic`) shares BUG-2's ripple. Migration-by-CLI works.

## GCP Pub/Sub → Azure Service Bus

```bash
shim pubsub --addr=:9300 \
  --frontend=gcp_pubsub \
  --backend=azure &
```

Control-plane migration works; data-plane subject to the AMQP gap from queue.

## Cloud → NATS (K8s peer)

Same shape; uses NATS subjects as the topic primitive.

## Coverage

Phase 4 closed clean for the intersection; carried bugs are surfacing in TF cells only.

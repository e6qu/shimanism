# Pub/Sub — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).

## AWS SNS frontend (awsQuery / XML) + SQS-receive frontend (paired)

AWS pubsub is split: SNS handles the publish side, SQS-shaped receive endpoints handle the subscription side. Two HTTP listeners.

| Op | Category | Status |
|---|---|---|
| CreateTopic, DeleteTopic, ListTopics, GetTopicAttributes | 1 | ✅ |
| Publish, PublishBatch | 1 | ✅ |
| Subscribe, Unsubscribe, ListSubscriptions, ListSubscriptionsByTopic | 1 | ✅ |
| SetTopicAttributes | 1 — needed by `aws_sns_topic` TF for reconciliation | ⚠ ripple of BUG-2 (same family of post-create attribute ops) |
| AddPermission, RemovePermission | 3 — out (resource policy vendor-specific) | ◇ |
| Phone-number / app / push-notification protocols | 3 — out | ◇ |

## GCP Pub/Sub frontend

| Op | Category | Status |
|---|---|---|
| Topics.{create,get,delete,list,publish} | 1 | ✅ |
| Subscriptions.{create,get,delete,list} (for the pubsub-side fan-out, paired with queue subscription receive) | 1 | ✅ |
| Schemas, snapshots, message-ordering, dead-letter policies | 3 — out | ◇ |

## Azure Service Bus topics

| Op | Category | Status |
|---|---|---|
| Create/Delete/Get/List Topic (ARM control plane) | 1 | ✅ |
| Create/Delete Subscription (under a topic) | 1 | ✅ |
| Send to topic (data plane) | 1 | ⚠ AMQP data plane — REST mode same caveat as queue |
| Filters / rules / partitioning | 3 — out | ◇ |

## Cross-cloud intersection (migration view)

| User-intent | AWS SNS+SQS | GCP Topic+Sub | Azure Topic+Sub | NATS | Status |
|---|---|---|---|---|---|
| Create a topic | CreateTopic | Topics.create | Create Topic | subject | ✅ |
| Subscribe a queue/receiver | Subscribe (SQS endpoint) | Subscriptions.create | Create Subscription | Consumer | ✅ |
| Publish | Publish | Topics.publish | Send | Publish | ✅ |
| Fan-out delivery | (SQS receive) | (Subscription pull) | (Subscription receive) | (Consumer fetch) | ✅ |
| Reconcile topic attributes | SetTopicAttributes | (set on create) | Update | (n/a) | ⚠ BUG-2 ripple |

## Known gaps

- `aws_sns_topic_subscription` Terraform cell ◇-skipped pending SetTopic/SubscriptionAttributes wiring.
- AMQP data-plane gap for Azure carried from queue.

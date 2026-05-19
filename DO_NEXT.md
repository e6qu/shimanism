# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #8 (Phase 3 — queue, full 3 × 5 × 3 matrix + NATS JetStream as K8s peer) at `07d11f5` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-4-pubsub` — fresh off main, 4.0 scope baseline drafted.
- **Project phase:** **Phase 4 — Pub/Sub.** Topic-fanout model (one topic, many subscriptions, each subscriber gets a copy of every message). Three frontends (AWS SNS+SQS receive, GCP Pub/Sub fanout, Azure Service Bus topics) × five backends (inmem + NATS core/JetStream as K8s peer + the three clouds) × three driver types (SDK + CLI + Terraform). 10-op intersection in [`services/pubsub/OPERATIONS.md`](services/pubsub/OPERATIONS.md).

## Phase 4 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **4.0** | ✅ | Scope + design baseline. `services/pubsub/OPERATIONS.md` captures the 10-op intersection across AWS SNS+SQS / GCP Pub/Sub (fanout) / Azure Service Bus topics / NATS core (with JetStream consumers for durable subs); separation of Topic ↔ Subscription resources; receipt-handle / visibility-timeout / message-attributes mapping reused from Phase 3; out-of-intersection list (FIFO topics, filters, push, sessions, ordering keys, exactly-once). |
| **4.1** | ✅ | Spec ingest. AWS SNS Smithy 2.0 JSON vendored at `services/pubsub/spec/aws-sns.smithy.json`, pinned to `aws/aws-sdk-go-v2@2517fe9f`. `services/pubsub/codegen.json` manifest names 9 publish-side ops (CreateTopic, DeleteTopic, ListTopics, Subscribe, Unsubscribe, ListSubscriptions, ListSubscriptionsByTopic, Publish, GetTopicAttributes). The receive side reuses Phase 3's vendored SQS spec — SNS subscriptions deliver to SQS endpoints. GCP + Azure specs reused via their official Go SDKs' wire-type packages. |
| **4.2** | ✅ | `internal/pubsub/domain/` neutral interface — `Pubsub` interface (12 methods: CreateTopic, DeleteTopic, ListTopics, HeadTopic, CreateSubscription, DeleteSubscription, ListSubscriptions, HeadSubscription, Publish, Receive, Ack, ChangeVisibility) + types (`Topic`, `Subscription`, `Message`, `*Options`, `*Result`); typed `Error` with `Kind` discriminator (NoSuchTopic, TopicAlreadyExists, NoSuchSubscription, SubscriptionAlreadyExists, InvalidReceiptHandle, MessageTooLarge, InvalidArgument). Opaque receipt handles (same rule as Phase 3). Caps mirror Phase 3 (600s visibility, 20s wait). `Subscription.Durable` toggles NATS core ↔ JetStream on the K8s peer; ignored on AWS/GCP/Azure (always durable). |
| **4.3** | ✅ | `services/pubsub/backends/inmem/` — topics + subscriptions; each subscription has its own pending + inflight delivery queue (lazy visibility reclamation). AWS SNS frontend at `internal/pubsub/frontends/aws_sns/` speaks awsQuery (form-encoded request, XML response with the SNS namespace + `<Op>Response/<Op>Result/ResponseMetadata` envelope). Slim SQS-shaped receive frontend at `internal/pubsub/frontends/aws_sqs_receive/` translates SQS QueueUrl → pubsub subscription name; only Receive / Ack / ChangeVisibility / DeleteQueue / GetQueueAttributes / GetQueueUrl exposed (no CreateQueue / SendMessage — fanout-only). Harness `StartPubsubServerAWS` returns SnsURL + SqsURL pointing at one shared backend. SDK conformance via `aws-sdk-go-v2/service/sns` + `aws-sdk-go-v2/service/sqs`: CreateTopic → Subscribe(Protocol=sqs, Endpoint=queueArn) → Publish → SQS.ReceiveMessage → DeleteMessage → Unsubscribe → DeleteTopic. |
| **4.4** | ✅ | **NATS JetStream backend** (K8s peer) `services/pubsub/backends/nats/`. Topic → JetStream stream (InterestPolicy retention so messages are kept until every consumer has read them, then dropped). Subscription → durable pull consumer attached to that stream. Publish → js.PublishMsg on the stream's subject; every consumer sees a copy. Ack/ChangeVisibility via publishing `+ACK`/`+WPI` on the reply subject — stateless, same machinery as Phase 3 NATS queue backend. Per-call context wrapping via `withDeadline`. Departure from the OPERATIONS.md draft: JetStream is used for both durable and non-durable subs since real AWS/GCP/Azure subscriptions are always durable; the `Durable` flag is recorded but doesn't change wire behaviour. |
| **4.5** | ✅ | **AWS SNS+SQS passthrough backend** `services/pubsub/backends/aws/`. `CreateSubscription` auto-creates the backing SQS queue (queue name = subscription name) then SNS Subscribe(TopicArn, Protocol=sqs, Endpoint=queueArn). `DeleteSubscription` reverses: Unsubscribe + DeleteQueue. Receive/Ack/ChangeVisibility hit the backing SQS queue resolved per-call via GetQueueUrl (no persistent cache). Subscription ARN lookup iterates `ListSubscriptions` since the SNS API only addresses subscriptions by ARN. |
| **4.6** | ◻ | **GCP Pub/Sub fanout backend** `services/pubsub/backends/gcp/` via `google.golang.org/api/pubsub/v1`. Multi-subscription topology (multiple subs per topic, each with its own ack-deadline). Diverges from Phase 3's "one topic + one sub collapsed to one queue" — Phase 4 keeps them separate. |
| **4.7** | ◻ | **Azure Service Bus topics backend** `services/pubsub/backends/azure/` — hybrid: `azservicebus` SDK for topic + subscription admin and Send + Receive; REST API (SAS-signed per request) for Complete + RenewLock since the high-level SDK requires `*ReceivedMessage`. |
| **4.8** | ◻ | **GCP Pub/Sub frontend** `internal/pubsub/frontends/gcp_pubsub/`. Same wire types as Phase 3 (reuse rule), but fanout-aware: topic ≠ subscription resources, multiple subs per topic. |
| **4.9** | ◻ | **Azure Service Bus topics REST frontend** `internal/pubsub/frontends/azure_servicebus_topics/`. Routes: `PUT /{topic}`, `PUT /{topic}/Subscriptions/{sub}`, `POST /{topic}/messages` (publish), `POST /{topic}/Subscriptions/{sub}/messages/head` (peek-lock on a subscription), Complete/Renew under the subscription's URL. AMQP fidelity tier deferred same as Phase 3. |
| **4.10** | ◻ | Conformance matrix: `TestPubsubMatrix_{AWSFrontend,GCPFrontend,AzureFrontend}` iterates every backend factory and drives Create → Subscribe → Publish → Receive → Ack → DeleteSubscription → DeleteTopic. |
| **4.11** | ◻ | CLI conformance: `aws sns publish`/`subscribe` (+ `aws sqs receive-message` for the backing queue), `gcloud pubsub topics+subscriptions`, `az servicebus topic+subscription`. |
| **4.12** | ◻ | Terraform conformance where the provider admits endpoint override: `hashicorp/aws` (`aws_sns_topic` + `aws_sns_topic_subscription` + `aws_sqs_queue`), `hashicorp/google` (`google_pubsub_topic` + `google_pubsub_subscription`). |
| **4.13** | ◻ | `cmd/shim pubsub` subcommand. Default `:9300`. Selectors: -frontend (aws_sns, gcp_pubsub, azure_servicebus_topics), -backend (inmem, nats, aws, gcp, azure). Version bump 0.5.0-phase-4. |
| **4.14** | ◻ | CI lane — reuse the existing `conformance-nats` container (it already runs `-js` which supports both core NATS subjects and JetStream consumers); add `TestPubsubMatrix` to the same lane's test command. |
| **4.15** | ◻ | Phase 4 closer: SDK matrix green across reachable cells; documented skips for Azure AMQP / ARM-only cells; STATUS / WHAT_WE_DID / BUGS refreshed; PR title + body refreshed; CI green. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 4 design notes

**Pub/Sub vs Queue — what's different from Phase 3.** Phase 3 was point-to-point: one queue, many consumers, each message goes to *one* consumer. Phase 4 is fanout: one topic, many subscriptions, each subscriber receives a copy of *every* message. Two consequences: Topic ≠ Subscription as separate domain resources, and Receive is per-subscription, not per-topic.

**AWS dual-protocol surface.** SNS for publish, SQS for receive. SNS subscriptions deliver to SQS queues by ARN; the shim auto-creates the backing queue at `CreateSubscription` time. The Phase 3 SQS handler is reused verbatim for the receive side. This is the only frontend whose driver tests use *two* SDK clients.

**NATS core (not JetStream) for fanout.** Subject-based publish/subscribe is true fanout. For *durable* subscriptions the backend toggles to JetStream consumers attached to a stream — same machinery the Phase 3 NATS backend uses. The Phase 3 `conformance-nats` lane already runs NATS with `-js`, so it covers both modes.

**Receipt handles, visibility, attributes.** Inherited verbatim from Phase 3 — same opaque-string contract, same 600s visibility cap, same 20s wait cap, same `map[string]string` attribute coercion rule. See [`services/pubsub/OPERATIONS.md`](services/pubsub/OPERATIONS.md) for details.

**Out-of-intersection features (return source-cloud "not supported" error):**
- AWS SNS FIFO topics, message filtering, KMS encryption, HTTP/Lambda/email subscription protocols, DLQ.
- GCP ordering keys, push subscriptions, exactly-once, schema registries, filters.
- Azure topic rules/filters, sessions, duplicate detection, auto-forwarding.
- NATS mirror streams, replay policies.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-pubsub test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention** ([AGENTS.md](AGENTS.md#reuse-over-reinvention)): wire types from each cloud's official Go SDK; spec inputs from upstream-canonical sources; auth verification via the cloud's official verifier libraries.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Live-cloud rows for AWS / GCP / Azure are skipped on every phase until this lands.
- **Track B — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.
- **BUG-2 (queue / SetQueueAttributes).** Wiring the 9th queue intersection op so `hashicorp/aws aws_sqs_queue` Terraform conformance lifts the ◇-skip. Pick up after Phase 4 closes, or fold into a later phase.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

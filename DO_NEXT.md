# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #7 (Phase 2 — secrets, full 3 × 5 × 3 matrix + shimakit framework) at `7df43ec` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-3-queue` — fresh branch off `main`, no commits yet, no PR yet.
- **Project phase:** **Phase 3 — Queue.** Three frontends (AWS SQS, GCP Pub/Sub pull, Azure Service Bus queue) × four backends (the three clouds + NATS JetStream as K8s peer) × three driver types (SDK + CLI + Terraform). Same N × N matrix discipline as Phases 1 + 2.

## Phase 3 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **3.0** | ✅ | Scope + design baseline. `services/queue/OPERATIONS.md` captures the 8-op intersection across AWS SQS / GCP Pub/Sub (pull) / Azure Service Bus queues / NATS JetStream (K8s peer); receipt-handle / visibility-timeout / message-attributes mapping; out-of-intersection list (FIFO, DLQ, KMS, push, IAM); stateless rule applied (no shim-side handle index). |
| **3.1** | ✅ | Spec ingest. AWS SQS Smithy 2.0 JSON vendored at `services/queue/spec/aws-sqs.smithy.json`, pinned to `aws/aws-sdk-go-v2@2517fe9f`. `services/queue/codegen.json` manifest names the 8 intersection ops + `GetQueueUrl` as a probe (clients use URL not name). GCP + Azure specs reused via their official Go SDKs' wire-type packages, per Phase 1.14/1.15/2.x precedent. |
| **3.2** | ◻ | `internal/queue/domain/` neutral interface — `Queue` interface + types (`Message`, `QueueAttributes`, `ReceiveOptions`); typed `Error` with `Kind` discriminator (NoSuchQueue, QueueAlreadyExists, InvalidReceiptHandle, MessageTooLarge, InvalidArgument). Receipt handles are opaque strings; visibility timeout in seconds. |
| **3.3** | ◻ | AWS SQS frontend `internal/queue/frontends/aws_sqs/`. Hand-written awsQuery (SQS uses POST with form-encoded body, not awsJson) dispatch + per-op shapes mirroring the Smithy spec. Note: SQS is one of the rare AWS services on awsQuery protocol; codegen extension deferred. |
| **3.4** | ◻ | `services/queue/backends/inmem/` covering all 8 ops with in-flight tracking + visibility-timeout expiry. `services/queue/conformance/aws_sdk_test.go` drives `aws-sdk-go-v2/service/sqs` against the in-mem backend. |
| **3.5** | ◻ | **NATS JetStream backend** (K8s peer): `services/queue/backends/nats/` via `nats.go` + `jetstream` package. Maps domain Queue to a JetStream stream + pull consumer. ChangeVisibility maps to `Msg.InProgress` (extends ack deadline). |
| **3.6** | ◻ | **AWS SQS passthrough backend** `services/queue/backends/aws/` via `aws-sdk-go-v2/service/sqs`. Receipt handle passes through as opaque string. |
| **3.7** | ◻ | **GCP Pub/Sub backend** `services/queue/backends/gcp/` via `cloud.google.com/go/pubsub`. Domain queue maps to a topic + subscription pair. `AckId` ↔ opaque receipt handle. |
| **3.8** | ◻ | **Azure Service Bus backend** `services/queue/backends/azure/` via `azure-sdk-for-go/sdk/messaging/azservicebus`. `LockToken` + `MessageId` encoded into the composite opaque receipt handle. |
| **3.9** | ◻ | **GCP Pub/Sub frontend** `internal/queue/frontends/gcp_pubsub/`. Wire types from `google.golang.org/api/pubsub/v1` (Discovery-generated). Routes cover topics + subscriptions + `:pull` and `:acknowledge`. |
| **3.10** | ◻ | **Azure Service Bus frontend** `internal/queue/frontends/azure_servicebus/`. AMQP-vs-REST decision (per PLAN.md open question): REST-only at this phase via the management API; AMQP wire-level shim deferred. |
| **3.11** | ◻ | Conformance matrix: `TestQueueMatrix_{AWSFrontend,GCPFrontend,AzureFrontend}` iterates every backend factory and drives the matching cloud's official Go SDK through Send → Receive → Delete → Send → Receive → Change-Visibility → Delete. |
| **3.12** | ◻ | CLI conformance: `aws sqs`, `gcloud pubsub`, `az servicebus`. |
| **3.13** | ◻ | Terraform conformance where the provider admits endpoint override: `hashicorp/aws` (`aws_sqs_queue`), `hashicorp/google` (`google_pubsub_topic` + `google_pubsub_subscription`). `hashicorp/azurerm` likely skipped same as Phase 1 + 2 — confirm at the time. |
| **3.14** | ◻ | `cmd/shim queue` subcommand. Selectors: -frontend (aws_sqs, gcp_pubsub, azure_servicebus), -backend (inmem, nats, aws, gcp, azure). Connection knobs accept flags + env vars. Version bumped to 0.4.0-phase-3. |
| **3.15** | ◻ | CI lane `conformance-nats`: NATS dev container, `TestQueueMatrix_*` against the live NATS backend. Real-cloud lanes (aws-sqs, gcp-pubsub, azure-servicebus) wait on Track A. |
| **3.16** | ◻ | Phase 3 closer: SDK matrix green across all (frontend × backend) cells; CLI + TF rows green where their tooling admits endpoint override; ◇ skipped cells documented per Phase 1+2 convention. CI green across all required checks. PR retitled + body refreshed. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 3 design notes

**Receipt handles — the hard part.** AWS / GCP / Azure / NATS each emit a different opaque token after a receive that the consumer must present back to ack / extend / delete. The domain uses opaque-string handles; each backend adapter maps native ↔ opaque. **No shim-side index** — per the no-state rule the handle round-trips through the backend, with composite encoding for Azure where the native pair (MessageId + LockToken) doesn't fit one string. See [`services/queue/OPERATIONS.md`](services/queue/OPERATIONS.md#receipt-handles) for the per-cloud mapping.

**Visibility / ack-deadline semantics.** AWS lets you override per-receive (up to 12h); GCP caps at 600s (10m) and doesn't honour a per-receive override (use `ModifyAckDeadline` after the receive instead); Azure extends via `RenewMessageLock`; NATS uses `Msg.InProgress`. Domain caps at 600 seconds across all backends so behaviour is uniform.

**Wait time.** AWS caps at 20s; Azure at 240s; NATS unbounded; GCP recommends streaming pull. Domain caps at 20s; backends that don't natively support per-receive wait busy-poll up to the budget.

**Out-of-intersection features (return source-cloud "not supported" error):**
- AWS FIFO queues, DLQ redrive, KMS encryption, message timers
- GCP ordering keys, push subscriptions, filters, exactly-once
- Azure sessions, duplicate detection, DLQ, scheduled messages, topics/subscriptions, partitioning
- NATS push consumers, mirror streams, replay policies

**K8s peer choice — NATS JetStream.** The KV / object engines inside JetStream are out of intersection (they belong to other shim services). Only the pull-consumer model is in scope.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-secrets test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention** ([AGENTS.md](AGENTS.md#reuse-over-reinvention)): wire types from each cloud's official Go SDK; spec inputs from upstream-canonical sources; auth verification via the cloud's official verifier libraries.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Live-cloud rows for AWS / GCS / Azure Blob are skipped on every phase until this lands.
- **Track B — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

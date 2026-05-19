# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-4-pubsub` — fresh off main. 4.0 scope baseline drafted; sub-phases 4.1–4.15 ahead. |
| In-flight | **Phase 4 — Pub/Sub.** Topic-fanout model. Three frontends (AWS SNS+SQS-receive, GCP Pub/Sub fanout, Azure Service Bus topics) × five backends (inmem + NATS core/JetStream as K8s peer + the three clouds) × three driver types. 10-op intersection in [`services/pubsub/OPERATIONS.md`](services/pubsub/OPERATIONS.md). Topic ≠ Subscription as separate resources; Receive is per-subscription. |
| Phase 3 closed | PR #8 merged `07d11f5` 2026-05-19. Three queue frontends × five backends × three driver types; 13 required CI checks. NATS JetStream as K8s peer; stateless receipt-handle round-trip; AMQP / ARM-only cells ◇-skipped with documented reasons. BUG-2 carried forward (SetQueueAttributes gap blocks `aws_sqs_queue` TF cell). |
| Phase 2 closed | PR #7 merged `7df43ec` 2026-05-19. Three secrets frontends × five secrets backends × three driver types; 12 required CI checks. Stateless invariant + shimakit framework + shima<service> naming convention landed alongside. |
| Phase 1 closed | PR #6 merged `1f64d9f` 2026-05-19. Three storage frontends × five storage backends × three driver types matrix; 11 required CI checks. |
| CI baseline | 13 required checks from Phase 3. Phase 4 reuses the `conformance-nats` lane (JetStream already enabled, covers both NATS-core fanout and durable JetStream subscriptions). Real-cloud lanes wait on Track A. |
| Scope rule (2026-05-18) | **Each phase ships the full N × N matrix.** Previous PLAN.md had Phases 9 and 10 as "GCP source row" and "Azure source row" of horizontal expansion across all 8 services; user reversed this. Each service phase now includes all 3 frontends + all 4 backends + SDK / CLI / Terraform for each, before moving to the next service. Phases 9 and 10 deleted; their work is absorbed into Phases 1-8. |
| Last merged | PR #5 — Phase 1.3 (codegen, originally all 107 ops) (`03b0ebb`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. |
| Renovate | Config committed (48h minimum release age, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
| Bugs | 2 filed · 1 fixed · 1 open (BUG-2: SetQueueAttributes gap blocks `aws_sqs_queue` TF cell). |
| Live infra | None. |

## Invariants (carry across compactions / fresh sessions)

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user. User merges.
- **Single-branch rule.** All work for one phase / sub-phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then start the fix commit.
- **Update continuity docs every significant chunk** (not just at phase end): STATUS.md + WHAT_WE_DID.md + DO_NEXT.md. This is what lets context survive compaction.
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing; sync local `main` after merge.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.
- **One service per phase.** Each phase ships one shimmed service end-to-end against all backends in scope.

### Architecture (load-bearing across all services)
- **The shim speaks the cloud's published API exactly.** Error shapes, response headers, status codes, async semantics — match. Server stubs are generated from the upstream spec; hand-written code is translation logic only.
- **Real backends, never emulators.** A shimmed call drives a real comparable service. The shim holds no state of record.
- **Stateless shim.** No sidecar storage, no shim-managed key/value namespace, no in-process cache treated as authoritative. State lives in the backend; cross-cloud mappings are derived at request time. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless).
- **Intersection-only scope.** Out-of-intersection feature calls fail loud with the source cloud's own error vocabulary. **Never fabricate success.**
- **Kubernetes is a first-class fourth backend** for every shimmed service.
- **No fakes, no fallbacks, no degraded modes.** Translation can't be honest → call fails loud.
- **Test from the official client surfaces.** SDK + CLI + Terraform provider in the same commit, against every backend in scope.

### Locked-in decisions (full table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions))
- Go is the implementation language.
- Specs pulled upstream, never forked: AWS Smithy JSON, GCP protobuf, Azure OpenAPI.
- Codegen: spec → typed Go server stubs; per-operation `translate.go` is the only hand-written code.
- Monorepo: `services/<service>/`, shared `internal/codegen/`, `internal/harness/`.
- Test rings: per-PR recorded interactions, nightly live cloud, pre-release vendor integration suites.

## Current phase — Phase 4: Pub/Sub

Phase 4 ships the pub/sub service: one topic with many subscriptions, each subscriber receiving a copy of every message. AWS SNS+SQS-receive / GCP Pub/Sub fanout / Azure Service Bus topics frontends, each translatable to inmem / NATS core (or JetStream for durable subs) / AWS / GCP / Azure backends. 10-op intersection.

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md). Scope baseline at [`services/pubsub/OPERATIONS.md`](services/pubsub/OPERATIONS.md).

### Phase 4 standing notes
- **Topic ≠ Subscription.** Phase 3 collapsed a GCP topic+subscription pair onto one queue; Phase 4 keeps them as separate resources because fanout requires it. Multiple subscriptions can attach to one topic; each subscription has its own ack-deadline.
- **AWS dual-protocol.** SNS for publish, SQS for receive. The shim auto-creates the backing SQS queue at `CreateSubscription` time and registers the SNS subscription with `Protocol=sqs`. The Phase 3 SQS handler is reused verbatim for the receive side.
- **NATS core for fanout, JetStream for durable subs.** Subject-based publish/subscribe is true fanout. When a subscription is created with `Durable=true`, the backend toggles to JetStream consumers attached to a stream — same machinery as Phase 3. The existing `conformance-nats` CI lane covers both modes.
- **Receipt handles, visibility, attributes.** Inherited verbatim from Phase 3 — opaque-string contract, 600s visibility cap, 20s wait cap, `map[string]string` attribute coercion.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #7 | 2 | Secrets service end-to-end. 3 frontends × 5 backends (inmem, Vault as K8s peer via shimakit, AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × 3 driver types. shimakit framework + shima\<service\> naming. Stateless invariant established. Merged 2026-05-19 at `7df43ec`. |
| #6 | 1 | Storage service end-to-end. 3 frontends × 5 backends (inmem, MinIO as K8s peer, AWS S3, GCS, Azure Blob) × 3 driver types. Spec-driven codegen pipeline. Merged 2026-05-19 at `1f64d9f`. |
| #5 | 1.3 | Codegen pipeline (originally all 107 ops; later narrowed to intersection-only). Merged 2026-05-18 at `03b0ebb`. |
| #4 | 1.2 | S3 Smithy spec vendored + license policy + Renovate + dependency policy (48h release age, pure-Go preference, pnpm + no lifecycle scripts) + version bumps to Go 1.26 / actions v6. Merged 2026-05-18 at `98e6ce9`. |
| #3 | 1.1 | Repo skeleton: Go module (1.25), Makefile, Go CI lane. PLAN.md restructured to one-service-per-phase. Merged 2026-05-18 at `48c0edf`. |

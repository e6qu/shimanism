# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-7-functions` — fresh off main. 7.0 scope baseline drafted. |
| In-flight | **Phase 7 — Functions.** Same control-plane shape as Phases 5+6; HTTP as the data plane. Container-image deployments only. Three frontends (AWS Lambda, GCP Cloud Run, Azure Container Apps) × five backends (inmem + Knative Serving as K8s peer + the three clouds) × three driver types. 5-op intersection. Events + auth-on-invoke deferred. Exit criterion: `curl <returned-url>` → function HTTP response. |
| Phase 6 closed | PR #11 merged `cca8bc0` 2026-05-19. Three cache frontends × five backends × three driver types; 15 required CI checks (added `conformance-redisop` lane). Redis Operator as K8s peer via dynamic client; PING exit criterion validated end-to-end. |
| Phase 4 closed | PR #9 merged `6305354` 2026-05-19. Three pubsub frontends × five backends × three driver types; same 13 required CI checks. NATS JetStream throughout as K8s peer; AWS dual-protocol surface (SNS publish + slim SQS-receive); 4-part Azure receipt encoding; AMQP / ARM-only cells ◇-skipped. `aws_sns_topic_subscription` cell carried as ripple of BUG-2. |
| Phase 3 closed | PR #8 merged `07d11f5` 2026-05-19. Three queue frontends × five backends × three driver types; 13 required CI checks. NATS JetStream as K8s peer; stateless receipt-handle round-trip; AMQP / ARM-only cells ◇-skipped with documented reasons. BUG-2 carried forward (SetQueueAttributes gap blocks `aws_sqs_queue` TF cell). |
| Phase 2 closed | PR #7 merged `7df43ec` 2026-05-19. Three secrets frontends × five secrets backends × three driver types; 12 required CI checks. Stateless invariant + shimakit framework + shima<service> naming convention landed alongside. |
| Phase 1 closed | PR #6 merged `1f64d9f` 2026-05-19. Three storage frontends × five storage backends × three driver types matrix; 11 required CI checks. |
| CI baseline | 15 required checks from Phase 6. Phase 7 will add `conformance-knative` (kind + Knative Serving). Real-cloud lanes wait on Track A. |
| Scope rule (2026-05-18) | **Each phase ships the full N × N matrix.** Previous PLAN.md had Phases 9 and 10 as "GCP source row" and "Azure source row" of horizontal expansion across all 8 services; user reversed this. Each service phase now includes all 3 frontends + all 4 backends + SDK / CLI / Terraform for each, before moving to the next service. Phases 9 and 10 deleted; their work is absorbed into Phases 1-8. |
| Last merged | PR #5 — Phase 1.3 (codegen, originally all 107 ops) (`03b0ebb`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. |
| Renovate | Config committed (48h minimum release age, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
| Bugs | 5 filed · 3 fixed · 2 open. BUG-2 (queue / SetQueueAttributes) ripples into Phase 4 + 5 TF cells. BUG-5 (rdbms / GCP Operations polling endpoint) blocks the GCP CLI + TF cells. |
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

## Current phase — Phase 7: Functions

Phase 7 ships the functions service end-to-end. AWS Lambda / GCP Cloud Run / Azure Container Apps frontends, each translatable to inmem / Knative Serving (K8s peer) / AWS / GCP / Azure backends. 5-op intersection. Container-image deployments only; events + auth-on-invoke deferred.

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md). Scope baseline at [`services/functions/OPERATIONS.md`](services/functions/OPERATIONS.md).

### Phase 7 standing notes
- **Same control-plane shape as Phases 5+6; HTTP as the data plane.** The shim provisions and returns an endpoint URL; clients invoke via plain HTTP — shim plays no role on the invocation path.
- **Container image only.** All four backends natively support container images; ZIP-package Lambda is out of intersection.
- **Events deferred.** Cross-cloud event payload normalization is the hard part per PLAN.md; HTTP-trigger functions only at this phase.
- **Auth-on-invoke deferred.** Public-HTTP functions only.
- **Exit criterion: `curl <endpoint>`.** Sub-phase 7.15 owns the HTTP-connectivity test against the Knative-provisioned function through the shim-returned endpoint URL.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #11 | 6 | Cache service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, Redis Operator as K8s peer via dynamic-client, AWS ElastiCache, GCP Memorystore, Azure Cache for Redis) × 3 driver types. Same control-plane shape as Phase 5; RESP PING exit criterion validated through kind + Redis Operator. Merged 2026-05-19 at `cca8bc0`. |
| #10 | 5 | RDBMS service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, CloudNativePG as K8s peer via dynamic-client + unstructured Cluster CRs, AWS RDS, GCP Cloud SQL Admin, Azure flexible-servers) × 3 driver types. Explicit async Status enum; psql connectivity exit criterion validated through kind + cnpg + real PG. Merged 2026-05-19 at `aeadbc8`. |
| #9 | 4 | Pubsub service end-to-end. 3 frontends × 5 backends (inmem, NATS JetStream as K8s peer with InterestPolicy retention + per-sub consumers, AWS SNS+SQS-receive, GCP Pub/Sub fanout, Azure Service Bus topics REST) × 3 driver types. Topic ≠ Subscription split as load-bearing change. Merged 2026-05-19 at `6305354`. |
| #8 | 3 | Queue service end-to-end. 3 frontends × 5 backends (inmem, NATS JetStream as K8s peer, AWS SQS, GCP Pub/Sub pull, Azure Service Bus queue) × 3 driver types. Stateless receipt-handle round-trip; new `conformance-nats` CI lane. Merged 2026-05-19 at `07d11f5`. |
| #7 | 2 | Secrets service end-to-end. 3 frontends × 5 backends (inmem, Vault as K8s peer via shimakit, AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × 3 driver types. shimakit framework + shima\<service\> naming. Stateless invariant established. Merged 2026-05-19 at `7df43ec`. |

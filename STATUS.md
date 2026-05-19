# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-8-apigateway` — Phase 8 + Phase 9 plan/docs/code on one PR per user instruction. |
| In-flight | **Phase 8 + Phase 9.** Phase 8 (API Gateway) complete: 3 frontends × 5 backends × 3 drivers; declarative-replace `DeployGateway`; exit criterion `TestRouteServes_Envoy` proves end-to-end HTTP routing through Envoy. Phase 9 (cross-cloud terraform-import) substantially advanced on same PR: importer-read contracts + INTERSECTION.md + MIGRATION.md per service; `shimctl env` CLI; `terraform import` test per service; 6 real fidelity fixes (XML double-nesting, Policy/EffectiveDeliveryPolicy JSON, ListQueueTags/ListTagsForResource handlers, APIGW selection-expression defaults, Lambda Read-path subresources, RDS DBInstanceArn). Exit criterion `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` proves cross-cloud import — AWS-shape TF imports a bucket whose data lives in mock GCS through the shim. |
| Phase 7 closed | PR #12 merged `9d02af0` 2026-05-19. Three functions frontends × five backends × three driver types; 16 required CI checks (added `conformance-knative` lane). Knative Serving as K8s peer via dynamic client + kourier-internal port-forward for HTTP-invoke exit criterion. Container-image deploys only; events + auth-on-invoke deferred. |
| Phase 6 closed | PR #11 merged `cca8bc0` 2026-05-19. Three cache frontends × five backends × three driver types; 15 required CI checks (added `conformance-redisop` lane). Redis Operator as K8s peer via dynamic client; PING exit criterion validated end-to-end. |
| Phase 4 closed | PR #9 merged `6305354` 2026-05-19. Three pubsub frontends × five backends × three driver types; same 13 required CI checks. NATS JetStream throughout as K8s peer; AWS dual-protocol surface (SNS publish + slim SQS-receive); 4-part Azure receipt encoding; AMQP / ARM-only cells ◇-skipped. `aws_sns_topic_subscription` cell carried as ripple of BUG-2. |
| Phase 3 closed | PR #8 merged `07d11f5` 2026-05-19. Three queue frontends × five backends × three driver types; 13 required CI checks. NATS JetStream as K8s peer; stateless receipt-handle round-trip; AMQP / ARM-only cells ◇-skipped with documented reasons. BUG-2 carried forward (SetQueueAttributes gap blocks `aws_sqs_queue` TF cell). |
| Phase 2 closed | PR #7 merged `7df43ec` 2026-05-19. Three secrets frontends × five secrets backends × three driver types; 12 required CI checks. Stateless invariant + shimakit framework + shima<service> naming convention landed alongside. |
| Phase 1 closed | PR #6 merged `1f64d9f` 2026-05-19. Three storage frontends × five storage backends × three driver types matrix; 11 required CI checks. |
| CI baseline | 16 required checks from Phase 7. Phase 8 will add a `conformance-envoy` lane (kind + Envoy Gateway). Real-cloud lanes wait on Track A. |
| Scope rule (2026-05-18) | **Each phase ships the full N × N matrix.** Previous PLAN.md had Phases 9 and 10 as "GCP source row" and "Azure source row" of horizontal expansion across all 8 services; user reversed this. Each service phase now includes all 3 frontends + all 4 backends + SDK / CLI / Terraform for each, before moving to the next service. Phases 9 and 10 deleted; their work is absorbed into Phases 1-8. |
| Last merged | PR #5 — Phase 1.3 (codegen, originally all 107 ops) (`03b0ebb`, 2026-05-18). |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. |
| Renovate | Config committed (48h minimum release age, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
| Bugs | 13 filed · 6 fixed · 7 open. Phase 9 fixed BUG-9/10/11; filed BUG-12 (queue domain tag storage) + BUG-13 (Lambda memory_size/role/publish soft plan diffs). |
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

## Current phase — Phase 8: API Gateway

Phase 8 ships the API Gateway service end-to-end. AWS API Gateway HTTP API v2 / GCP API Gateway / Azure API Management frontends, each translatable to inmem / Envoy Gateway (K8s peer) / the three clouds. 5-op intersection — Create/Delete/Describe/List/Deploy Gateway.

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md). Scope baseline at [`services/apigateway/OPERATIONS.md`](services/apigateway/OPERATIONS.md).

### Phase 8 standing notes
- **Declarative-replace.** `DeployGateway(spec)` atomically swaps the entire routing table. Partial route mutations on a live gateway are out of intersection (cross-cloud semantics diverge).
- **Route shape minimal.** Method + path + backend URL only. Per-route auth, throttling, transforms, CORS, custom domains all deferred — the exit criterion is "routes dispatch HTTP to backends correctly."
- **HTTP data plane.** Same posture as Phases 5-7. Shim provisions; HTTP traffic goes directly to the gateway URL; shim plays no role on the request path.
- **Exit criterion: gateway routes HTTP to a backend.** Sub-phase 8.15 owns the test: deploy a gateway with one route pointing at a `pong`-echo backend; HTTP-invoke the gateway's URL through that path; assert `pong`.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #12 | 7 | Functions service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, Knative Serving as K8s peer via dynamic-client + Service CRs, AWS Lambda, GCP Cloud Run, Azure Container Apps) × 3 driver types. Container-image only; HTTP-invoke exit criterion validated through kind + Knative + kourier-internal port-forward. Merged 2026-05-19 at `9d02af0`. |
| #11 | 6 | Cache service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, Redis Operator as K8s peer via dynamic-client, AWS ElastiCache, GCP Memorystore, Azure Cache for Redis) × 3 driver types. Same control-plane shape as Phase 5; RESP PING exit criterion validated through kind + Redis Operator. Merged 2026-05-19 at `cca8bc0`. |
| #10 | 5 | RDBMS service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, CloudNativePG as K8s peer via dynamic-client + unstructured Cluster CRs, AWS RDS, GCP Cloud SQL Admin, Azure flexible-servers) × 3 driver types. Explicit async Status enum; psql connectivity exit criterion validated through kind + cnpg + real PG. Merged 2026-05-19 at `aeadbc8`. |
| #9 | 4 | Pubsub service end-to-end. 3 frontends × 5 backends (inmem, NATS JetStream as K8s peer with InterestPolicy retention + per-sub consumers, AWS SNS+SQS-receive, GCP Pub/Sub fanout, Azure Service Bus topics REST) × 3 driver types. Topic ≠ Subscription split as load-bearing change. Merged 2026-05-19 at `6305354`. |
| #8 | 3 | Queue service end-to-end. 3 frontends × 5 backends (inmem, NATS JetStream as K8s peer, AWS SQS, GCP Pub/Sub pull, Azure Service Bus queue) × 3 driver types. Stateless receipt-handle round-trip; new `conformance-nats` CI lane. Merged 2026-05-19 at `07d11f5`. |
| #7 | 2 | Secrets service end-to-end. 3 frontends × 5 backends (inmem, Vault as K8s peer via shimakit, AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × 3 driver types. shimakit framework + shima\<service\> naming. Stateless invariant established. Merged 2026-05-19 at `7df43ec`. |

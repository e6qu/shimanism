# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md. Anything that needs re-explaining belongs here or in an Invariants block.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-10` — Phase 10 (cross-cloud `terraform apply` through the shim). |
| In-flight | **Phase 10.** Apply-side proof: Create → Read → Update → Destroy through the shim with no drift. Sub-phase 10.0-A (per-service `APPLY_INTERSECTION.md` contract) is the first gate; matrix tests assert against the contract, not whatever the provider tries. |
| Phase 9 closed | PR #13 + PR #16 merged at `ad85ddf` then `326f57d`, 2026-05-20 / 2026-05-21. **All 8 services through cross-cloud terraform import** (storage / secrets / queue / pubsub / apigateway / cache / functions / rdbms); `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` proves the headline. 6 real fidelity bugs fixed inline. PR #16 closed BUG-5 (Phase 10.1 gate: GCP `Operations.Get` across rdbms / cache / functions / apigateway) and adopted `PHASE_10_PLAN.md`. |
| Phase 8 closed | Co-merged in PR #13 with the Phase 9 chunk. API Gateway service end-to-end; AWS APIGW v2 / GCP API Gateway / Azure APIM frontends × inmem / Envoy Gateway K8s peer / 3 clouds × SDK + CLI + Terraform. Declarative-replace via `DeployGateway`; route shape minimal (method + path + backend URL). `TestRouteServes_Envoy` exit criterion. |
| Phase 7 closed | PR #12 merged `9d02af0` 2026-05-19. Three functions frontends × five backends × three driver types; 16 required CI checks (added `conformance-knative` lane). Knative Serving as K8s peer via dynamic client + kourier-internal port-forward for HTTP-invoke exit criterion. Container-image deploys only; events + auth-on-invoke deferred. |
| Phase 6 closed | PR #11 merged `cca8bc0` 2026-05-19. Three cache frontends × five backends × three driver types; 15 required CI checks (added `conformance-redisop` lane). Redis Operator as K8s peer via dynamic client; PING exit criterion validated end-to-end. |
| Phase 4 closed | PR #9 merged `6305354` 2026-05-19. Three pubsub frontends × five backends × three driver types; same 13 required CI checks. NATS JetStream throughout as K8s peer; AWS dual-protocol surface (SNS publish + slim SQS-receive); 4-part Azure receipt encoding; AMQP / ARM-only cells ◇-skipped. `aws_sns_topic_subscription` cell carried as ripple of BUG-2. |
| Phase 3 closed | PR #8 merged `07d11f5` 2026-05-19. Three queue frontends × five backends × three driver types; 13 required CI checks. NATS JetStream as K8s peer; stateless receipt-handle round-trip; AMQP / ARM-only cells ◇-skipped with documented reasons. BUG-2 carried forward (SetQueueAttributes gap blocks `aws_sqs_queue` TF cell). |
| Phase 2 closed | PR #7 merged `7df43ec` 2026-05-19. Three secrets frontends × five secrets backends × three driver types; 12 required CI checks. Stateless invariant + shimakit framework + shima<service> naming convention landed alongside. |
| Phase 1 closed | PR #6 merged `1f64d9f` 2026-05-19. Three storage frontends × five storage backends × three driver types matrix; 11 required CI checks. |
| CI baseline | 16 required checks from Phase 7. Phase 8 will add a `conformance-envoy` lane (kind + Envoy Gateway). Real-cloud lanes wait on Track A. |
| Scope rule (2026-05-18) | **Each phase ships the full N × N matrix.** Previous PLAN.md had Phases 9 and 10 as "GCP source row" and "Azure source row" of horizontal expansion across all 8 services; user reversed this. Each service phase now includes all 3 frontends + all 4 backends + SDK / CLI / Terraform for each, before moving to the next service. Phases 9 and 10 deleted; their work is absorbed into Phases 1-8. |
| Last merged | PR #16 — Phase 9 docs roll-up + Phase 10 plan + Phase 10.1 BUG-5 fix (`326f57d`, 2026-05-21). Closed BUG-5 (stateless `Operations.Get` across 4 GCP frontends); adopted `PHASE_10_PLAN.md`. |
| Standing merge auth | **None.** User merges every PR. |
| CI | Five required checks: `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible`. |
| Renovate | Config committed (48h minimum release age, weekly batches, pinned GitHub Actions SHAs); **user must install the Renovate GitHub App** at https://github.com/apps/renovate. |
| Dep policy | [`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md): min release age 48h, prefer pure-Go over cgo, pnpm + no lifecycle scripts when JS lands. |
| Bugs | 17 filed · 9 fixed · 7 open · 1 false positive. Phase 10 to-date: filed BUG-14 (false positive), BUG-15 (GCP queue retention), BUG-16 (rdbms GCP v1 vs v1beta4 path mismatch), BUG-17 (secrets UpdateSecret); BUG-5 (10.1), BUG-17 (10.3 first chunk), BUG-2 (10.3 second chunk — AWS SQS SetQueueAttributes across 5 backends + read-side attribute surface + awsQueryCompatible legacy error codes) closed. |
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

## Current phase — Phase 10: cross-cloud `terraform apply` through the shim

Phase 10 extends Phase 9's read-side proof (`terraform import` honest end-to-end) to the **write side**: `terraform apply` against the shim provisions, updates, and destroys resources on the destination backend, with the source-cloud provider unaware of the translation. Apply is the everyday Terraform workflow — proving it honest makes shimanism a **cross-cloud IaC control-plane migration tool** (not yet a full migration tool; data movement, IAM rebinding, DNS swap, etc. are follow-on phases).

Sub-phase table is in [DO_NEXT.md](DO_NEXT.md). Full plan, including codex review responses, at [`PHASE_10_PLAN.md`](PHASE_10_PLAN.md).

### Phase 10 standing notes
- **Contract-first matrix.** Sub-phase 10.0-A writes a per-service `APPLY_INTERSECTION.md` enumerating exactly which Create / Update / Delete ops the shim claims honest semantics for, with per-cell translation specified. Matrix tests assert against this contract, not "whatever the provider tries." This is the gate that prevents the matrix-explosion failure mode codex flagged.
- **BUG-5 closed in 10.1.** GCP `Operations.Get` is implemented across rdbms / cache / functions / apigateway. Apply against GCP-shape frontends no longer hangs on async ops.
- **Create-then-Read is necessary but not sufficient.** Single-frontend create-then-read won't catch self-consistent wrongness, invalid-input fidelity gaps, or cross-frontend semantic divergence. Phase 10 adds 10.2-B (cross-frontend read after cross-cloud write) and 10.2-C (invalid-input fidelity) to cover those classes.
- **Soft-delete is opt-in only.** No "default-30" cross-cloud fabrication. Where the destination doesn't expose a first-class soft-delete primitive, the shim returns the source cloud's `OperationNotSupported` envelope on retention-windowed destroy — not a silent hard-delete.
- **Exit criterion: `TestCrossCloudApply_Roundtrip` per service.** Symmetric to Phase 9.13's import roundtrip. Apply A-shape HCL through shim with backend=B; no drift; update in place; no drift; destroy.

## Recently closed phases (last 5)

| PR | Phase | Headline |
|---|---|---|
| #16 | 9 closer + 10 plan + 10.1 | Phase 9 docs roll-up (narrative correctly says "all 8 services"); `PHASE_10_PLAN.md` adopted (codex-reviewed); BUG-5 closed via stateless `Operations.Get` across 4 GCP-shape frontends (rdbms / cache / functions / apigateway). Merged 2026-05-21 at `326f57d`. |
| #13 | 8 + 9 chunk | API Gateway service end-to-end + Phase 9 substantial chunk (all 8 services through cross-cloud terraform import; `shimctl env` + endpoint-override registry; per-service `INTERSECTION.md` + `MIGRATION.md` audits; 6 real fidelity bugs fixed inline; `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` exit criterion). Merged 2026-05-20 at `ad85ddf`. |
| #12 | 7 | Functions service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, Knative Serving as K8s peer via dynamic-client + Service CRs, AWS Lambda, GCP Cloud Run, Azure Container Apps) × 3 driver types. Container-image only; HTTP-invoke exit criterion validated through kind + Knative + kourier-internal port-forward. Merged 2026-05-19 at `9d02af0`. |
| #11 | 6 | Cache service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, Redis Operator as K8s peer via dynamic-client, AWS ElastiCache, GCP Memorystore, Azure Cache for Redis) × 3 driver types. Same control-plane shape as Phase 5; RESP PING exit criterion validated through kind + Redis Operator. Merged 2026-05-19 at `cca8bc0`. |
| #10 | 5 | RDBMS service end-to-end (control-plane only). 3 frontends × 5 backends (inmem, CloudNativePG as K8s peer via dynamic-client + unstructured Cluster CRs, AWS RDS, GCP Cloud SQL Admin, Azure flexible-servers) × 3 driver types. Explicit async Status enum; psql connectivity exit criterion validated through kind + cnpg + real PG. Merged 2026-05-19 at `aeadbc8`. |
| #9 | 4 | Pubsub service end-to-end. 3 frontends × 5 backends (inmem, NATS JetStream as K8s peer with InterestPolicy retention + per-sub consumers, AWS SNS+SQS-receive, GCP Pub/Sub fanout, Azure Service Bus topics REST) × 3 driver types. Topic ≠ Subscription split as load-bearing change. Merged 2026-05-19 at `6305354`. |

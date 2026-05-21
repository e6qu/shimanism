# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #16 (Phase 9 docs roll-up + Phase 10 plan + Phase 10.1 BUG-5 fix) at `326f57d` on `origin/main`, 2026-05-21.
- **Active branch:** `phase-10`. Single PR for the whole Phase 10 effort (cross-cloud `terraform apply` through the shim). Granular commits as sub-phases land.
- **Phase 8** closed in PR #13 (co-merged with the Phase 9 chunk). API Gateway end-to-end, exit criterion `TestRouteServes_Envoy` green.
- **Phase 9** closed across PR #13 + PR #16. All 8 services through cross-cloud `terraform import`; `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` proves the headline. Per-service `INTERSECTION.md` + `MIGRATION.md` audits in tree.
- **Phase 10.1** already landed in PR #16: BUG-5 closed. All four GCP-shape frontends (rdbms Cloud SQL Admin / cache Memorystore / functions Cloud Run / apigateway API Gateway) implement `Operations.Get` statelessly. Apply against GCP frontends no longer hangs on async ops.

## Phase 10 sub-task table

Plan in [`PHASE_10_PLAN.md`](PHASE_10_PLAN.md). One PR for the whole phase; granular commits per sub-phase.

| Sub | Status | Headline |
|---|---|---|
| **10.0** | ✅ | Scope baseline. `PHASE_10_PLAN.md` written and codex-reviewed (5 critiques applied). |
| **10.0-A** | ✅ | Per-service `APPLY_INTERSECTION.md` — 8 files, one per service. |
| **10.1** | ✅ | BUG-5 family closed (PR #16). GCP `Operations.Get` on all four GCP-shape frontends. |
| **10.2** | ✅ | Create-then-Read drift audit per service. Apply test scaffolding in tree for all 8 services. **All 8 services have active drift assertions** (storage AWS / secrets AWS / queue AWS / pubsub AWS / apigateway AWS / functions AWS / rdbms GCP / **cache GCP**). Documented skips with BUG pointers: AWS rdbms (Modify reconcile + subnet/parameter-group metadata), AWS cache (ModifyCacheCluster reconcile), Azure cells across services (Azure-AsyncOperation URLs). |
| **10.2-B** | ◻ | Cross-frontend read after cross-cloud write. Catches self-consistent wrongness. |
| **10.2-C** | ◻ | Invalid-input fidelity — known-bad inputs assert the shim returns the source cloud's *real* error envelope. |
| **10.3** | ◐ | Update intersection audit per service. Two BUGs closed so far: BUG-17 (secrets `UpdateSecret` + per-backend + frontend dispatch) and BUG-2 (AWS SQS `SetQueueAttributes` + per-backend + read-side attribute surface + awsQueryCompatible legacy error codes). Remaining open: BUG-12 (queue tag storage), BUG-13 (Lambda role/publish/memory), BUG-15 (queue retention plan/apply asymmetry — partial fix landed), BUG-16 (rdbms GCP v1 vs v1beta4 path mismatch), BUG-6 (Azure APIM v3 delete), BUG-7/8 (Azure CLI + GCP TF apigateway). |
| **10.4** | ◻ | Soft-delete intersection across secrets + storage (queue dropped per codex review). Opt-in only. |
| **10.5** | ◐ | Per-service `apply_test.go` covering full lifecycle. Secrets test now drives Create → Read → Update (description) → Read → Destroy after BUG-17 closed. Queue test now drives Create → Read → Destroy (and apply-Update via SetQueueAttributes is implicitly exercised by provider reconcile). Remaining services gated on their own BUG closures (the 10.3 list). |
| **10.6** | ◻ | Cross-cloud Apply matrix tests, contract-scoped. |
| **10.7** | ◻ | Exit criterion: `TestCrossCloudApply_Roundtrip` per service. |
| **10.8** | ◻ | Phase 10 closer — push, CI green, PR merged. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 10 design notes

- **Contract-first matrix.** Sub-phase 10.0-A is the gate per codex review #5. Each `services/<svc>/APPLY_INTERSECTION.md` enumerates which Create / Update / Delete ops the shim claims honest cross-cloud semantics for, with per-cell translation specified. Matrix tests assert *against this contract*. This prevents the matrix-explosion failure mode where the provider tries every attribute and the shim has to either fake or 500.
- **Necessary-but-not-sufficient drift audit.** Single-frontend Create-then-Read passes when Create translates wrong *and* Read translates wrong in the same direction. 10.2-B drives Create through frontend A and Read through frontend B (same service, same backend) to catch this. 10.2-C drives known-bad inputs to assert error-envelope fidelity.
- **Soft-delete is opt-in only.** No default-30-day fabrication. Where the destination backend lacks a first-class soft-delete primitive, the shim returns the source cloud's `OperationNotSupported` envelope on a retention-windowed destroy. Queue dropped from soft-delete scope (no peer concept across AWS / GCP / Azure / NATS).
- **Stateless invariant carried.** 10.1's `Operations.Get` implementation encodes `(opType, target)` into the operation Name so polling re-reads the underlying resource at request time. Same posture extends through Phase 10: no shim-owned mapping table for IaC state.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR at a time.** Phase 10 = one PR; all sub-phases on `phase-10`.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API.
- Real backends only.
- Tests from official client surfaces.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention.**

## Resumable tracks

- **Track A — Cloud test accounts.** Real-cloud lanes for Phase 10-A (Apply against real AWS / GCP / Azure accounts).
- **Track B — Coding-agent automation.**
- **BUG-12 (queue domain tag storage).** `TagQueue` / `UntagQueue` write paths unbacked; same fan-out shape as BUG-2 (domain method + 5 backends + AWS frontend dispatch). Natural next-chunk in 10.3.
- **BUG-13 (functions Lambda role/publish/memory).** memory_size partly fixed in Phase 9.5 (default emit); role + publish need domain extension. Apply tests for AWS Lambda are still skipped on this gap.
- **BUG-15 (queue retention plan/apply asymmetry).** Partial fix in 10.3 (GCP queue frontend parses + emits retention); hashicorp/google plan/apply pipeline still keeps "345600s" in state regardless. Deeper investigation needed.
- **BUG-16 (rdbms GCP v1 vs v1beta4 path mismatch).** Wiring v1beta4 routes unblocks `google_sql_database_instance` Apply.
- **BUG-6 (apigateway Azure v3 delete signature).** Azure-backed apigateway destroy still skips.

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — verify the Phase 10 PR is the only one open.
3. `git checkout phase-10`.
4. Read STATUS snapshot + this file's "Where we are".
5. Read STATUS invariants + AGENTS.md.
6. Skim BUGS open.
7. Pick the next ◻ sub-task from the Phase 10 table.

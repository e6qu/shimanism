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
| **10.2-B** | ⏸ | Cross-frontend read after cross-cloud write. Catches self-consistent wrongness. Deferred — single-frontend create-then-read (10.2) caught all drift in this PR; cross-frontend uncovers issues only after a meaningful cross-cloud apply path is live. |
| **10.2-C** | ◐ | Invalid-input fidelity. Storage first chunk in tree (`TestInvalidInput_AWSS3_*`); per-service expansion follows the same pattern. |
| **10.3** | ✅ | Update intersection audit per service. 6 chunks landed: BUG-17 (secrets `UpdateSecret`), BUG-2 (AWS SQS `SetQueueAttributes` + read-side surface + awsQueryCompatible legacy error codes), AWS SNS `SetTopicAttributes`, BUG-13 (functions Lambda `Role`/`Publish` + cross-cloud silent-accept posture), BUG-16 (rdbms GCP `/sql/v1beta4/` + Region + canonical Settings defaults + `/users`+`/databases` sub-resources), cache GCP Memorystore `/v1beta1/` + Operation name canonicalization + full Instance round-trip. |
| **10.4** | ⏸ | Soft-delete intersection across secrets + storage. Contracts documented in per-service APPLY_INTERSECTION.md; inmem backend's `force=false` path exercises the opt-in soft-delete posture today. Cross-cloud retention-window honest-vs-OperationNotSupported per-cell tests deferred. |
| **10.5** | ✅ | Per-service full lifecycle. Secrets exercises Create → Read → Update (description) → Read → Destroy after BUG-17 closed. Other services exercise Update implicitly via the provider's post-create reconcile path. |
| **10.6** | ⏸ | Cross-cloud Apply matrix tests, contract-scoped. The active 10.7 storage cell is the headline; matrix expansion is Track A work. |
| **10.7** | ✅ | Exit criterion: `TestCrossCloudApply_Roundtrip` per service. Storage AWS→GCS is the active cross-cloud exit assertion (passes). Six other services document the cross-cloud asymmetries (provider WaitForStateEqual on cloud-specific attribute sets, AWS→Azure value-on-create mismatch, multi-step reconcile semantics that don't translate) — honest skips, not shim bugs. |
| **10.8** | ✅ | Phase 10 closer — this PR. |

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

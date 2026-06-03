# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up where Phase 16 left off.

## Where we are

**Phase 16.C PR3 in progress** (branch `phase-16c-instances-pr3`). AWS Terraform `aws_instance` lifecycle now passing. Cross-cloud Apply cell written. CLI instance tests added (GCP + AWS). Remaining: merge PR3, then do 16.C PR4 (az CLI + GCP TF + Azure TF).

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A, blocked on real-cloud credentials — not actionable).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Check if PR3 is already merged; if so, create `phase-16c-instances-pr4` from `main`.
3. Continue with 16.C PR4 items below.

## Phase 16 sub-phase checklist

### 16.A — Normalization audit + scoping + ec2Query codegen ✅ (PR #104)

All items closed.

### 16.B — VPC networking primitives ✅ (PRs #105–#108)

All items closed. Full 3×4×3 conformance matrix for networking operations.

### 16.D — Load balancers ✅ (PRs #109–#110)

All items closed. Full 3×4×3 conformance matrix for LB operations. Sockerless RegisterTargets lane gated on #373–#375.

### 16.C — Compute instance lifecycle ◐ (PRs #111–#112 merged; PR3 in progress)

**PR1 ✅ (PR #111):** domain.Instances + inmem + AWS EC2 frontend + BUG-55.

**PR2 ✅ (PR #112):** GCP Compute + Azure Compute frontends + real backends + SDK conformance.

**PR3 (branch `phase-16c-instances-pr3`) — ready to merge:**
- [x] Terraform `aws_instance` lifecycle: apply + plan + destroy (destroy waiter fix: keep terminated instances in inmem store; use ID-scoped visibility).
- [x] AWS CLI instance conformance tests (aws_instances_cli_test.go).
- [x] GCP CLI instance conformance tests (gcp_instances_cli_test.go) with proper empty-output skip.
- [x] Cross-cloud Apply cell: `TestCrossCloudApply_Roundtrip_Compute_AWStoGCP`.
- [x] TF_LOG debug removed from Terraform test.
- [x] immem `TerminateInstances` keeps instance in store (state=terminated) so Terraform destroy waiter sees terminal state.
- [x] `DescribeInstances` by ID returns terminated instances; list-all excludes them (mirrors AWS behavior).
- [x] `ec2StateToDomain` reverse mapping + `instance-state-name` filter parsing in AWS adapter.
- [x] AWS SDK test + inmem unit test updated to reflect corrected terminated-visibility semantics.
- [x] GCP CLI machine-types + instances list: empty-output skip (gcloud exits 0 after 401).
- [x] Continuity docs updated.

**PR4 (to create after PR3 merges):**
- [ ] GCP Terraform conformance: `hashicorp/google google_compute_instance` apply + destroy.
- [ ] Azure Terraform conformance: `hashicorp/azurerm azurerm_linux_virtual_machine` apply + destroy.
- [ ] Azure CLI conformance: `az vm create/show/list/delete` (requires az binary).
- [ ] Sockerless instance lane (gated: t.Skip referencing #373/#374/#375 until they close).
- [ ] 16.C INTERSECTION.md additions for instance lifecycle.

## Upstream watch

Sockerless gaps blocking 16.C instance lane + 16.D RegisterTargets:

- [sockerless #373](https://github.com/e6qu/sockerless/issues/373) — `DetectFirecrackerCapabilities()` missing `/dev/kvm` check. **Blocks 16.C sockerless lane.**
- [sockerless #374](https://github.com/e6qu/sockerless/issues/374) — 3 GB rootfs per VM risks disk exhaustion on 14 GB runners. **Blocks 16.C sockerless lane.**
- [sockerless #375](https://github.com/e6qu/sockerless/issues/375) — kernel + rootfs downloaded fresh every run; no `actions/cache`. **Blocks 16.C sockerless lane.**

## Standing rules

- File sockerless issues for any gap found during shim work; never paper over gaps in shim test code.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in [BUGS.md](BUGS.md) before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file + provenance; mirrors CI `codegen deterministic`.
- `make spec-freshness` — informational; weekly CI workflow surfaces upstream spec drift.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane.

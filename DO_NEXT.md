# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up where Phase 16 left off.

## Where we are

**Phase 16.C PR4 merged as #114.** GCP `google_compute_instance` TF conformance green in CI. Azure TF/CLI rows deferred on BUG-56/BUG-57.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A) · BUG-56 · BUG-57 (Azure compute JWKS).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Pick up next task below.

## Phase 16 sub-phase checklist

### 16.A — Normalization audit + scoping + ec2Query codegen ✅ (PR #104)

All items closed.

### 16.B — VPC networking primitives ✅ (PRs #105–#108)

All items closed. Full 3×4×3 conformance matrix for networking operations.

### 16.D — Load balancers ✅ (PRs #109–#110)

All items closed. Full 3×4×3 conformance matrix for LB operations. Sockerless RegisterTargets lane gated on #373–#375.

### 16.C — Compute instance lifecycle ◐ (PRs #111–#114 merged; Azure TF/CLI blocked)

**PR1 ✅ (#111):** domain.Instances + inmem + AWS EC2 frontend + BUG-55.
**PR2 ✅ (#112):** GCP + Azure Compute frontends + real backends + SDK conformance.
**PR3 ✅ (#113):** AWS TF `aws_instance` + AWS+GCP CLI + cross-cloud Apply cell.
**PR4 ✅ (#114):** GCP TF `google_compute_instance` (Linux-only) + Azure TF/CLI deferred + INTERSECTION.md.

**Remaining for 16.C closure:**
- [x] Sockerless instance lane unblocked: `TestSockerless_EC2_Instances_ThroughShim` + `TestSockerless_ELBv2_Through_Shim_RegisterTargets` — merged as PR #116.
- [ ] **BUG-56/57 (Azure compute JWKS):** add `HandlerWithConfig` to `azure_compute` frontend (same pattern as `azure_dns.HandlerWithConfig`); wire JWKS from sockerless's Entra stub; re-enable `TestTerraformAzure_Compute_VMLifecycle` + `TestAzureCLI_Compute_VMList`.

## Upstream watch

Sockerless #373/#374/#375 **closed** by PR #372 (merged 2026-06-02). Instance lanes unblocked.

Sockerless PR #392 merged: GCP SA keys + instance templates + SDK/CLI/Terraform tests.

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

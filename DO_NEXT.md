# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phase 16 is complete. Pick up the next phase.

## Where we are

**Phase 16 complete.** All sub-phases closed (16.A ✅ 16.B ✅ 16.C ✅ 16.D ✅). PRs #104–#120. Only Track A bugs remain (BUG-8 · BUG-15 · BUG-41 — blocked on real GCP credentials).

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Start the next phase (see PLAN.md for what follows Phase 16).

## Phase 16 — all closed ✅ (PRs #104–#120)

| Sub-phase | PRs | Status |
|---|---|---|
| 16.A — ec2Query codegen + normalization | #104 | ✅ |
| 16.B — VPC networking primitives | #105–#108 | ✅ |
| 16.D — Load balancers | #109–#110 | ✅ |
| 16.C — Compute instance lifecycle | #111–#120 | ✅ |

16.C highlights: AWS TF `aws_instance` (destroy waiter fix), GCP TF `google_compute_instance`, Azure CLI `az vm` (BUG-57), Azure TF `azurerm_linux_virtual_machine` via sockerless passthrough (BUG-56), sockerless Firecracker instance + LB RegisterTargets lanes (sockerless #373/#374/#375).

## Upstream watch

All Firecracker blockers resolved. Sockerless PRs #372/#392/#395 merged.

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

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up where Phase 16 left off.

## Where we are

**Phase 16.C PR4 in progress** (branch `phase-16c-instances-pr4`). GCP `google_compute_instance` Terraform test written (Linux-only, TLS server). Azure TF/CLI tests explicitly skipped on BUG-56/BUG-57 (configurable JWKS in azure_compute frontend). INTERSECTION.md extended with 16.C instance/machine-type tables.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A) · BUG-56 · BUG-57 (Azure compute JWKS).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Continue with PR4 commit + push + PR creation (or next phase if PR4 already merged).

## Phase 16 sub-phase checklist

### 16.A — Normalization audit + scoping + ec2Query codegen ✅ (PR #104)

All items closed.

### 16.B — VPC networking primitives ✅ (PRs #105–#108)

All items closed. Full 3×4×3 conformance matrix for networking operations.

### 16.D — Load balancers ✅ (PRs #109–#110)

All items closed. Full 3×4×3 conformance matrix for LB operations. Sockerless RegisterTargets lane gated on #373–#375.

### 16.C — Compute instance lifecycle ◐ (PRs #111–#113 merged; PR4 in progress)

**PR1 ✅ (PR #111):** domain.Instances + inmem + AWS EC2 frontend + BUG-55.

**PR2 ✅ (PR #112):** GCP Compute + Azure Compute frontends + real backends + SDK conformance.

**PR3 ✅ (PR #113):** AWS TF `aws_instance` lifecycle (destroy waiter fix) + AWS+GCP CLI conformance + cross-cloud Apply cell.

**PR4 (branch `phase-16c-instances-pr4`) — ready to push + create PR:**
- [x] GCP Terraform `google_compute_instance` apply + destroy (Linux-only TLS test, `StartComputeServerGCPTLS`).
- [x] GCP compute frontend: project-level `GET /projects/{p}` stub + `global/images` stub + `zones/{z}/disks` stub + boot disk in `domainInstanceToGCP`.
- [x] Harness: `ComputeServerTLS` type + `StartComputeServerGCPTLS`.
- [x] Azure TF test: skipped on BUG-56 (configurable JWKS in azure_compute).
- [x] Azure CLI test: skipped on BUG-57 (same prereq).
- [x] INTERSECTION.md: Phase 16.C instances + machine types tables + out-of-intersection list.
- [x] BUGS.md: BUG-56 + BUG-57 filed.
- [ ] **Commit + push + open PR #114.**

**Next after PR4 merges:**
- Resolve BUG-56/BUG-57 to complete the Azure conformance matrix (add `HandlerWithConfig` to azure_compute, wire JWKS from sockerless).
- Close Phase 16.C with full 3×4×3 matrix once Azure lanes unblock.
- Sockerless instance lane: write tests (same structure as other cloud tests — no special cases) once sockerless #373/#374/#375 close.

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

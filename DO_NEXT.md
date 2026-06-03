# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phase 16 complete. Phase 17 in progress.

## Where we are

**Phase 17.A in progress** (branch `phase-17a-block-storage`). Block storage domain + inmem + AWS EC2 frontend + SDK/CLI/Terraform conformance. AWS lane complete; all tests pass.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout phase-17a-block-storage` — resume in-flight branch.
2. Continue with Phase 17 items below.

## Phase 16 ✅ (PRs #104–#120)

All sub-phases closed. See PLAN.md and WHAT_WE_DID.md for narrative.

## Phase 17 — Block Storage ◐

### 17.A — Domain + inmem + AWS lane ◐ (in progress)

- [x] `internal/compute/domain/volumes.go` — Volume + Snapshot types + BlockStorage interface.
- [x] `services/compute/backends/inmem/` — Full BlockStorage implementation (volumes map + snapshots map).
- [x] `services/compute/codegen.json` — Added CreateVolume, DeleteVolume, AttachVolume, DetachVolume, CreateSnapshot, DeleteSnapshot, DescribeSnapshots (7 new operations; 45 total).
- [x] `internal/compute/frontends/aws_ec2/adapter.go` — All 7 handlers + domainVolumeToGen/domainSnapshotToGen (CreateTime non-nil for provider compatibility).
- [x] `services/compute/backends/aws/` — Real AWS EBS backend (CreateVolume/Attach/Detach/Delete/Snapshots).
- [x] `services/compute/backends/k8scompute/` — NotImplemented stubs (volumes + snapshots are out of K8s intersection).
- [x] `services/compute/conformance/aws_ebs_test.go` — SDK: VolumeLifecycle + SnapshotLifecycle.
- [x] `services/compute/conformance/aws_ebs_cli_test.go` — CLI: VolumeLifecycle + SnapshotLifecycle.
- [x] `services/compute/conformance/aws_ebs_terraform_test.go` — TF: aws_ebs_volume apply + destroy.
- [x] `docs/normalizations.md` — N28 (volume size GiB, type opaque, attach synchronous in domain).
- [x] `services/compute/INTERSECTION.md` — Phase 17 volumes + snapshots tables.
- [ ] **Commit + push + open PR.**

### 17.B — GCP Compute + Azure Compute frontends + real backends

- [ ] GCP: `disks.insert/delete/get/list` + `instances.attachDisk/detachDisk` + `snapshots.*`
- [ ] Azure: `Microsoft.Compute/disks` + `Microsoft.Compute/snapshots`
- [ ] GCP real backend + GCP SDK/CLI/TF conformance tests
- [ ] Azure real backend + Azure SDK/CLI/TF conformance tests

### 17.C — K8s peer + sockerless lane

- [ ] K8s: PersistentVolume + PersistentVolumeClaim lifecycle
- [ ] Sockerless: EBS volumes + snapshots through shim

## Upstream watch

All Firecracker blockers resolved. Sockerless PRs #392/#395 merged.

## Standing rules

- File sockerless issues for any gap found during shim work.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in BUGS.md before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file; mirrors CI.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane.

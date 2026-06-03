# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phase 16 complete. Phase 17 in progress.

## Where we are

**Phase 17.B in progress** (branch `phase-17b-block-storage-gcp-azure`). GCP + Azure disk/snapshot frontends + real backends + SDK conformance. SDK lanes green. 17.A merged as #122.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout phase-17b-block-storage-gcp-azure` — resume in-flight branch.
2. Continue with Phase 17 items below.

## Phase 16 ✅ (PRs #104–#120)

All sub-phases closed. See PLAN.md and WHAT_WE_DID.md for narrative.

## Phase 17 — Block Storage ◐

### 17.A — Domain + inmem + AWS lane ✅ (PR #122)

domain.BlockStorage + inmem + AWS EBS frontend/backend + K8s NotImplemented + SDK/CLI/TF conformance + N28 + INTERSECTION.md.

### 17.B — GCP + Azure frontends + real backends ◐ (in progress)

- [x] GCP frontend: `disks.insert/get/list/delete` + `snapshots.insert/get/list/delete` + `instances.attachDisk/detachDisk` (`internal/compute/frontends/gcp_compute/server.go`).
- [x] GCP real backend: `Disks.*` + `Snapshots.*` + `Instances.AttachDisk/DetachDisk` (`services/compute/backends/gcp/gcp.go`).
- [x] Azure frontend: `Microsoft.Compute/disks` + `Microsoft.Compute/snapshots` createOrUpdate/get/list/delete (returns 200 — armcompute disk/snapshot poller expects 200/202, not 201).
- [x] Azure real backend: `DisksClient` + `SnapshotsClient` + attach/detach via VM `dataDisks` update.
- [x] GCP SDK conformance: `gcp_disks_test.go` (Disk, Snapshot, AttachDetach lifecycles).
- [x] Azure SDK conformance: `azure_disks_test.go` (Disk, Snapshot lifecycles).
- [x] inmem CreateVolume stores Name from Tags["Name"] (GCP/Azure disks are name-addressed).
- [ ] GCP + Azure CLI conformance (gcloud compute disks/snapshots; az disk/snapshot).
- [ ] GCP + Azure Terraform conformance (google_compute_disk; azurerm_managed_disk).
- [ ] **Commit + push + open PR.**

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

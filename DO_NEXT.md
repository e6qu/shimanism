# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–19 complete. Next substantive work is Phase 20 (Event Streaming).

## Where we are

**Phase 19 (Key Management) is complete** — PRs #127 (19.A) / #128 (19.B) / #129 (19.C) / #130 (19.D CLI/TF) / #131 (19.D sockerless), all merged. KMS across AWS/GCP/Azure + K8s: domain + inmem (real AES-256-GCM) + all three frontends + real backends + full SDK/CLI/TF conformance + all sockerless lanes green with **zero skips**. All four KMS sockerless gaps closed upstream (#407/#413/#419/#423).

**Phase 18 — Container Registry is complete.** OCI Distribution `/v2/` data plane (shared hand-written router) + ECR/AR/ACR control planes + connected backends. See [docs/phase-18-scoping.md](docs/phase-18-scoping.md), [services/registry/INTERSECTION.md](services/registry/INTERSECTION.md), and [services/registry/APPLY_INTERSECTION.md](services/registry/APPLY_INTERSECTION.md). 18.A–18.D landed across PRs #132–#141: GCP AR, AWS ECR, and Azure ACR frontends all have SDK/CLI/Terraform control-plane coverage plus go-containerregistry OCI data-plane coverage; connected backends now include CNCF Distribution, AWS ECR, GCP Artifact Registry, Azure ACR, and inmem.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials) · BUG-64/65/66 (sockerless registry `/v2/` gaps; external issues pending user approval for those specific reports).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Start Phase 20 (Event Streaming) from [PLAN.md § Phase 20](PLAN.md#phase-20--event-streaming).

## Phase 20 — Event Streaming ◐

Phase 20 is planned around ordered, partitioned event streams with a Kafka-compatible data plane. The first PR should be 20.A scoping: prove the intersection, define the domain, choose the honest K8s peer shape, and decide how much Kafka wire protocol to implement in the first slice.

Suggested first chunk:

- [ ] Draft `docs/phase-20-scoping.md`: Kafka data-plane boundary, control-plane/resource intersection, out-of-intersection features, and normalization rules.
- [ ] Inspect official specs/source APIs for AWS MSK, GCP Managed Service for Apache Kafka, and Azure Event Hubs/ARM.
- [ ] Decide K8s peer: Strimzi/Redpanda/Apache Kafka as connected backend vs an in-tree `shima<service>` peer. Prefer real Kafka-compatible OSS if it can satisfy the intersection honestly.
- [ ] Define the service domain under `internal/eventstream/domain` only after the scoping doc settles the minimum operation set.

## Phase 18 — Container Registry ✅ COMPLETE

Closed by PRs #132–#141. Registry sockerless tests are wired and fail loud on simulator gaps only: BUG-64 (AWS ECR no `/v2/`), BUG-65 (GCP AR chunk `PATCH` 405), and BUG-66 (Azure ACR upload start 404). Ask the user before filing those specific upstream issues at `github.com/e6qu/sockerless`.

## Phase 19 — Key Management ✅ COMPLETE

Closed by PRs #127–#131. KMS has all three frontends, real AWS/GCP/Azure backends, K8s NotImplemented, full SDK/CLI/Terraform conformance, and all sockerless lanes green with zero skips.

## Phase 17 ✅ (PRs #122–#125)

Block storage — AWS/GCP/Azure SDK+CLI+Terraform, K8s PVC volume CRUD, sockerless EBS. Provider wire-quirks absorbed in-shim: EBS `CreateTime` nil-deref, Azure disk/snapshot 200-vs-201 poller, GCP `sizeGb` unquoted-vs-`,string`.

## Upstream watch

All Firecracker blockers resolved. Sockerless PRs #392/#395 merged.

## Standing rules

- **One PR open at a time.** Before opening any PR, check `gh pr list --state open`; if one exists, ask the user first. Close stale/superseded PRs.
- **Fix shim problems in the shim.** Only ever file with `github.com/e6qu/sockerless` (real sockerless-side gaps, after asking) — never Hashicorp or any other upstream.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in BUGS.md before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file; mirrors CI.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane.

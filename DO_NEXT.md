# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–19 complete. Next substantive work is Phase 20 (Event Streaming).

## Where we are

**Phase 19 (Key Management) is complete** — PRs #127 (19.A) / #128 (19.B) / #129 (19.C) / #130 (19.D CLI/TF) / #131 (19.D sockerless), all merged. KMS across AWS/GCP/Azure + K8s: domain + inmem (real AES-256-GCM) + all three frontends + real backends + full SDK/CLI/TF conformance + all sockerless lanes green with **zero skips**. All four KMS sockerless gaps closed upstream (#407/#413/#419/#423).

**Phase 18 — Container Registry is complete.** OCI Distribution `/v2/` data plane (shared hand-written router) + ECR/AR/ACR control planes + connected backends. See [docs/phase-18-scoping.md](docs/phase-18-scoping.md), [services/registry/INTERSECTION.md](services/registry/INTERSECTION.md), and [services/registry/APPLY_INTERSECTION.md](services/registry/APPLY_INTERSECTION.md). 18.A–18.D landed across PRs #132–#141: GCP AR, AWS ECR, and Azure ACR frontends all have SDK/CLI/Terraform control-plane coverage plus go-containerregistry OCI data-plane coverage; connected backends now include CNCF Distribution, AWS ECR, GCP Artifact Registry, Azure ACR, and inmem.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials) · BUG-65/66/67 (sockerless registry `/v2/` gaps filed upstream as sockerless#451/#452/#465).

**Current branch:** `phase-20-eventstream-20a` starts Phase 20.A with the event-stream domain, a real inmem append-only partition log backend, and service/intersection docs. PR #146 is merged; `dupl` is strict in normal lint/CI and BUG-64 is closed.

## Session-start checklist

1. Stay on `phase-20-eventstream-20a` unless the PR has already merged; if it has, sync `main` before starting the next branch.
2. Continue Phase 20.A from [PLAN.md § Phase 20](PLAN.md#phase-20--event-streaming) and [docs/phase-20-scoping.md](docs/phase-20-scoping.md).

## Phase 20 — Event Streaming ◐

Phase 20 is planned around ordered, partitioned event streams with a Kafka-compatible data plane. The current PR is a 20.A foundation slice: scoping is done, the domain is defined, Strimzi is the honest K8s peer choice, and the inmem backend is a real append-only partition log. The Kafka wire runtime is deliberately not faked in this slice.

Current 20.A checklist:

- [x] Draft `docs/phase-20-scoping.md`: Kafka data-plane boundary, control-plane/resource intersection, out-of-intersection features, and normalization rules.
- [x] Inspect official specs/source APIs for AWS MSK, GCP Managed Service for Apache Kafka, Azure Event Hubs/ARM, Kafka protocol, and Strimzi.
- [x] Decide K8s peer: Strimzi Kafka as a connected backend.
- [x] Define `internal/eventstream/domain`.
- [x] Add `services/eventstream/backends/inmem` as a real append-only partition log, with tests for topic lifecycle, produce/fetch, offsets, retention, and validation.
- [x] Publish `services/eventstream/INTERSECTION.md` and `docs/services/eventstream.md`.
- [ ] Run full local verification (`go test`, `make lint`, code-health audits as practical).
- [ ] Open the Phase 20.A PR.
- [ ] Monitor the Phase 20.A PR and fix CI until green. Do not merge it.

## Phase 18 — Container Registry ✅ COMPLETE

Closed by PRs #132–#141. Registry sockerless tests are wired and fail loud on simulator gaps only: BUG-65 (GCP AR chunk `PATCH` 405, [sockerless#451](https://github.com/e6qu/sockerless/issues/451)), BUG-66 (Azure ACR upload start 404, [sockerless#452](https://github.com/e6qu/sockerless/issues/452)), and BUG-67 (AWS ECR manifest `HEAD` 400, [sockerless#465](https://github.com/e6qu/sockerless/issues/465)). BUG-64 / sockerless#450 is closed on current sockerless main; PR #146 replaced the old missing-`/v2/` probe with a real AWS ECR through-shim push/pull attempt.

## Code Health Audit Baseline ◐

- [x] Research current Go dead-code and duplicate-code tooling.
- [x] File registry sockerless simulator issues after user approval.
- [x] Add advisory `make duplication-audit`, `make deadcode-audit`, and `make code-health` targets.
- [x] Publish [docs/code-health.md](docs/code-health.md) with triage rules and cleanup order.
- [x] Open and merge the baseline PR (#143).
- [x] Refactor `cmd/shim/cache.go` / `cmd/shim/rdbms.go` runner duplication without hiding backend/frontend errors.
- [x] Open and merge the `cmd/shim` cleanup PR (#144).
- [x] Extract focused helpers for repeated Terraform apply bodies in `services/secrets/conformance/sockerless_test.go`.
- [x] Review and deduplicate compute inmem and K8s duplicate helpers while preserving domain-specific behavior.
- [x] Open and merge the remaining duplicate-code cleanup PR (#145).
- [x] Make duplicate-code detection strict in `make lint` / CI.
- [x] Triage small dead-code findings and delete confirmed unused helpers.
- [x] Open and merge the code-health closeout + Phase 20 scoping PR (#146).
- [ ] Next code-health candidate: continue hand-written `deadcode` triage one package at a time after Phase 20.A PR is green.

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

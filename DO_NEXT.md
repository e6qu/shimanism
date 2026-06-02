# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up Phase 15.C without re-deriving context.

## Where we are

Phase 15 (cross-cloud normalization + new services): 15.A ✅ · 15.B ✅ · 15.D ✅ · **15.C in flight.**

15.C NoSQL service is structurally complete: DynamoDB + Firestore + Cosmos Tables + etcd backends and frontends all shipped (PRs #90–#96). Cosmos Tables ARM passthrough + metadata + bearer + Terraform conformance landed in PRs #97–#98.

**In-flight:** `az cosmosdb table` CLI conformance on branch `15c-cosmos-tables-az-cli-conformance`. `TestAzureCLI_CosmosTable_Lifecycle_ThroughShim` added to `services/nosql/conformance/azure_cli_test.go`. PR pending.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A, blocked on real-cloud credentials) · BUG-50 (Cosmos Tables ARM — foundational + metadata + TF landed PRs #97–#98; CLI conformance is this branch).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. If `/tmp/sockerless` is stale: `git -C /tmp/sockerless pull --ff-only`, rebuild sims, rerun `make sockerless` (43-pass baseline).
3. Create a new branch from `main` for the next PR.
4. Read [STATUS.md](STATUS.md) + this file. Skim BUGS.md § Open.

## Remaining Phase 15.C work

- **PR #97 ✅ PR #98 ✅** — ARM passthrough + metadata + bearer + TF conformance. Merged 2026-06-02.
- **This branch — `az cosmosdb table` CLI conformance** — `TestAzureCLI_CosmosTable_Lifecycle_ThroughShim` mirrors DNS BUG-43 (PR #86 `azure_cli_test.go`). Merge when CI green.
- **After merge: 15.C closure** — WHAT_WE_DID.md Phase 15.C narrative + STATUS.md phase-close update.

Track A (BUG-8 + BUG-15) still blocked on real-cloud credentials — not actionable until infra exists.

## Sockerless rebuild (when needed)

```sh
git -C /tmp/sockerless pull --ff-only
cd /tmp/sockerless/simulators/aws && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-aws .
cd /tmp/sockerless/simulators/gcp && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-gcp .
cd /tmp/sockerless/simulators/azure && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-azure .
cd /Users/zardoz/projects/shimanism && make sockerless
```

The Azure sim requires `SIM_SERVICEBUS_AMQP_LISTEN_ADDR` on a separate port (handled by `scripts/run-sockerless-storage.sh`).

## Upstream watch

- [sockerless PR #357](https://github.com/e6qu/sockerless/pull/357) ✅ — Cosmos + Storage Tables ARM. Merged.
- [sockerless PR #358](https://github.com/e6qu/sockerless/pull/358) ✅ — Linux netns + NAT/IPAM. Merged.
- [sockerless#360](https://github.com/e6qu/sockerless/issues/360) ✅ filed + closed by [PR #361](https://github.com/e6qu/sockerless/pull/361) 2026-06-02 — DynamoDB `DeleteItem ReturnValues=ALL_OLD`.
- [sockerless PR #364](https://github.com/e6qu/sockerless/pull/364) ✅ — GCP/Azure security (nftables NIC filters) + load-balancer data planes. Merged 2026-06-02. No Phase 15 dependency; baseline still 10 packages / all green.

No open blockers for Phase 15.C.

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
- `make sockerless` — through-shim e2e lane (43-pass baseline as of PR #98).

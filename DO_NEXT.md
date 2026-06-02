# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up Phase 15.C without re-deriving context.

## Where we are

Phase 15 (cross-cloud normalization + new services): 15.A ✅ · 15.B ✅ · 15.D ✅ · **15.C in flight.**

15.C NoSQL service is structurally complete: DynamoDB + Firestore + Cosmos Tables + etcd backends and frontends all shipped (PRs #90–#96). Cosmos Tables ARM passthrough + metadata + bearer wiring landed in PRs #97–#98. Two follow-on conformance PRs remain before 15.C closes.

**In-flight:** PR #98 open on branch `15c-cosmos-tables-metadata-and-tf`. Three CI fixes pushed 2026-06-02 (global ARM `/providers/...` routing, DynamoDB `DeleteItem ConditionExpression`, continuity docs). Awaiting CI green.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A, blocked on real-cloud credentials) · BUG-50 (Cosmos Tables ARM — foundational + metadata landed; TF + CLI follow-ons next).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. If `/tmp/sockerless` is stale: `git -C /tmp/sockerless pull --ff-only`, rebuild sims, rerun `make sockerless` (43-pass baseline).
3. Create a new branch from `main` for the next PR.
4. Read [STATUS.md](STATUS.md) + this file. Skim BUGS.md § Open.

## Remaining Phase 15.C work

- **PR #98 — open.** Metadata + bearer wiring. Merge when CI green.
- **Next: `azurerm_cosmosdb_table` Terraform conformance** — `terraform apply`/`destroy` through shim → sockerless ARM. Pattern mirrors DNS BUG-43 (PR #86). Unblocked by sockerless PR #357.
- **Next: `az cosmosdb table` CLI conformance** — `az cosmosdb table create/show/list/delete` with `--endpoint` override. Pattern mirrors DNS BUG-45 (PR #86).
- **Next: 15.C closure** — WHAT_WE_DID.md Phase 15.C narrative + STATUS.md phase-close update.

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

- [sockerless PR #357](https://github.com/e6qu/sockerless/pull/357) — Cosmos + Storage Tables ARM. Unblocks TF + CLI conformance for 15.C.
- [sockerless PR #358](https://github.com/e6qu/sockerless/pull/358) — full Linux netns per VPC + NAT/IPAM. No current Phase 15 dependency.
- [sockerless#360](https://github.com/e6qu/sockerless/issues/360) filed + closed by [PR #361](https://github.com/e6qu/sockerless/pull/361) 2026-06-02 — DynamoDB `DeleteItem ReturnValues=ALL_OLD`. Shim-side fix (`ConditionExpression`) kept as the better approach.

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

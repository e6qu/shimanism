# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up where Phase 15 left off.

## Where we are

**Phase 15 fully closed** (2026-06-02). All sub-phases complete: 15.A ✅ · 15.B ✅ · 15.C ✅ · 15.D ✅.

- 15.C (NoSQL): DynamoDB + Firestore + Cosmos Tables + etcd — PRs #90–#100. All conformance rows green (SDK + CLI + Terraform × AWS/GCP/Azure frontends).
- 15.D (DNS): Route 53 + Cloud DNS + Azure DNS + CoreDNS — PR #89.

**No phase currently in flight.** Next phase is unscoped — needs user direction.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A, blocked on real-cloud credentials — not actionable).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. If `/tmp/sockerless` is stale: `git -C /tmp/sockerless pull --ff-only`, rebuild sims, rerun `make sockerless` (baseline: all 10 packages green).
3. Ask user what comes next.
4. Create a new branch from `main` for the next PR.

## Sockerless rebuild (when needed)

```sh
git -C /tmp/sockerless pull --ff-only
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/aws/simulator-aws /tmp/sockerless/simulators/aws/
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/gcp/simulator-gcp /tmp/sockerless/simulators/gcp/
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/azure/simulator-azure /tmp/sockerless/simulators/azure/
cd /Users/zardoz/projects/shimanism && make sockerless
```

The Azure sim requires `SIM_SERVICEBUS_AMQP_LISTEN_ADDR` on a separate port (handled by `scripts/run-sockerless-storage.sh`).

## Upstream watch

All prior sockerless gaps closed. Recent merges:

- [sockerless PR #361](https://github.com/e6qu/sockerless/pull/361) ✅ — DynamoDB `DeleteItem ReturnValues=ALL_OLD`.
- [sockerless PR #364](https://github.com/e6qu/sockerless/pull/364) ✅ — GCP/Azure security (nftables NIC filters) + load-balancer data planes.
- [sockerless PR #368](https://github.com/e6qu/sockerless/pull/368) ✅ — Azure Entra authorization-code flow (PKCE, ID tokens, refresh tokens). No current shim dependency; relevant if we test interactive auth scenarios.
- [sockerless PR #369](https://github.com/e6qu/sockerless/pull/369) ✅ — Azure SDK local portability (macOS Docker harness, Event Grid resolver). Build infra only.
- [sockerless PR #370](https://github.com/e6qu/sockerless/pull/370) ✅ — Dockerfile build context fixes + `.dockerignore` + `SIM_RUNTIME=process` docs. Build infra only.
- [sockerless PR #372](https://github.com/e6qu/sockerless/pull/372) ✅ — Firecracker VM lifecycle for EC2/GCE/Azure VM simulators: real TAP NIC attachment, kernel + rootfs boot, nftables/NSG filter wiring. Switches CI from arm64 to `ubuntu-latest` (x86_64) for `/dev/kvm` access. No current shim dependency; relevant when Phase 16 covers compute/VMs. Three follow-up issues filed: [#373](https://github.com/e6qu/sockerless/issues/373) (`/dev/kvm` capability check), [#374](https://github.com/e6qu/sockerless/issues/374) (3 GB rootfs disk usage), [#375](https://github.com/e6qu/sockerless/issues/375) (asset caching + runner pinning).

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
- `make sockerless` — through-shim e2e lane (10 packages, all green).

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #10 (Phase 5 — rdbms, full 3 × 5 × 3 matrix + CloudNativePG as K8s peer + psql exit criterion) at `aeadbc8` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-6-cache` — fresh off main, 6.0 scope baseline drafted.
- **Project phase:** **Phase 6 — Managed Redis (control plane only).** Same structural shape as Phase 5. Three frontends (AWS ElastiCache, GCP Memorystore Admin, Azure Cache for Redis) × five backends (inmem + Redis Operator as K8s peer + the three clouds) × three driver types. 6-op intersection (snapshot/restore deferred — cross-cloud Redis snapshot semantics are too divergent). Exit criterion: `redis-cli PING → PONG` through the shim-returned Connection block.

## Phase 6 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **6.0** | ◐ | Scope + design baseline. `services/cache/OPERATIONS.md` captures the 6-op intersection (Create/Delete/Describe/List/Modify/Reboot Instance). Snapshot/restore deferred (AWS S3 vs GCP GCS export vs Azure backup containers vs Redis Operator BackupRestore CRs — too divergent). Same async / stateless / control-plane-only rules as Phase 5. |
| **6.1** | ◻ | Spec ingest. AWS ElastiCache Smithy 2.0 JSON vendored. GCP Memorystore + Azure Cache reused via official SDKs' wire-type packages. |
| **6.2** | ◻ | `internal/cache/domain/` neutral interface — `Cache` (7 methods: 6 ops + HeadInstance probe), Instance, Connection (host, port, auth token, engine version), Status enum (reused shape from Phase 5). |
| **6.3** | ◻ | inmem backend + AWS ElastiCache frontend (awsQuery) + SDK conformance via `aws-sdk-go-v2/service/elasticache`. |
| **6.4** | ◻ | **Redis Operator backend** (K8s peer) via dynamic client + unstructured `Redis` CRs. Same pattern as Phase 5's cnpg backend. |
| **6.5** | ◻ | **AWS ElastiCache passthrough backend** via `aws-sdk-go-v2/service/elasticache`. |
| **6.6** | ◻ | **GCP Memorystore Admin backend** via `cloud.google.com/go/redis/apiv1` or `google.golang.org/api/redis/v1`. |
| **6.7** | ◻ | **Azure Cache for Redis backend** via `armredis`. |
| **6.8** | ◻ | **GCP Memorystore Admin frontend** REST/JSON. |
| **6.9** | ◻ | **Azure Cache for Redis REST frontend** ARM URL shape. |
| **6.10** | ◻ | Conformance matrix `TestCacheMatrix_*`. |
| **6.11** | ◻ | CLI conformance — `aws elasticache`, `gcloud redis instances`, `az redis`. |
| **6.12** | ◻ | Terraform conformance — `hashicorp/aws aws_elasticache_cluster`, `hashicorp/google google_redis_instance`. |
| **6.13** | ◻ | `cmd/shim cache` subcommand. Default `:9500`. Version bump 0.7.0-phase-6. |
| **6.14** | ◻ | CI lane `conformance-redisop`: kind + Redis Operator. |
| **6.15** | ◻ | **redis-cli PING connectivity test**. Provisions a Redis-Operator instance via the shim, opens a real RESP connection through the returned Connection block, runs PING. Phase-6 exit criterion. |
| **6.16** | ◻ | Phase 6 closer. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 6 design notes

**Same shape as Phase 5.** Control plane only — shim provisions, clients connect directly via RESP. Explicit `Status` enum for async lifecycle. Stateless credential handling — auth token returned exactly once at create time. The Redis Operator backend mirrors the cnpg backend's dynamic-client + unstructured-CR pattern.

**Snapshot/restore deferred.** Cross-cloud Redis snapshot semantics are too divergent for a clean intersection at this phase (AWS exports to S3, GCP to GCS, Azure to backup containers, Redis Operator uses BackupRestore CRs with different conventions). Defer to a follow-on if needed.

**Out-of-intersection features (return source-cloud "not supported" error):**
- AWS ElastiCache cluster mode, replication groups, parameter groups, ElastiCache Serverless.
- GCP Memorystore persistence configs, maintenance policies, read replicas.
- Azure Cache for Redis premium-tier features (clustering, persistence, geo-replication).
- Redis Operator Sentinel deployments, custom Redis configs.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-cache test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention** ([AGENTS.md](AGENTS.md#reuse-over-reinvention)): wire types from each cloud's official Go SDK; spec inputs from upstream-canonical sources; auth verification via the cloud's official verifier libraries.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays.
- **Track B — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.
- **BUG-2 (queue / SetQueueAttributes).** Wiring the 9th queue intersection op so `hashicorp/aws aws_sqs_queue` Terraform conformance lifts the ◇-skip. Same gap blocks `aws_sns_topic_subscription` (Phase 4) and contributed to `aws_db_instance` skip (Phase 5).
- **BUG-5 (rdbms / GCP Operations polling endpoint).** Unlocks `gcloud sql instances` + `google_sql_database_instance` cells from Phase 5.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

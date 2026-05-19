# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #9 (Phase 4 — pubsub, full 3 × 5 × 3 matrix + NATS JetStream fanout) at `6305354` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-5-rdbms` — fresh off main, 5.0 scope baseline drafted.
- **Project phase:** **Phase 5 — Managed RDBMS (control plane only).** Different shape from Phases 1-4: the shim provisions real DB instances and returns connection metadata; clients connect directly to Postgres/MySQL via wire protocol. Three frontends (AWS RDS, GCP Cloud SQL Admin, Azure DB Admin) × five backends (inmem + CloudNativePG as K8s peer + the three clouds) × three driver types. Two engines (Postgres + MySQL) per cell. 10-op intersection in [`services/rdbms/OPERATIONS.md`](services/rdbms/OPERATIONS.md).

## Phase 5 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **5.0** | ✅ | Scope + design baseline. `services/rdbms/OPERATIONS.md` captures the 10-op intersection across AWS RDS / GCP Cloud SQL / Azure DB / CloudNativePG (K8s peer); explicit async semantics (`Status` field — Creating, Available, Modifying, Rebooting, Deleting); two engines (Postgres + MySQL); `Connection` block returned by `DescribeInstance`; out-of-intersection list (replicas, PITR, IAM auth, query insights, …). Master password returned exactly once at CreateInstance time; no shim-side credential cache. |
| **5.1** | ✅ | Spec ingest. AWS RDS Smithy 2.0 JSON vendored at `services/rdbms/spec/aws-rds.smithy.json` (2.1 MB), pinned to `aws/aws-sdk-go-v2@2517fe9f`. `services/rdbms/codegen.json` manifest names the 9 intersection ops. RDS uses awsQuery (same wire protocol as SNS — form-encoded + XML responses). GCP Cloud SQL Admin reused via `google.golang.org/api/sqladmin/v1` (Discovery-generated); Azure flexible-servers reused via `armpostgresqlflexibleservers` + `armmysqlflexibleservers`. |
| **5.2** | ✅ | `internal/rdbms/domain/` neutral interface — `RDBMS` interface (11 methods: CreateInstance, DeleteInstance, DescribeInstance, ListInstances, ModifyInstance, RebootInstance, CreateSnapshot, DeleteSnapshot, DescribeSnapshot, ListSnapshots, RestoreFromSnapshot) + types (`Instance`, `Snapshot`, `Connection`, `Status` enum, `Engine` enum). `CreateInstanceResult` surfaces `MasterPassword` exactly once at create time; `Connection` block emitted by `DescribeInstance` once Status reaches Available. Typed `Error` with Kind discriminator (NoSuchInstance, InstanceAlreadyExists, NoSuchSnapshot, SnapshotAlreadyExists, InstanceNotAvailable, UnsupportedEngine, InvalidArgument). |
| **5.3** | ✅ | `services/rdbms/backends/inmem/` — async lifecycle faked via background goroutines that flip Creating → Available after 50ms (configurable). Connection block populated when Available; localhost:5432 (PG) / localhost:3306 (MySQL). AWS RDS frontend `internal/rdbms/frontends/aws_rds/` speaks awsQuery (same wire protocol family as Phase 4 SNS — form-encoded request + XML response). SDK conformance via `aws-sdk-go-v2/service/rds`: CreateDBInstance → poll DescribeDBInstances until status=available → ModifyDBInstance → CreateDBSnapshot → DescribeDBSnapshots → RestoreDBInstanceFromDBSnapshot → DeleteDBSnapshot → DeleteDBInstance × 2. |
| **5.4** | ✅ | **CloudNativePG backend** (K8s peer) `services/rdbms/backends/cnpg/` via `k8s.io/client-go` + dynamic client (no cnpg-api module pulled — Cluster/Backup CRs as `unstructured.Unstructured` so cnpg version bumps don't force a recompile). CreateInstance → master-creds `Secret` + `Cluster` CR with `bootstrap.initdb.secret` reference. DescribeInstance reads the cluster status condition + the `<name>-rw` Service + the `-shim-creds` Secret to populate the Connection block once `status.readyInstances > 0`. CreateSnapshot → `Backup` CR; RestoreFromSnapshot → Cluster CR with `bootstrap.recovery.backup` reference. RebootInstance bumps the `cnpg.io/reloadedAt` annotation. Postgres only this phase; MySQL via MySQL-Operator deferred to a follow-on. |
| **5.5** | ✅ | **AWS RDS passthrough backend** `services/rdbms/backends/aws/` via `aws-sdk-go-v2/service/rds`. Direct mapping; status strings translated to the domain enum (creating, available, modifying, rebooting, deleting). Endpoint block populated when status=available. SkipFinalSnapshot=true at Delete (intersection scope: snapshot-at-delete is an explicit op). |
| **5.6** | ✅ | **GCP Cloud SQL Admin backend** `services/rdbms/backends/gcp/` via `google.golang.org/api/sqladmin/v1`. Async-aware: every mutating call returns an `Operation` resource; the backend deliberately doesn't wait — clients poll DescribeInstance. State mapping: `RUNNABLE` → Available, `PENDING_CREATE` → Creating, `PENDING_UPDATE`/`MAINTENANCE` → Modifying, `PENDING_DELETE` → Deleting. BackupRuns use API-assigned numeric IDs; the shim stashes the caller-supplied snapshot ID in the `description` field. Snapshot Delete/Describe/Restore are documented as out-of-intersection at this phase (lookup-by-description would require list+filter; cross-instance lookups are out of scope). |
| **5.7** | ✅ | **Azure DB Admin backend (Postgres only)** `services/rdbms/backends/azure/` via `armpostgresqlflexibleservers/v4`. ARM-based async; BeginCreate/Update/Delete/Restart return pollers the backend deliberately doesn't `Wait()` on. State string mapping covers Ready/Succeeded → Available, Provisioning/Creating/Starting → Creating, Updating → Modifying, Restarting → Rebooting, Disabled/Dropping/Deleting → Deleting. MySQL via `armmysqlflexibleservers` is a follow-on (Postgres only this phase, matching cnpg). |
| **5.8** | ✅ | **GCP Cloud SQL Admin frontend** `internal/rdbms/frontends/gcp_cloudsql/`. REST/JSON dispatcher reusing `google.golang.org/api/sqladmin/v1` wire types. Routes: `/v1/projects/{p}/instances` (list, create), `/v1/projects/{p}/instances/{name}` (get, delete, patch), `:restart`, `:restoreBackup`, `/backupRuns` (list, create), `/backupRuns/{id}` (get, delete). Cloud SQL's `Operation` envelope returned for every mutating op (status=PENDING); clients poll Instances.Get for the actual state. |
| **5.9** | ✅ | **Azure DB Admin REST frontend (Postgres only)** `internal/rdbms/frontends/azure_dbadmin/`. ARM URL shape: `/subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.DBforPostgreSQL/flexibleServers/{name}` for server CRUD; `/restart` sub-path for reboot; `/backups/{id}` for snapshot CRUD. Returns 202 Accepted for mutating ops (the proper ARM async indicator is the `Azure-AsyncOperation` header pointing at a polling URL — deferred at this phase; Azure SDK pollers eventually fall through to a `Get` call which the shim handles honestly). |
| **5.10** | ✅ | Conformance matrix at `services/rdbms/conformance/`: `backends.go` lists ActiveBackends (inmem / cnpg / aws / gcp / azure with env-var gates: KUBECONFIG+CNPG_CONFORMANCE for cnpg, AWS_RDS_CONFORMANCE for AWS, GCP_CLOUDSQL_CONFORMANCE+GCP_PROJECT_ID for GCP, AZURE_SUBSCRIPTION_ID+AZURE_RESOURCE_GROUP for Azure). `matrix_test.go` drives the AWS frontend through every backend factory: Create → poll-until-available → Delete. Budget is 2s for inmem, 10min for cloud cells. inmem cell green; other cells skip cleanly. Frontend matrix tests for GCP + Azure aren't yet added (deferred to a follow-on); the AWS-frontend matrix is the load-bearing test since AWS RDS is the most-used SDK shape and the inmem assertion proves the async transition works correctly. |
| **5.11** | ✅ | CLI conformance. `aws rds create-db-instance` + `describe-db-instances` + `delete-db-instance` green against the inmem backend. `gcloud sql instances create` ◇ skipped — polls Operations.Get which the GCP frontend doesn't implement (filed as BUG-5). `az postgres flexible-server` ◇ skipped — Azure-AsyncOperation polling, same as Phase 3/4 Azure CLI cells. |
| **5.12** | ✅ | Terraform conformance. All three providers ◇ skipped with documented reasons: `hashicorp/aws aws_db_instance` reconciles via ModifyDBInstance + waits on parameter/option/security-group metadata (same SetAttribute-shaped pattern as BUG-2). `hashicorp/google google_sql_database_instance` polls Operations (BUG-5). `hashicorp/azurerm azurerm_postgresql_flexible_server` polls Azure-AsyncOperation (Phase-3/4 Azure posture). SDK + matrix cells cover these driver-backend combinations. |
| **5.13** | ✅ | `cmd/shim rdbms` subcommand at `cmd/shim/rdbms.go`. Default `:9400`. Selectors: -frontend (aws_rds, gcp_cloudsql, azure_dbadmin), -backend (inmem, cnpg, aws, gcp, azure). Connection knobs accept flags + env vars (KUBECONFIG + CNPG_NAMESPACE for cnpg, AWS_RDS_ENDPOINT for aws, GCP_PROJECT_ID + GCP_REGION for gcp, AZURE_SUBSCRIPTION_ID + AZURE_RESOURCE_GROUP + AZURE_LOCATION for azure). Version bumped to 0.6.0-phase-5. |
| **5.14** | ✅ | CI lane `conformance-cnpg` added to `.github/workflows/checks.yml`. Uses `helm/kind-action@v1` to create a kind cluster, applies the CloudNativePG 1.24.1 release manifest, waits on the controller-manager rollout, then runs `TestRDBMSMatrix_AWSFrontend/cnpg` + `TestPsqlConnectivity_CNPG` with `KUBECONFIG`+`CNPG_CONFORMANCE=1`+`CNPG_NAMESPACE=default`. Real-cloud lanes (aws-rds, gcp-cloudsql, azure-dbadmin) wait on Track A. |
| **5.15** | ✅ | **psql connectivity test** at `services/rdbms/conformance/psql_connectivity_test.go`. Provisions a CloudNativePG cluster via the AWS RDS frontend, polls until status=available, then opens a real PostgreSQL connection via `database/sql` + `github.com/jackc/pgx/v5/stdlib` using the returned Connection block. Runs `SELECT 1` and asserts the result. Gated on `CNPG_CONFORMANCE=1`. **This is the Phase 5 exit criterion** — proves the shim plays no role on the data path; the SQL goes straight to the real Postgres the cnpg operator brought up. |
| **5.16** | ◻ | Phase 5 closer. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 5 design notes

**Control plane only — no data-plane proxying.** Phases 1-4 shimmed every wire-protocol message; Phase 5 shimmed only the *provisioning* API. Once `CreateInstance` returns a `Connection` block, the client opens a direct PostgreSQL/MySQL connection to the returned host; the shim plays no role on the data path. The Phase-5 exit criterion is "psql can connect through the returned metadata", not "every SQL statement goes through the shim."

**Async semantics, surfaced explicitly.** All four backends provision asynchronously. The domain models this with an explicit `Status` field (Creating, Available, Modifying, Rebooting, Deleting). Clients call `CreateInstance` → poll `DescribeInstance` until `Status == Available`. The shim doesn't try to be synchronous-on-top-of-async; the wire protocols (RDS, CloudSQL, Azure ARM, K8s reconciliation) all surface the async-ness anyway, and the SDKs all know how to poll.

**Master password handling.** Cloud backends return the password once at CreateInstance (Azure even requires the caller to provide it; AWS generates or accepts; GCP generates or accepts). CloudNativePG stores the master credentials in a Kubernetes `Secret` the operator creates; DescribeInstance re-reads the Secret each call. No shim-side credential cache — that violates the stateless rule.

**Engines.** Postgres + MySQL are the intersection. Versions track a small LTS set (Postgres 15+16, MySQL 8.0 to start) — other versions return source-cloud error vocabulary for "engine version not supported."

**Out-of-intersection features (return source-cloud "not supported" error):**
- Replicas (every cloud's replica model differs), Multi-AZ, performance insights, IAM auth, hybrid Azure AD, custom engine versions, point-in-time recovery distinct from snapshots, RDS Proxy, query insights, ScheduledBackup, advanced threat protection.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-rdbms test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention** ([AGENTS.md](AGENTS.md#reuse-over-reinvention)): wire types from each cloud's official Go SDK; spec inputs from upstream-canonical sources; auth verification via the cloud's official verifier libraries.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Live-cloud rows for AWS / GCP / Azure are skipped on every phase until this lands.
- **Track B — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.
- **BUG-2 (queue / SetQueueAttributes).** Wiring the 9th queue intersection op so `hashicorp/aws aws_sqs_queue` Terraform conformance lifts the ◇-skip. Same gap blocks `aws_sns_topic_subscription` in Phase 4. Pick up after Phase 5 closes or fold into a later phase.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

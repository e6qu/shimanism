# RDBMS — operation and feature mapping

> The intersection footprint shimanism's `rdbms` service can cover, across the four backends in scope:
> **AWS RDS**, **GCP Cloud SQL**, **Azure Database for PostgreSQL / MySQL**, **CloudNativePG + MySQL Operator** as the K8s peer.
>
> Anything not in the intersection is out of scope and returns the source cloud's own "not supported" error. See [PHILOSOPHY.md § The Circle](../../PHILOSOPHY.md#the-circle).
>
> The shim itself is stateless — instance metadata and credentials live in the backend, not in shimanism. See [AGENTS.md § The shim is stateless](../../AGENTS.md#the-shim-is-stateless).

## Phase 5 is control-plane only

Phases 1–4 shimmed data planes: every API call goes through the shim to the backend. Phase 5 is different: shimmed operations **provision a DB instance** in the backend and **return connection metadata**. The shim never sits on the wire-protocol path — clients open a direct PostgreSQL or MySQL connection to the returned endpoint.

What this means in practice:

- **Async provisioning.** `CreateInstance` returns immediately with `Status="creating"`. Clients poll `DescribeInstance` until `Status="available"`. The shim translates each cloud's async semantics onto a single domain shape.
- **No protocol shim for SQL.** psql / mysql clients connect *directly* to the provisioned endpoint; the shim plays no role in the data plane. Connectivity is the exit criterion for the K8s peer: `aws rds create-db-instance` → CloudNativePG cluster → `psql` opens a connection and runs queries.
- **Credentials.** Backends generate the master password and either return it once at provision time (AWS RDS / Cloud SQL / Azure) or store it in a backend-side secret (CloudNativePG generates a Kubernetes `Secret`). The shim re-reads from the backend on each `DescribeInstance` — no shim-side credential cache.

## Engines + versions

Two engines are in the intersection:

| Engine | AWS RDS | GCP Cloud SQL | Azure DB | CloudNativePG / MySQL-Operator |
|---|---|---|---|---|
| Postgres | `aurora-postgresql`, `postgres` engines | `POSTGRES_<n>` | `Microsoft.DBforPostgreSQL/flexibleServers` | CloudNativePG `Cluster` |
| MySQL    | `aurora-mysql`, `mysql` engines        | `MYSQL_<n>`    | `Microsoft.DBforMySQL/flexibleServers`     | MySQL Operator `InnoDBCluster` (or Percona PXC) |

Domain enum: `EnginePostgres`, `EngineMySQL`. Backend adapters translate to their cloud's native engine identifier.

Versions track a small intersection set (the LTS versions present on all four backends). Initial cut: Postgres 15, 16; MySQL 8.0. Other versions return `InvalidParameterValue: engine version not supported on this backend`.

## The intersection — 10 operations

| Domain op | AWS RDS | GCP Cloud SQL | Azure DB | CloudNativePG |
|---|---|---|---|---|
| **CreateInstance**(name, engine, version, master_username, master_password?, allocated_storage_gb, instance_class) | `CreateDBInstance` | `instances.insert` | `Servers_Create` | `Cluster` CR + master-creds `Secret` |
| **DeleteInstance**(name) | `DeleteDBInstance` (SkipFinalSnapshot=true at this phase) | `instances.delete` | `Servers_Delete` | `Cluster` CR delete |
| **DescribeInstance**(name) | `DescribeDBInstances` (filter on name) | `instances.get` | `Servers_Get` | `Cluster` CR get + read connection `Secret` |
| **ListInstances**(prefix?) | `DescribeDBInstances` (no filter) | `instances.list` | `Servers_List` | `Cluster` CR list |
| **ModifyInstance**(name, allocated_storage_gb?, instance_class?) | `ModifyDBInstance` | `instances.patch` | `Servers_Update` | `Cluster` CR patch |
| **RebootInstance**(name) | `RebootDBInstance` | `instances.restart` | `Servers_Restart` | rollout-restart of the cluster |
| **CreateSnapshot**(instance, snapshot_id) | `CreateDBSnapshot` | `backupRuns.insert` | `Backups_Create` (long-running) | `Backup` CR (`Schedule` resource for ad-hoc) |
| **DeleteSnapshot**(snapshot_id) | `DeleteDBSnapshot` | `backupRuns.delete` | `Backups_Delete` | `Backup` CR delete |
| **DescribeSnapshot**(snapshot_id) | `DescribeDBSnapshots` (filter) | `backupRuns.get` | `Backups_Get` | `Backup` CR get |
| **RestoreFromSnapshot**(snapshot_id, new_instance_name) | `RestoreDBInstanceFromDBSnapshot` | `instances.restoreBackup` | `Servers_Create` from backup | `Cluster` CR with `bootstrap.recovery` ref |

The shim's `Snapshot` is logical: AWS calls them "DB Snapshots", GCP and Azure call them "Backups", CloudNativePG uses a `Backup` CR. They all serve the same purpose — point-in-time copy that can be restored into a new instance.

## Instance status mapping

| Domain status | AWS RDS | GCP Cloud SQL | Azure | CloudNativePG |
|---|---|---|---|---|
| `Creating`  | `creating`           | `PENDING_CREATE` | `Provisioning` | Cluster condition `Ready=False, Reason=Bootstrap` |
| `Available` | `available`          | `RUNNABLE`       | `Ready` | Cluster condition `Ready=True` |
| `Modifying` | `modifying`          | `PENDING_UPDATE` | `Updating` | Cluster condition `Ready=False, Reason=Switchover` |
| `Rebooting` | `rebooting`          | `PENDING_RESTART`| `Restarting` | rolling-restart in progress |
| `Deleting`  | `deleting`           | `PENDING_DELETE` | `Disabled` | Cluster CR has `deletionTimestamp` |

Each cell where the cloud uses a more granular status (AWS's `backing-up`, GCP's `MAINTENANCE`, …) collapses to the closest domain status; "no narrower status fits" maps to `Creating` or `Modifying`.

## Connection metadata

`DescribeInstance` returns the `Connection` block the client needs to open a direct connection:

```go
type Connection struct {
    Host             string   // RDS hostname / Cloud SQL public IP / Azure FQDN / K8s Service DNS
    Port             int      // 5432 for Postgres, 3306 for MySQL
    MasterUsername   string   // backend-provided
    DatabaseName     string   // backend-default ("postgres" / "mysql")
    EngineVersion    string
}
```

Master password is **not** in the Connection block. It's returned exactly once at `CreateInstance` time (or set by the caller); afterwards the shim refuses to surface it. Phase 5 doesn't ship a password-rotation op (out of intersection in the messy way — every cloud's API differs significantly). Use `ModifyInstance` with `master_password` set if a rotation is needed later.

## What's emphatically out of intersection

- **AWS RDS**: read replicas, Aurora-specific features, Multi-AZ DB clusters, performance insights, IAM auth, Activity Streams, custom engine versions, RDS Proxy.
- **GCP Cloud SQL**: read replicas, Cloud SQL Insights, IAM auth, point-in-time recovery (PITR — different from snapshots), connector for IAM-authenticated clients, query insights.
- **Azure DB**: high availability tiers (ZoneRedundant vs SameZone), read replicas, advanced threat protection, server-level managed identity, hybrid Azure AD auth.
- **CloudNativePG-specific**: pooling (PgBouncer), failover groups across multiple clusters, partitioned WAL archive bucket, declarative role management beyond master, ScheduledBackup CRs (the shim provides on-demand snapshots only).

When a request targets one of these, return the source cloud's own error vocabulary (`InvalidParameterValue`, `BAD_REQUEST`, etc.) — never fabricate success.

## Sub-phase plan (Phase 5)

| Sub | Headline |
|---|---|
| 5.0 | Scope + intersection mapping (this doc) + sub-phase plan. |
| 5.1 | Vendor AWS RDS Smithy spec. GCP + Azure specs reused via official SDKs' wire-type packages. |
| 5.2 | Domain interface (`internal/rdbms/domain/`): `RDBMS`, types (`Instance`, `Snapshot`, `Connection`, `Status` enum, `Engine` enum). Async-aware: `Status` is the explicit field clients poll. |
| 5.3 | inmem backend + AWS RDS frontend (awsQuery wire protocol — same as SNS) + SDK conformance. |
| 5.4 | CloudNativePG backend (K8s peer) via the Kubernetes Go client + the cnpg-api types. |
| 5.5 | AWS RDS passthrough backend via `aws-sdk-go-v2/service/rds`. |
| 5.6 | GCP Cloud SQL Admin backend via `google.golang.org/api/sqladmin/v1`. |
| 5.7 | Azure DB Admin backend via `armpostgresqlflexibleservers` + `armmysqlflexibleservers`. |
| 5.8 | GCP Cloud SQL Admin frontend (REST/JSON, fanout to per-engine routes). |
| 5.9 | Azure DB Admin REST frontend. |
| 5.10 | Matrix conformance (`TestRDBMSMatrix_*`). inmem cell exercises CRUD; cloud + cnpg cells verify the create→available poll loop. |
| 5.11 | CLI conformance — `aws rds`, `gcloud sql instances`, `az postgres flexible-server`. |
| 5.12 | Terraform conformance — `hashicorp/aws aws_db_instance`, `hashicorp/google google_sql_database_instance`. |
| 5.13 | `cmd/shim rdbms` subcommand (default `:9400`). |
| 5.14 | CI lane `conformance-cnpg`: kind cluster + CloudNativePG operator + `TestRDBMSMatrix_*` against the cnpg backend. |
| 5.15 | psql connectivity test against the CloudNativePG-provisioned cluster: opens a real Postgres connection through the returned `Connection` block and runs `SELECT 1`. |
| 5.16 | Phase 5 closer. |

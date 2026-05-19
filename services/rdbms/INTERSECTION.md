# RDBMS — intersection inventory

> Phase 9 sub-phase 9.2-A audit. Classification rules in [`services/apigateway/INTERSECTION.md`](../apigateway/INTERSECTION.md).
>
> Control-plane only — Phase 5's exit criterion is the user can `psql` to the connection string the shim returns. Data plane goes direct.

## AWS RDS frontend (awsQuery / XML)

| Op | Category | Status |
|---|---|---|
| CreateDBInstance, DescribeDBInstances, ModifyDBInstance, DeleteDBInstance | 1 | ✅ |
| CreateDBSnapshot, DescribeDBSnapshots, RestoreDBInstanceFromDBSnapshot | 1 — migration-critical (DB migration via snapshot) | ⚠ partial (snapshot + restore deferred to Phase 5.x; in scope but not yet wired across all backends) |
| AddTagsToResource, RemoveTagsFromResource, ListTagsForResource | 1 | ✅ |
| Parameter groups, option groups, subnet groups | 3 — out (vendor-specific configuration knobs) | ◇ |
| Cross-region replication, Multi-AZ, RDS Proxy | 3 — out | ◇ |
| StartDBInstance, StopDBInstance | 1 — migration-critical for cost control | ⚠ partial |

## GCP Cloud SQL Admin frontend

| Op | Category | Status |
|---|---|---|
| Instances.{insert,get,delete,list,patch} | 1 | ✅ |
| Instances.restoreBackup | 1 | ⚠ partial |
| Operations.{get,list} | 1 — async-op polling, REQUIRED for `gcloud sql instances` + `hashicorp/google google_sql_database_instance` | ❌ **BUG-5** |
| Backups, users, databases (sub-resources) | 1 — migration-critical | ⚠ partial |
| SslCerts, BackupRuns | 3 — out (cloud-specific) | ◇ |

## Azure DB Admin frontend (armdbforpostgresql etc.)

| Op | Category | Status |
|---|---|---|
| Servers.{CreateOrUpdate,Get,Delete,List,Update} | 1 | ✅ |
| Databases.{CreateOrUpdate,Get,Delete,List} | 1 | ✅ |
| Firewall rules, configurations, VNet rules | 3 — out (network is its own surface) | ◇ |
| Backup, geo-restore | 1 — migration-critical | ⚠ partial |

## Cross-cloud intersection (migration view)

| User-intent | AWS | GCP | Azure | CNPG | Status |
|---|---|---|---|---|---|
| Provision a DB instance | CreateDBInstance | Instances.insert | Servers.CreateOrUpdate | Cluster CR | ✅ |
| Describe / get connection string | DescribeDBInstances | Instances.get | Servers.Get | Cluster status | ✅ |
| Resize / change tier | ModifyDBInstance | Instances.patch | Servers.Update | Cluster spec edit | ✅ |
| Drop a DB instance | DeleteDBInstance | Instances.delete | Servers.Delete | Cluster delete | ✅ |
| Poll async op | (Marker = synchronous-style) | Operations.get | (polling via ARM provisioning state) | (k8s status) | ⚠ BUG-5 for GCP |
| Snapshot for migration | CreateDBSnapshot | Backups (sub-resource) | Server backups | (volume snapshot) | ⚠ partial across all four |

## Known gaps

- BUG-5: GCP Operations.get not implemented → CLI + TF cells ◇-skipped.
- Snapshot/backup/restore is migration-critical; Phase 9 (or a Phase 5.x follow-on) should close it.

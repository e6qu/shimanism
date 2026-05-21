# RDBMS — Apply intersection contract

> Phase 10 sub-phase 10.0-A. The contract that Phase 10's Apply matrix tests assert against.
>
> Companion to [`INTERSECTION.md`](INTERSECTION.md).

## Resource scope

| Terraform resource | Maps to (source-cloud op family) | Shim domain ops |
|---|---|---|
| `aws_db_instance` | AWS RDS `CreateDBInstance` / `DescribeDBInstances` / `ModifyDBInstance` / `DeleteDBInstance` | `CreateInstance` / `DescribeInstance` / `ModifyInstance` / `DeleteInstance` |
| `google_sql_database_instance` | GCP Cloud SQL Admin `instances.insert/get/patch/delete` | same |
| `azurerm_postgresql_flexible_server` / `azurerm_mysql_flexible_server` | Azure DB Admin `Servers.CreateOrUpdate` / `Servers.Get` / `Servers.Update` / `Servers.Delete` | same |
| `aws_db_snapshot` | `CreateDBSnapshot` / `DescribeDBSnapshots` / `DeleteDBSnapshot` | `CreateSnapshot` / `DescribeSnapshot` / `DeleteSnapshot` |
| `google_sql_database_instance` with `restore_backup_context` | restore-from-backup | `RestoreFromSnapshot` |

## Apply contract — DB instance resource

### Create

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `identifier` / `name` | ✅ | All backends. Globally-unique constraint surfaced via the backend's native conflict error. |
| `engine` | ✅ | `domain.Engine` — Postgres or MySQL only. Other engines (Aurora, Oracle, SQL Server, MariaDB) are out of intersection; shim returns `InvalidParameterValue` envelope. |
| `engine_version` | ✅ | Round-trips through `domain.Instance.EngineVersion`. Per-cloud version-string fidelity tradeoff: AWS `15.4` vs GCP `POSTGRES_15` vs Azure `15`. Translation happens in each backend's adapter; provider HCL must match the *source-cloud*'s version-string format. |
| `instance_class` / `tier` / `sku` | ✅ | `domain.CreateInstanceOptions.InstanceClass`. Per-cloud sizing-tier names (AWS `db.t3.micro` / GCP `db-custom-1-3840` / Azure `Standard_B1ms`). Backends translate; CNPG K8s peer ignores (sizing is per-pod, surfaced via labels at create time). |
| `allocated_storage` | ✅ | `domain.Instance.AllocatedStorageGB`. All four backends honor. |
| `username` / `master_username` | ✅ | `domain.Connection.MasterUsername`. **Phase 9.5 surfaced this as a Create-then-Read drift bug fixed inline.** Round-trip honest. |
| `password` / `master_password` | ⚠ | Write-only attribute. Returned exactly once at Create time per `domain.CreateInstanceResult.MasterPassword`. **Terraform stores the password in state.** Read does *not* re-surface (per stateless-credentials posture). Provider `password` plan-diff is suppressed via `ignore_changes` or sensitive-by-default; users not opting in see a diff every plan. Documented as a known fidelity edge. |
| `db_name` / `database_name` (initial DB) | ✅ | `domain.Connection.DatabaseName`. All four backends honor. |
| `port` | ⚠ | Per-cloud variance: AWS allows configuring; GCP fixed-per-engine; Azure configurable on Flex. Cross-cloud-honest: shim accepts the value on Create, returns the backend's actual port at Read. Provider plans no-drift when `port` matches backend default; explicit non-default port that the backend can't honor returns `InvalidParameterValue`. |
| `publicly_accessible` / `public_network_access_enabled` | ⚠ | Per-backend boolean. In-contract for Create (accepted on all four); read-side honest. Cross-cloud value semantics: AWS `true` ≠ GCP `authorizedNetworks` open ≠ Azure firewall-rule wildcard. Fidelity tradeoff documented per cell. |
| `storage_type`, `storage_encrypted`, `iops`, `kms_key_id` | ◇ | Per-cloud storage-tier names + encryption-at-rest config. Out of contract. |
| `multi_az`, `availability_zone` | ◇ | Cross-cloud HA semantics differ materially. Out of contract. |
| `backup_retention_period`, `backup_window`, `maintenance_window` | ◇ | Per-cloud backup config differs. Out of contract for Phase 10 (snapshot resource is in-contract separately). |
| `vpc_security_group_ids`, `db_subnet_group_name` (AWS), `network` / `private_network` (GCP), `vnet_subnet_id` (Azure) | ◇ | Per-cloud networking. Out of contract. |
| `parameter_group_name`, `option_group_name`, `character_set_name`, `collation_server` | ◇ | Engine-tuning per-backend. Out of contract. |
| `iam_database_authentication_enabled`, `domain` (Active Directory) | ◇ | Auth-mode config per backend. Out of contract. |
| `monitoring_interval`, `enabled_cloudwatch_logs_exports`, `performance_insights_*` | ◇ | Observability config. Out of contract. |
| `tags` / `user_labels` | ◇ | **Not in domain.** Same gap pattern as queue/pubsub. Out of contract for Phase 10. |
| `replicate_source_db`, `replica_mode` | ◇ | Cross-cloud read-replica semantics differ. Out of contract; covered in Phase 10-A. |
| `deletion_protection` | ⚠ | AWS attribute. Cross-cloud: GCP / Azure have equivalents but the semantics differ. For Phase 10, **in-contract for AWS-to-AWS passthrough only**; against other backends, shim returns `OperationNotSupportedException`. |
| `skip_final_snapshot`, `final_snapshot_identifier` | ⚠ | AWS attribute. Phase 10 destroy path: if `skip_final_snapshot = true` shim issues `DeleteInstance` directly. If `false`, shim issues `CreateSnapshot` then `DeleteInstance` (per AWS Destroy semantics); against non-AWS backends without a fully equivalent final-snapshot concept, the shim returns `OperationNotSupportedException`. AWS-to-AWS passthrough honors. |

### Async semantics

Per `domain.go`: every backend provisions asynchronously. `CreateInstance` returns `Status=Creating`; Terraform's provider polls until `Status=Available`. **BUG-5 was the gate** — closed in Phase 10.1; all four GCP-shape frontends now implement `Operations.Get`. Apply against GCP-shape frontends no longer hangs.

`ModifyInstance` returns `Status=Modifying`; Terraform polls. `DeleteInstance` enters `Status=Deleting`; Terraform polls until `NoSuchInstance`.

### Update (`ModifyInstance`)

`domain.ModifyInstanceOptions` supports:

- `AllocatedStorageGB` — **in-place across all backends** (storage grow). Shrink is `OperationNotSupportedException` everywhere.
- `InstanceClass` — in-place on AWS / GCP / Azure (results in restart). CNPG: in-place is a `ForceNew` (pod recreation through CNPG controller).
- `MasterPassword` — in-place on AWS / GCP / Azure. CNPG: in-place by updating the K8s Secret.

ForceNew across all backends:
- `identifier` / `name`
- `engine` (cannot change engine in place)
- `db_name` (initial database is set once)
- `username` / `master_username` (post-create rename is destructive on every backend)

### Delete (`DeleteInstance`)

Async on every backend. `skip_final_snapshot` semantics above; otherwise synchronous-from-Terraform's-perspective via the polling loop until `NoSuchInstance`.

## Apply contract — snapshot resource

### Create (`CreateSnapshot`)

| Attribute | In-contract? | Per-cell honest semantics |
|---|---|---|
| `db_snapshot_identifier` / snapshot id | ✅ | All backends. |
| `db_instance_identifier` / source | ✅ | All backends. Returns `NoSuchInstance` if source doesn't exist. |
| `tags` | ◇ | Same domain gap as instance tags. Out of contract. |

Snapshot Create is async (`Status=Creating`); Terraform polls.

### Update

Snapshots are immutable across the intersection. **Any provider Update returns `OperationNotSupportedException`.**

### Delete

`DeleteSnapshot` synchronous everywhere.

## Apply contract — restore-from-snapshot

`RestoreFromSnapshot(snapshotID, newInstanceName)`. Async create. In-contract for Phase 10 to the extent that the source-cloud HCL exposes a "restore from" attribute on the instance resource:

- AWS: `snapshot_identifier` on `aws_db_instance`.
- GCP: `restore_backup_context` on `google_sql_database_instance`.
- Azure: `source_server_id` + `point_in_time_restore_time` on Flex (PITR, not point-snapshot — fidelity edge, documented).

## Out of contract

Per-cloud tuning + networking + observability + multi-AZ + replica + IAM-auth + backup-policy. All return source-cloud's `OperationNotSupportedException` envelope.

## What this contract commits the shim to

1. Accept the in-contract Create attributes for instance + snapshot resources; round-trip through Read with no drift, *including the username* (Phase 9.5 fixed this — keep it green).
2. Reject out-of-contract attributes with the source cloud's real error envelope.
3. Honor `ModifyInstance` for storage-grow, instance-class change, master-password change in-place; `ForceNew` for identifier / engine / db_name / username changes.
4. Honor async semantics via `Operations.Get` polling (Phase 10.1 closed BUG-5).
5. Honor `skip_final_snapshot` + `final_snapshot_identifier` semantics on AWS-to-AWS only; surface `OperationNotSupportedException` for `skip_final_snapshot = false` against non-AWS backends.
6. Snapshot resource is in-contract for Create / Read / Delete; Update is immutable everywhere.

## Known open BUGs gating this contract

None. BUG-5 was the canonical gate — closed in Phase 10.1.

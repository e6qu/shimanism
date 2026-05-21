# Managed RDBMS (control plane)

Provision and manage Postgres / MySQL instances across clouds. **Control-plane only** — the data plane is wire-protocol Postgres / MySQL; connect directly to the returned host.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS RDS | awsQuery XML | Same wire family as SQS / SNS. |
| GCP Cloud SQL Admin | REST + JSON | Routes accept both `/v1/projects/...` (Go SDK / gcloud) and `/sql/v1beta4/projects/...` (hashicorp/google) — Phase 10.3 BUG-16. |
| Azure DB Admin (ARM) | REST + ARM | Long-polling async-operation URLs. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws` | Real AWS RDS | Passthrough. |
| `gcp` | Real GCP Cloud SQL Admin | Passthrough. |
| `azure` | Real Azure DB | Flexible-server only (single-server EOL). |
| `cnpg` | CloudNativePG operator | K8s peer for Postgres. Dynamic client + unstructured `Cluster` CRs. |
| `mysql-operator` | MySQL Operator for Kubernetes | K8s peer for MySQL. |
| `inmem` | Process-local | Tests + local dev. |

## Async semantics

Every backend provisions asynchronously. `CreateInstance` returns `Status=Creating`; clients poll `DescribeInstance` until `Status=Available`. The shim doesn't fake synchronous-on-top-of-async — the wire protocols + SDKs all surface async-ness anyway.

**Operations.Get polling** (Phase 10.1 / BUG-5):
- GCP Cloud SQL Admin: `/v1/projects/{p}/operations/{op}` + `/sql/v1beta4/projects/{p}/operations/{op}`.
- Operation Name encodes `(opType, target)` — polling client resolves status by re-reading the underlying instance. Stateless.

## Stateless credentials

Master password returned exactly once at `CreateInstance` time, then never re-surfaced. The CNPG backend stores credentials in a Kubernetes Secret; the shim re-reads the Secret on each `DescribeInstance`. **No shim-side credential cache** ([AGENTS.md § stateless](../../AGENTS.md#the-shim-is-stateless)).

## Intersection contracts

- **[`services/rdbms/OPERATIONS.md`](../../services/rdbms/OPERATIONS.md)** — operation list.
- **[`services/rdbms/INTERSECTION.md`](../../services/rdbms/INTERSECTION.md)** — per-frontend classification.
- **[`services/rdbms/APPLY_INTERSECTION.md`](../../services/rdbms/APPLY_INTERSECTION.md)** — Apply contract:
  - In-contract Create: `identifier`, `engine` (postgres/mysql only), `engine_version`, `instance_class`, `allocated_storage`, `username`, `db_name`, `region`.
  - Password is write-only sensitive (returned once at Create, not at Read).
  - Out-of-contract: VPC / subnet / parameter / option groups, multi-AZ, replicas, backup policy, IAM-database-auth, monitoring, encryption.

## Update intersection

`ModifyInstance` in-place: `AllocatedStorageGB` (grow only; shrink is `OperationNotSupported`), `InstanceClass`, `MasterPassword`. `ForceNew`: `identifier`, `engine`, `db_name`, `username`.

## Snapshot resource

`CreateSnapshot` / `DeleteSnapshot` / `DescribeSnapshot` / `ListSnapshots` / `RestoreFromSnapshot` all in-contract. Immutable Update (any provider Update returns `OperationNotSupportedException`).

## Conformance

- `TestRDBMSMatrix_*` — (frontend × backend × driver) cells.
- `TestTerraform_GCPRDBMS_Apply_NoDrift` — GCP frontend Apply through inmem (active drift cell after BUG-16 closure).
- `TestTerraform_AWSRDBMS_Apply_NoDrift` — diamond-skipped (AWS Modify reconcile + subnet/parameter/option-group metadata gap).
- `TestCrossCloudImport_Roundtrip_RDBMSAWStoGCPCloudSQL` (Phase 9.13).
- `TestCrossCloudApply_Roundtrip_RDBMSAWStoGCPCloudSQL` (Phase 10.7) — documented-skip.

## Known gaps

- AWS-shape Apply requires post-create reconcile metadata not in intersection (BUG-2-class) — skipped with pointer.
- Azure ARM ProvisioningState long-polling deferred to Track A.

## Cross-link

- Architecture: [docs/architecture.md](../architecture.md)
- Migration recipes: [services/rdbms/MIGRATION.md](../../services/rdbms/MIGRATION.md)
- Related: [docs/services/cache.md](cache.md) (same control-plane shape).

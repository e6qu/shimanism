# RDBMS importer-read contract

> Phase 9 sub-phase 9.2 — captured from `terraform import aws_db_instance` against the shim's AWS RDS frontend.

## aws_db_instance import — observed wire ops

awsQuery / XML; one endpoint, action selected via the `Action` form field.

| Action | Category | Status |
|---|---|---|
| `DescribeDBInstances` | 1 | ✅ (with fix) |
| `ListTagsForResource` (after) | 2 | ✅ (existing) |

## Fidelity fix surfaced

The initial run had `DescribeDBInstances` returning Status=available + all attributes, but the hashicorp/aws provider still reported "Cannot import non-existent remote object." Root cause: the response was missing two attributes the provider's existence check uses:

- `DBInstanceArn` — the canonical resource ARN.
- `DbiResourceId` — the immutable Amazon-assigned identifier.

These aren't decoration; the provider's import path explicitly checks for them. Fixed by emitting both with deterministic shim values: `arn:aws:rds:us-east-1:000000000000:db:<name>` and `db-<name>`. No state needed; derived from the instance name + a fixed account.

Result: import succeeds. `terraform plan -detailed-exitcode` returns 2 (diff) for parameter-group / option-group / etc. attributes that need write-path support — same category as BUG-13's memory_size/role/publish diffs.

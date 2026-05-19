# RDBMS — migration walkthroughs

> Phase 9 sub-phase 9.2-B. Control plane only. See [INTERSECTION.md](INTERSECTION.md).

## AWS RDS → GCP Cloud SQL

```bash
shim rdbms --addr=:9500 \
  --frontend=aws_rds \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=rdbms --endpoint=http://localhost:9500)"

aws rds create-db-instance --db-instance-identifier prod \
  --engine postgres --master-username admin --master-user-password ...
aws rds describe-db-instances --db-instance-identifier prod
# Connection string ends up in DBInstance.Endpoint.Address — psql goes
# direct to Cloud SQL.
aws rds modify-db-instance --db-instance-identifier prod --db-instance-class db.t3.medium
aws rds delete-db-instance --db-instance-identifier prod --skip-final-snapshot
```

**Walkthrough holds for runtime.** Caveat: `gcloud sql instances` and `hashicorp/google google_sql_database_instance` both hang on the missing GCP Operations.get endpoint (BUG-5). The SDK matrix passes because we surface a synthetic operation marker; the CLI + TF need real polling. Phase 9 picks this up.

## Cloud → CloudNativePG (K8s peer)

```bash
shim rdbms --addr=:9500 \
  --frontend=aws_rds \
  --backend=cnpg --kubeconfig=$HOME/.kube/config &
```

`AWS RDS DBInstance` ↔ `CloudNativePG Cluster CR`. Spec translates; Endpoint comes from `<cluster>-rw.<ns>.svc.cluster.local`.

## Snapshot/backup-driven migration (incomplete)

The cross-cloud "snapshot → restore on the other side" path is partial across all four backends — flagged in INTERSECTION.md. This is the *primary* DB migration mechanism; Phase 9 (or a follow-on 5.x) must close it.

## Coverage

Provisioning + modify + delete green for the intersection. Snapshot/restore + GCP async-poll are the gaps.

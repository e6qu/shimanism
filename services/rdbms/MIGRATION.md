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

## Terraform walkthrough (AWS-shaped provider against a non-AWS backend)

The `hashicorp/aws` provider's `endpoints.rds` block lets existing `aws_db_instance` resources provision against whichever backend the shim is fronting (GCP Cloud SQL, Azure PostgreSQL FlexibleServer, CloudNativePG operator).

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    rds = "http://localhost:9500"
  }
}

resource "aws_db_instance" "shim_db" {
  identifier          = "cross-cloud-db"
  engine              = "postgres"
  engine_version      = "16"
  instance_class      = "db.t3.micro"
  username            = "admin"
  password            = "shim-password"
  allocated_storage   = 20
  skip_final_snapshot = true
}
```

```bash
shim rdbms --addr=:9500 --frontend=aws_rds --backend=gcp --gcp-project=$GCP_PROJECT &

terraform init
terraform apply -auto-approve
# psql -h $(terraform output -raw shim_db_address) -U admin
terraform import aws_db_instance.existing cross-cloud-db
```

Conformance test reference: `services/rdbms/conformance/{terraform_import_test.go, cross_cloud_import_test.go}`.

> **Caveat (BUG-5 / Phase 10.1).** GCP's `Operations.get` polling is stateless across replicas. AWS Terraform's apply path doesn't depend on GCP-shaped polling, but if you front a GCP-shaped client against this RDBMS shim, see `internal/rdbms/frontends/gcp_cloudsql/server.go`'s `reOperation` handler for the stateless polling story (derives status from the resource's current state, no shim-side operation table).

## Snapshot/backup-driven migration (incomplete)

The cross-cloud "snapshot → restore on the other side" path is partial across all four backends — flagged in INTERSECTION.md. This is the *primary* DB migration mechanism; Phase 9 (or a follow-on 5.x) must close it.

## Coverage

Provisioning + modify + delete green for the intersection. Snapshot/restore + GCP async-poll are the gaps.

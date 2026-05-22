# Secrets — migration walkthroughs

> Phase 9 sub-phase 9.2-B. See [INTERSECTION.md](INTERSECTION.md) for per-op coverage.

## AWS Secrets Manager → GCP Secret Manager

```bash
shim secrets --addr=:9100 \
  --frontend=aws_secretsmanager \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=secrets --endpoint=http://localhost:9100)"

aws secretsmanager create-secret --name api/token --secret-string "..."
aws secretsmanager get-secret-value --secret-id api/token        # latest version
aws secretsmanager put-secret-value --secret-id api/token \
    --secret-string "rotated"                                    # new version
aws secretsmanager list-secrets
aws secretsmanager list-secret-version-ids --secret-id api/token
aws secretsmanager delete-secret --secret-id api/token --force-delete-without-recovery
```

**Walkthrough holds.** Every op is in the intersection.

## AWS Secrets Manager → Vault (cloud → K8s peer)

```bash
shim secrets --addr=:9100 \
  --frontend=aws_secretsmanager \
  --backend=vault --vault-addr=$VAULT_ADDR --vault-token=$VAULT_TOKEN &
eval "$(shimctl env --frontend=aws --service=secrets --endpoint=http://localhost:9100)"

# Same ops. Vault KV v2's versioning model maps directly to Secrets
# Manager versions; the shim derives version IDs from Vault's
# metadata at read time (stateless invariant: no shim-side mapping
# table). See WHAT_WE_DID.md § Phase 2 for the versioning translation.
```

**Walkthrough holds.** Phase 2 closed clean.

## Azure Key Vault → AWS Secrets Manager (reverse cross-cloud)

```bash
shim secrets --addr=:9100 \
  --frontend=azure_keyvault \
  --backend=aws &  # AWS Secrets Manager via default credential chain
eval "$(shimctl env --frontend=azure --service=secrets --endpoint=https://localhost:9100)"
# Note: https://, because the Azure SDK refuses to attach bearer tokens
# to plain HTTP. The shim runs TLS for this frontend.

az keyvault secret set --vault-name shimanism --name api/token --value "..."
az keyvault secret show --vault-name shimanism --name api/token
az keyvault secret list --vault-name shimanism
```

**Walkthrough holds.** The 4-part Azure secret URI maps to the underlying inmem/AWS/GCP shape during translation; no shim-side state needed.

## Terraform walkthrough (AWS-shaped provider against a non-AWS backend)

The `hashicorp/aws` provider's `endpoints.secretsmanager` block lets existing `aws_secretsmanager_secret` resources provision against whichever backend the shim is fronting.

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    secretsmanager = "http://localhost:9100"
  }
}

resource "aws_secretsmanager_secret" "shim_secret" {
  name                    = "cross-cloud-secret"
  recovery_window_in_days = 0
}

resource "aws_secretsmanager_secret_version" "shim_secret_v1" {
  secret_id     = aws_secretsmanager_secret.shim_secret.id
  secret_string = "rotated"
}
```

```bash
# Start the shim against your chosen backend.
shim secrets --addr=:9100 --frontend=aws_secretsmanager --backend=gcp --gcp-project=$GCP_PROJECT &

terraform init
terraform apply -auto-approve
terraform import aws_secretsmanager_secret.existing cross-cloud-secret
terraform plan -refresh-only -detailed-exitcode
```

Conformance test reference: `services/secrets/conformance/{terraform_import_test.go, cross_cloud_import_test.go}`.

## Coverage

All three frontends × four backends (12 cells) green. No fakes.

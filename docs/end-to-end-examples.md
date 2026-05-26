# End-to-end examples

This page shows the complete path shimanism is built for:

```text
source-cloud client -> shimanism frontend -> shimanism backend -> destination service
```

The client stays source-shaped. The destination owns the data or control-plane resource.

## Start from cloud credentials

For AWS -> GCP storage, the application, CLI, SDK, and Terraform still speak S3. The shim backend uses Google Application Default Credentials to call real Cloud Storage.

```sh
git clone https://github.com/e6qu/shimanism
cd shimanism
go build -o bin/shim ./cmd/shim

gcloud auth application-default login
export GCS_PROJECT_ID=my-target-gcp-project

bin/shim storage \
  -frontend=aws_s3 \
  -backend=gcs \
  -gcs-project="$GCS_PROJECT_ID" \
  -addr=:9001
```

In another terminal, point AWS-shaped clients at the shim:

```sh
export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
export AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
export AWS_REGION=us-east-1

aws --endpoint-url=http://localhost:9001 s3 mb s3://shim-demo-assets
aws --endpoint-url=http://localhost:9001 s3 cp README.md s3://shim-demo-assets/README.md
aws --endpoint-url=http://localhost:9001 s3 ls s3://shim-demo-assets/
```

The bucket is created in GCS. The client only used S3.

## Optional local simulator testing with sockerless

shimanism is the system under test here: source-cloud clients call a shimanism frontend, and shimanism translates those calls to a destination backend. In production, that destination is usually a real cloud service or a real compatible backend such as MinIO, Vault, NATS, or a Kubernetes operator.

For local testing, you can also run shimanism against [sockerless](https://github.com/e6qu/sockerless). Sockerless is a separate project that provides local AWS-, GCP-, and Azure-shaped simulator processes. Those simulators stand in for cloud control planes so you can exercise real CLI, SDK, and Terraform client paths without requiring cloud accounts or incurring cloud cost. It is a test target, not an application dependency.

The commands below are standalone examples. They start the simulator processes, start shimanism, and then drive shimanism with source-cloud CLIs. They do not rely on the Go unit-test harness.

Prerequisites:

- Go, `aws`, `gcloud`, and `az` installed.
- A running container runtime for sockerless: Docker or Podman.
- Free local ports `14566`, `14567`, `14569`, `19001`, `19002`, and `19003`.

First, build shimanism and the three sockerless simulators:

```sh
git clone https://github.com/e6qu/shimanism
cd shimanism
go build -o bin/shim ./cmd/shim

git clone --depth=1 https://github.com/e6qu/sockerless.git /tmp/sockerless

SOCKERLESS_DIR=/tmp/sockerless
(cd "$SOCKERLESS_DIR/simulators/aws" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o simulator-aws .)
(cd "$SOCKERLESS_DIR/simulators/gcp" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o simulator-gcp .)
(cd "$SOCKERLESS_DIR/simulators/azure" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o simulator-azure .)
```

Start the simulators in one terminal:

```sh
SOCKERLESS_DIR=/tmp/sockerless

SIM_LISTEN_ADDR=:14566 "$SOCKERLESS_DIR/simulators/aws/simulator-aws" >/tmp/sockerless-aws.log 2>&1 &
AWS_SIM_PID=$!

SIM_LISTEN_ADDR=:14567 "$SOCKERLESS_DIR/simulators/gcp/simulator-gcp" >/tmp/sockerless-gcp.log 2>&1 &
GCP_SIM_PID=$!

SIM_LISTEN_ADDR=:14569 "$SOCKERLESS_DIR/simulators/azure/simulator-azure" >/tmp/sockerless-azure.log 2>&1 &
AZURE_SIM_PID=$!

trap 'kill "$AWS_SIM_PID" "$GCP_SIM_PID" "$AZURE_SIM_PID" 2>/dev/null || true' EXIT
sleep 1
```

### AWS client -> GCS backend

This route is:

```text
aws CLI -> shimanism S3 frontend -> shimanism GCS backend -> sockerless GCP
```

Start shimanism in a second terminal:

```sh
cd shimanism

STORAGE_EMULATOR_HOST=localhost:14567 \
  bin/shim storage \
    -frontend=aws_s3 \
    -backend=gcs \
    -gcs-project=shim-sockerless \
    -addr=:19001
```

Drive it from the AWS CLI in a third terminal:

```sh
cd shimanism

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1

printf 'hello from AWS-shaped S3 into sockerless GCP\n' >/tmp/shimanism-sockerless.txt

aws --endpoint-url=http://localhost:19001 s3 mb s3://e2e-aws-gcp-demo
aws --endpoint-url=http://localhost:19001 s3 cp /tmp/shimanism-sockerless.txt s3://e2e-aws-gcp-demo/hello.txt
aws --endpoint-url=http://localhost:19001 s3 cp s3://e2e-aws-gcp-demo/hello.txt -
aws --endpoint-url=http://localhost:19001 s3 rm s3://e2e-aws-gcp-demo/hello.txt
aws --endpoint-url=http://localhost:19001 s3 rb s3://e2e-aws-gcp-demo
```

### GCP client -> Azure Blob backend

This route is:

```text
gcloud storage -> shimanism GCS frontend -> shimanism Azure Blob backend -> sockerless Azure
```

Start shimanism:

```sh
cd shimanism

AZURE_ACCOUNT_KEY='a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s='
AZURE_STORAGE_CONNECTION_STRING="DefaultEndpointsProtocol=http;AccountName=testacct;AccountKey=$AZURE_ACCOUNT_KEY;BlobEndpoint=http://localhost:14569/testacct/;"

bin/shim storage \
  -frontend=gcs \
  -backend=azureblob \
  -azure-connection-string="$AZURE_STORAGE_CONNECTION_STRING" \
  -azure-region=eastus \
  -addr=:19002
```

Drive it from `gcloud storage`:

```sh
cd shimanism

export CLOUDSDK_AUTH_DISABLE_CREDENTIALS=true
export CLOUDSDK_CORE_PROJECT=shim-sockerless
export CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=http://localhost:19002/

printf 'hello from GCS-shaped storage into sockerless Azure\n' >/tmp/shimanism-sockerless.txt

gcloud storage buckets create gs://e2e-gcp-azure-demo --location=US
gcloud storage cp /tmp/shimanism-sockerless.txt gs://e2e-gcp-azure-demo/hello.txt
gcloud storage cat gs://e2e-gcp-azure-demo/hello.txt
gcloud storage rm gs://e2e-gcp-azure-demo/hello.txt
gcloud storage rm --recursive gs://e2e-gcp-azure-demo
```

### Azure Blob client -> AWS S3 backend

This route is:

```text
az storage -> shimanism Azure Blob frontend -> shimanism AWS S3 backend -> sockerless AWS
```

Start shimanism:

```sh
cd shimanism

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1
export AWS_REQUEST_CHECKSUM_CALCULATION=when_required

bin/shim storage \
  -frontend=azure_blob \
  -backend=aws \
  -aws-endpoint=http://localhost:14566 \
  -addr=:19003
```

Drive it from the Azure CLI:

```sh
cd shimanism

AZURE_ACCOUNT_KEY='a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s='
printf 'hello from Azure-shaped Blob into sockerless AWS\n' >/tmp/shimanism-sockerless.txt

az storage container create \
  --auth-mode key \
  --account-name testacct \
  --account-key "$AZURE_ACCOUNT_KEY" \
  --blob-endpoint http://localhost:19003/testacct/ \
  --name e2e-azure-aws-demo

az storage blob upload \
  --auth-mode key \
  --account-name testacct \
  --account-key "$AZURE_ACCOUNT_KEY" \
  --blob-endpoint http://localhost:19003/testacct/ \
  --container-name e2e-azure-aws-demo \
  --name hello.txt \
  --file /tmp/shimanism-sockerless.txt \
  --overwrite

az storage blob download \
  --auth-mode key \
  --account-name testacct \
  --account-key "$AZURE_ACCOUNT_KEY" \
  --blob-endpoint http://localhost:19003/testacct/ \
  --container-name e2e-azure-aws-demo \
  --name hello.txt \
  --file /tmp/shimanism-sockerless-roundtrip.txt
cat /tmp/shimanism-sockerless-roundtrip.txt
cmp /tmp/shimanism-sockerless.txt /tmp/shimanism-sockerless-roundtrip.txt

az storage blob delete \
  --auth-mode key \
  --account-name testacct \
  --account-key "$AZURE_ACCOUNT_KEY" \
  --blob-endpoint http://localhost:19003/testacct/ \
  --container-name e2e-azure-aws-demo \
  --name hello.txt

az storage container delete \
  --auth-mode key \
  --account-name testacct \
  --account-key "$AZURE_ACCOUNT_KEY" \
  --blob-endpoint http://localhost:19003/testacct/ \
  --name e2e-azure-aws-demo
```

`AWS_REQUEST_CHECKSUM_CALCULATION=when_required` is set on the shimanism process for this local HTTP-only AWS destination. Real AWS S3 uses HTTPS; the setting is only needed for this standalone simulator route.

The repository also has `make sockerless`, which builds these simulators and runs the maintained `TestSockerless_*` validation lane. Use that for contributor verification; use the commands above when you want to run shimanism manually.

Issue [#24](https://github.com/e6qu/shimanism/issues/24) tracks extending this same through-shim sockerless shape across the remaining service families.

LocalStack is another local-cloud option people can try, especially for AWS-shaped development workflows. The shimanism project does not currently test the LocalStack-backed path, so the maintained local simulator lane is the sockerless one above.

## CLI, SDK, and Terraform

For each source cloud, the rule is the same: configure that cloud's official client to use the shim endpoint for the one service being migrated.

### AWS-shaped clients

CLI:

```sh
aws --endpoint-url=http://shim.internal:9001 s3 cp app.tar.gz s3://shim-demo-assets/
```

Go SDK:

```go
cfg, err := config.LoadDefaultConfig(ctx,
    config.WithRegion("us-east-1"),
)
if err != nil {
    return err
}
s3c := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("http://shim.internal:9001")
    o.UsePathStyle = true
})
_, err = s3c.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("shim-demo-assets"),
    Key:    aws.String("app.tar.gz"),
    Body:   body,
})
```

Terraform:

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = var.aws_access_key_id
  secret_key                  = var.aws_secret_access_key
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    s3 = "http://shim.internal:9001"
  }
}

resource "aws_s3_bucket" "assets" {
  bucket = "shim-demo-assets"
  tags   = {}
}
```

### GCP-shaped clients

CLI:

```sh
CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=http://shim.internal:9002/ \
  gcloud storage cp app.tar.gz gs://shim-demo-assets/
```

Go SDK:

```go
client, err := storage.NewClient(ctx, option.WithEndpoint("http://shim.internal:9002/"))
if err != nil {
    return err
}
wc := client.Bucket("shim-demo-assets").Object("app.tar.gz").NewWriter(ctx)
_, err = io.Copy(wc, body)
if closeErr := wc.Close(); err == nil {
    err = closeErr
}
```

Terraform:

```hcl
provider "google" {
  project                 = var.project_id
  region                  = "us-central1"
  storage_custom_endpoint = "http://shim.internal:9002/storage/v1/"
}

resource "google_storage_bucket" "assets" {
  name     = "shim-demo-assets"
  location = "US"
}
```

### Azure-shaped clients

Azure Blob SDK and CLI can target the shim's Blob endpoint. The `hashicorp/azurerm` provider does not currently expose a data-plane-only Blob endpoint override; its storage resources derive the Blob URL from the ARM storage account response. The Azure Terraform data-plane cells remain documented skips until either the provider exposes a direct override or shimanism fronts the required ARM account surface.

CLI:

```sh
az storage blob upload \
  --account-name shimstorage \
  --container-name shim-demo-assets \
  --name app.tar.gz \
  --file app.tar.gz \
  --blob-endpoint http://shim.internal:9003/shimstorage/
```

Go SDK:

```go
cred, err := azblob.NewSharedKeyCredential("shimstorage", base64AccountKey)
if err != nil {
    return err
}
client, err := azblob.NewClientWithSharedKeyCredential(
    "http://shim.internal:9003/shimstorage/",
    cred,
    nil,
)
if err != nil {
    return err
}
_, err = client.UploadStream(ctx, "shim-demo-assets", "app.tar.gz", body, nil)
```

## Terraform import state

After import, Terraform state stays source-provider-shaped. That is the point: your module still sees the provider it already used, while the shim sends reads and writes to the destination backend.

AWS-shaped import of a bucket that lives in GCS:

```sh
terraform init
terraform import aws_s3_bucket.assets shim-demo-assets
terraform state show aws_s3_bucket.assets
```

Representative state:

```hcl
# aws_s3_bucket.assets:
resource "aws_s3_bucket" "assets" {
    arn           = "arn:aws:s3:::shim-demo-assets"
    bucket        = "shim-demo-assets"
    force_destroy = false
    id            = "shim-demo-assets"
    tags          = {}
}
```

GCP-shaped import of a bucket that lives behind another storage backend:

```sh
terraform init
terraform import google_storage_bucket.assets shim-demo-assets
terraform state show google_storage_bucket.assets
```

Representative state:

```hcl
# google_storage_bucket.assets:
resource "google_storage_bucket" "assets" {
    id       = "shim-demo-assets"
    location = "US"
    name     = "shim-demo-assets"
    project  = "my-source-project"
    url      = "gs://shim-demo-assets"
}
```

The state does not expose the destination implementation detail. The backend is still the source of record for the resource data; Terraform records the source-provider shape it imported through.

## Cross-cloud routes by service

The examples below use three common migration directions. Replace the credential flags with the destination cloud credentials for your environment.

| Service | AWS -> GCP | GCP -> Azure | Azure -> AWS |
|---|---|---|---|
| Storage | `shim storage -frontend=aws_s3 -backend=gcs` | `shim storage -frontend=gcs -backend=azureblob` | `shim storage -frontend=azure_blob -backend=aws` |
| Secrets | `shim secrets -frontend=aws_secretsmanager -backend=gcp` | `shim secrets -frontend=gcp_secretmanager -backend=azure` | `shim secrets -frontend=azure_keyvault -backend=aws` |
| Queue | `shim queue -frontend=aws_sqs -backend=gcp` | `shim queue -frontend=gcp_pubsub -backend=azure` | `shim queue -frontend=azure_servicebus -backend=aws` |
| Pub/sub | `shim pubsub -frontend=aws_sns -backend=gcp` | `shim pubsub -frontend=gcp_pubsub -backend=azure` | `shim pubsub -frontend=azure_servicebus_topics -backend=aws` |
| RDBMS control plane | `shim rdbms -frontend=aws_rds -backend=gcp` | `shim rdbms -frontend=gcp_cloudsql -backend=azure` | `shim rdbms -frontend=azure_dbadmin -backend=aws` |
| Cache control plane | `shim cache -frontend=aws_elasticache -backend=gcp` | `shim cache -frontend=gcp_memorystore -backend=azure` | `shim cache -frontend=azure_redis -backend=aws` |
| Functions control plane | `shim functions -frontend=aws_lambda -backend=gcp` | `shim functions -frontend=gcp_cloudrun -backend=azure` | `shim functions -frontend=azure_containerapps -backend=aws` |
| API gateway | `shim apigateway -frontend=aws_apigatewayv2 -backend=gcp` | `shim apigateway -frontend=gcp_apigateway -backend=azure` | `shim apigateway -frontend=azure_apim -backend=aws` |

Read the per-service `INTERSECTION.md` and `APPLY_INTERSECTION.md` files before adopting a route. If a source-cloud feature is not in the cross-cloud intersection, shimanism must return the source cloud's own unsupported-operation error instead of pretending it worked.

## Validation checklist

1. Start the destination backend with real credentials, or start the sockerless simulator processes from the standalone local examples above.
2. Start one shim per service being moved, with the source frontend and destination backend selected explicitly.
3. Point exactly one source-cloud client surface at the shim endpoint: CLI, SDK, or Terraform provider.
4. Create, read, update if supported, and delete a small resource.
5. For Terraform, run `terraform plan -detailed-exitcode` after `apply` or `import`; exit code `0` is the no-drift result.
6. Confirm the destination cloud owns the resource, then remove the endpoint override to roll back.

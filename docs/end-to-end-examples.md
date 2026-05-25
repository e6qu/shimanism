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

For local testing, you can also run shimanism against [sockerless](https://github.com/e6qu/sockerless). Sockerless is a separate project that provides local AWS-, GCP-, and Azure-shaped simulator processes. Those simulators stand in for cloud control planes so the shimanism tests can exercise real SDK/CLI/Terraform client paths and real shimanism translation code without requiring cloud accounts or incurring cloud cost. It is a test target, not an application dependency.

```sh
git clone --depth=1 https://github.com/e6qu/sockerless.git /tmp/sockerless
make sockerless
```

The lane builds the AWS, GCP, and Azure sockerless simulator binaries, starts them on local ports, and runs every `TestSockerless_*` test. The storage package now includes through-shim cross-cloud cells:

| Test | Route |
|---|---|
| `TestSockerless_E2E_AWSFrontendToGCSBackend` | AWS S3 SDK -> S3 frontend -> GCS backend -> sockerless GCP |
| `TestSockerless_E2E_GCSFrontendToAzureBlobBackend` | GCS SDK -> GCS frontend -> Azure Blob backend -> sockerless Azure |
| `TestSockerless_E2E_AzureBlobFrontendToAWSBackend` | Azure Blob SDK -> Azure Blob frontend -> AWS S3 backend -> sockerless AWS |

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

1. Start the destination backend with real credentials, or run `make sockerless` for the local simulator lane.
2. Start one shim per service being moved, with the source frontend and destination backend selected explicitly.
3. Point exactly one source-cloud client surface at the shim endpoint: CLI, SDK, or Terraform provider.
4. Create, read, update if supported, and delete a small resource.
5. For Terraform, run `terraform plan -detailed-exitcode` after `apply` or `import`; exit code `0` is the no-drift result.
6. Confirm the destination cloud owns the resource, then remove the endpoint override to roll back.

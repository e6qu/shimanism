# shimanism

**Migrate from one cloud to another, one service at a time — without rewriting your code.**

shimanism is a translation proxy that speaks the cloud's own published API on the front and forwards each call to a real, comparable service somewhere else — another cloud, a Kubernetes operator, or a self-hosted server.

The point is **gradual, service-by-service migration**: keep your existing AWS code talking to "AWS"; transparently redirect, say, storage to GCS while everything else still uses real AWS; verify it works; move the next service; repeat. The shim is a stable on-ramp between clouds — no big-bang rewrite, no SDK swap, no Terraform-provider port.

Your code keeps using `boto3`, `aws s3`, `gcloud storage`, `hashicorp/aws`, the Azure SDK, or whatever else. You change one thing: the endpoint URL. The bytes land wherever the shim is pointed.

Nothing is reimplemented. Nothing is emulated. The data lives in a real backend.

## See it in action

The same MinIO server, accessed three different ways via three different shim frontends:

**AWS CLI:**

```sh
aws --endpoint-url=http://localhost:9001 s3 mb s3://my-bucket
aws --endpoint-url=http://localhost:9001 s3 cp app.tar.gz s3://my-bucket/
aws --endpoint-url=http://localhost:9001 s3 ls s3://my-bucket/
```

**gcloud CLI:**

```sh
gcloud storage buckets create gs://my-bucket \
  --api-endpoint-overrides=storage=http://localhost:9002
gcloud storage cp app.tar.gz gs://my-bucket/ \
  --api-endpoint-overrides=storage=http://localhost:9002
```

**Go SDK (AWS S3):**

```go
client := s3.NewFromConfig(aws.Config{
    Region:       "us-east-1",
    BaseEndpoint: aws.String("http://localhost:9001"),
})
_, err := client.PutObject(ctx, &s3.PutObjectInput{
    Bucket: aws.String("my-bucket"),
    Key:    aws.String("app.tar.gz"),
    Body:   strings.NewReader("..."),
})
```

**Terraform (`hashicorp/aws`):**

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true

  endpoints {
    s3 = "http://localhost:9001"
  }
}

resource "aws_s3_bucket" "app" {
  bucket = "my-bucket"
}
```

All four examples produce a real bucket holding real bytes — in MinIO (or GCS, Azure Blob, real S3, etc., whichever backend the shim was started with). The clients don't know they're not talking to real S3 / real GCS.

See [docs/getting-started.md](docs/getting-started.md) for the five-minute walkthrough.

## Shimmed services

| Service | AWS | GCP | Azure | Kubernetes | Per-service docs |
|---|---|---|---|---|---|
| Object storage | S3 | Cloud Storage | Blob Storage | MinIO | [docs/services/storage.md](docs/services/storage.md) |
| Secrets | Secrets Manager | Secret Manager | Key Vault | Vault | [docs/services/secrets.md](docs/services/secrets.md) |
| Queue | SQS | Pub/Sub (pull) | Service Bus queue | NATS JetStream | [docs/services/queue.md](docs/services/queue.md) |
| Pub/sub | SNS + SQS | Pub/Sub | Service Bus topics | NATS | [docs/services/pubsub.md](docs/services/pubsub.md) |
| Managed Postgres / MySQL *(control plane)* | RDS | Cloud SQL | Azure DB Flexible Server | CloudNativePG / MySQL Operator | [docs/services/rdbms.md](docs/services/rdbms.md) |
| Managed Redis *(control plane)* | ElastiCache | Memorystore | Azure Cache for Redis | Redis Operator | [docs/services/cache.md](docs/services/cache.md) |
| Functions *(container image, control plane)* | Lambda | Cloud Run | Container Apps | Knative | [docs/services/functions.md](docs/services/functions.md) |
| API gateway | API Gateway HTTP API v2 | API Gateway | API Management | Envoy Gateway | [docs/services/apigateway.md](docs/services/apigateway.md) |

Managed databases and managed Redis: only the **control plane** is shimmed (provisioning, scaling, snapshots, access bindings). The data plane is wire-protocol Postgres / MySQL / RESP — connect directly to the returned host.

## Why this works

Every shim is two layers:

- **Frontend:** speaks the cloud's published API exactly (SigV4 + S3 XML; OAuth2 + GCP JSON; SharedKey + Azure REST; etc). Server stubs are generated from the upstream spec.
- **Backend:** calls the real destination. AWS frontend over a GCS backend = AWS S3 wire on the front, real `storage.googleapis.com` on the back.

In between sits a neutral **domain** interface — the cross-cloud intersection of what every backend actually supports. Where a feature has no peer (S3 Object Lambda, Azure private endpoints, GCP-only retention), the shim returns the source cloud's own "not supported" error in its own vocabulary. **Never a fabricated success. Never a silent fallback.** See [PHILOSOPHY.md](PHILOSOPHY.md) for the *why*.

## Goals

- Shim cloud-locked managed services that publish strong, machine-readable APIs (OpenAPI, protobuf, ARM schema, Smithy).
- Translate honestly between AWS, GCP, Azure, and a selected Kubernetes-native peer per service.
- Stay transparent to existing SDKs, CLIs, and Terraform providers via their built-in endpoint-override.
- Cover the **intersection** of functionality across all backends. Where a feature has no peer, fail loud in the source cloud's own error vocabulary.
- Build the Kubernetes peer ourselves if no suitable open-source equivalent exists.

## How shimanism differs from other projects

shimanism's niche: it lives at the **wire-protocol layer**, speaks the source cloud's exact API, and runs against **real backends** — not emulators, not its own API, not a single-cloud compatibility shim. The result is that the same `aws`, `gcloud`, `boto3`, Terraform module, or Pulumi provider works without modification while data moves between clouds.

The closest comparison points and how shimanism differs from each:

| Project | What it is | How shimanism differs |
|---|---|---|
| **[LocalStack](https://localstack.cloud/)** | In-memory emulator of AWS services for local dev/test. | shimanism is not an emulator. Data lives in real backends. Production-grade migration is the use case; LocalStack is dev/test. |
| **[MinIO](https://min.io/)**, Cloudflare R2, Backblaze B2 | S3-compatible *backends*. They implement the S3 wire on top of their own storage. | shimanism is a *frontend translation layer*, not a backend. It can use MinIO as a backend; the value-add is the cross-cloud frontend matrix (`gcloud` → GCS frontend → MinIO; `aws` → S3 frontend → MinIO; etc.) and the other shimmed services beyond storage. |
| **[Crossplane](https://www.crossplane.io/)** | Multi-cloud infrastructure-as-code via Kubernetes CRDs. Users write `XR` resources. | Crossplane is at the IaC abstraction layer (its own CRDs). shimanism is at the wire-protocol layer: your existing `hashicorp/aws` Terraform module, your existing `aws-sdk` code, your existing `boto3` script all keep working unchanged. No new abstraction to learn. |
| **[Dapr](https://dapr.io/)** | Distributed-app runtime with multi-cloud bindings for state, pub/sub, secrets, etc. Apps call the Dapr SDK/sidecar. | Dapr requires you to rewrite to its API. shimanism lets you keep the cloud's API. Different target: Dapr is for greenfield apps that want portability; shimanism is for brownfield apps that need migration. |
| **[gocloud.dev](https://gocloud.dev/)** (Go Cloud Development Kit) | Go library with cloud-agnostic abstractions (blob, pubsub, etc). | Library-level cloud-agnostic abstraction. Apps import `gocloud.dev/blob` instead of `aws-sdk-go`. shimanism keeps the cloud SDK; no library swap. |
| **[Pulumi](https://www.pulumi.com/)** / **[Terraform](https://www.terraform.io/)** / **AWS CDK** | Infrastructure-as-code with multi-cloud provider plugins. | IaC tools, not data-plane proxies. They provision resources; they don't proxy runtime API calls. shimanism works *with* them — your Terraform plan still uses `hashicorp/aws`, but the bytes land on GCS through the shim. |
| **OpenStack Swift S3 middleware**, **Ceph RGW** | S3-compatible APIs on top of OpenStack / Ceph storage. | Single-cloud-API, single-backend compat shims. shimanism is many-frontend × many-backend, with the per-service intersection contracts enforced. |

The mental model: shimanism is to **gradual cloud migration** what cross-compilation is to portable binaries. The application doesn't know the target changed; the platform layer below it absorbed the difference, honestly.

For deeper reading: [PHILOSOPHY.md](PHILOSOPHY.md) on the "intersection-only, never lie" stance; [docs/architecture.md](docs/architecture.md) on the frontend/domain/backend layering; [doc/CROSS_CLOUD_ROUTING.md](doc/CROSS_CLOUD_ROUTING.md) on the migration story end-to-end.

## Non-goals

- **Not an emulator.** shimanism does not reimplement services in-memory or on local disk. For developer-local emulation, use LocalStack.
- **Not a neutral SDK.** There is no shimanism client library. Application code keeps importing `boto3`, `@azure/storage-blob`, `google-cloud-pubsub`, and so on.
- **Not a lowest-common-denominator abstraction layer.** We honor the source cloud's API. Where a call cannot be translated honestly, it fails in the source cloud's own error vocabulary rather than being smoothed over.
- **Not a Terraform wrapper.** Control-plane operations call the cloud admin APIs directly.
- **Not for services where redirection is already trivial.** Postgres, MySQL, and Redis speak the same wire protocol everywhere; their data planes are not shimmed.
- **Not for services with fundamentally incompatible models across clouds**, such as IAM and identity.
- **Not for layers already standardized by the industry**: Kubernetes, OCI distribution, OpenTelemetry.

## Documentation

The repo root keeps a small set of load-bearing files. Everything else lives under [`docs/`](docs/).

- **[docs/](docs/README.md)** — the documentation index. Start here for setup, architecture, contributing, testing, codegen, releasing, and per-service detail.
- **[PHILOSOPHY.md](PHILOSOPHY.md)** — the *why* every contributor should read before changing code.
- **[AGENTS.md](AGENTS.md)** — rules for human and LLM contributors. The continuity contract, the no-fakes rule, the bug-first rule, branch + PR hygiene.
- **[PLAN.md](PLAN.md)** — phase roadmap and exit criteria.
- **[STATUS.md](STATUS.md)** · **[DO_NEXT.md](DO_NEXT.md)** · **[WHAT_WE_DID.md](WHAT_WE_DID.md)** · **[BUGS.md](BUGS.md)** — the four continuity files that carry the project across sessions.

## License

AGPL-3.0.

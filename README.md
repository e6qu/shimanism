# shimanism

Cloud-agnostic shims for popular managed services, so the same application code can run against AWS, GCP, Azure, Kubernetes, or self-hosted backends — without changing.

## How it works

A shim is a stateless protocol-translation proxy. It speaks the cloud's own published API on the front, and forwards each call to a real, comparable service running elsewhere — another cloud, a Kubernetes operator, or an on-prem deployment. Application code keeps using the cloud SDK, CLI, or Terraform provider it was already using, redirected via the standard endpoint-override mechanism (`--endpoint-url`, `endpoint_url=...`, `endpoints { ... }`).

Nothing is reimplemented. Nothing is emulated. The data lives in a real backend.

## Goals

- Shim cloud-locked managed services that publish strong, machine-readable APIs (OpenAPI, protobuf, ARM schema).
- Translate between AWS, GCP, Azure, and a selected Kubernetes-native peer per service.
- Stay transparent to existing SDKs, CLIs, and Terraform providers via their built-in endpoint-override.
- Cover the **intersection** of functionality across all four backends — honestly. Where a feature has no peer, return the source cloud's own error in its own vocabulary; never a fabricated success.
- Build the Kubernetes peer ourselves if no suitable open-source equivalent exists.

## Non-goals

- **Not an emulator.** shimanism does not reimplement services in-memory or on local disk. For developer-local emulation, use LocalStack.
- **Not a neutral SDK.** There is no shimanism client library. Application code keeps importing `boto3`, `@azure/storage-blob`, `google-cloud-pubsub`, and so on.
- **Not a lowest-common-denominator abstraction layer.** We honor the source cloud's API. Where a call cannot be translated honestly, it fails in the source cloud's own error vocabulary rather than being smoothed over.
- **Not a Terraform wrapper.** Control-plane operations call the cloud admin APIs directly.
- **Not for services where redirection is already trivial.** Postgres, MySQL, and Redis speak the same wire protocol everywhere; their data planes are not shimmed.
- **Not for services with fundamentally incompatible models across clouds**, such as IAM and identity.
- **Not for layers already standardized by the industry**: Kubernetes, OCI distribution, OpenTelemetry.

## Planned MVP services

Eight services, each with AWS / GCP / Azure / Kubernetes backends:

| Service | AWS | GCP | Azure | Kubernetes |
|---|---|---|---|---|
| Object storage | S3 | Cloud Storage | Blob Storage | MinIO |
| Secrets | Secrets Manager | Secret Manager | Key Vault | Vault |
| Queue | SQS | Pub/Sub (pull) | Service Bus queue | NATS JetStream |
| Pub/sub | SNS + SQS | Pub/Sub | Service Bus topics | NATS |
| Managed Postgres / MySQL *(control plane)* | RDS | Cloud SQL | Azure DB Flexible Server | CloudNativePG / MySQL Operator |
| Managed Redis *(control plane)* | ElastiCache | Memorystore | Azure Cache for Redis | Redis Operator |
| Functions | Lambda | Cloud Run | Container Apps | Knative |
| API gateway | API Gateway HTTP API v2 | API Gateway | API Management (Consumption) | Envoy Gateway |

Managed databases and managed Redis: only the **control plane** is shimmed (provisioning, scaling, snapshots, and whatever access bindings the managed-service API requires). The data plane is wire-protocol Postgres / MySQL / RESP — connect directly to the real instance.

## Philosophy

See [PHILOSOPHY.md](./PHILOSOPHY.md).

## License

AGPL-3.0.

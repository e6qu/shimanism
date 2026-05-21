# shimanism vs. other projects

A longer-form comparison than the table in [README.md](../README.md#how-shimanism-differs-from-other-projects). Helps clarify when shimanism is the right tool vs when something else fits better.

## The mental model

Pick any cell from this 2×2:

|  | Source cloud's API | New / abstracted API |
|---|---|---|
| **Emulated data** | LocalStack | (rare) |
| **Real data** | **shimanism** | Crossplane, Dapr, gocloud.dev |

shimanism is the bottom-left: your application keeps using its source cloud's exact API; the bytes / messages / secrets land on real backends elsewhere. Everything else trades on at least one of those axes.

## Detailed comparisons

### LocalStack

[LocalStack](https://localstack.cloud/) is a local cloud emulator for development and testing. Free + Pro tiers. It implements cloud APIs from scratch (primarily AWS; Pro also covers Snowflake and a growing set); state can be in-process or persisted to disk via the Pro persistence module.

| Axis | LocalStack | shimanism |
|---|---|---|
| Source-cloud APIs | AWS-shape (Pro adds Snowflake + others) | AWS + GCP + Azure source APIs; any frontend composes with any backend (one shim instance = one frontend + one backend) |
| Data | Emulated (in-process or persisted to disk) | Real backend (real S3 / GCS / Azure Blob / MinIO / etc.) |
| Production use | No (dev/test only) | Yes (migration on-ramp; signature verification still pending — see [BUG-18](../BUGS.md)) |
| Fidelity | Partial; many cloud services have stubbed or absent behavior | Intersection-only; what's covered is honest |

**Use LocalStack when** you want to run AWS-shape code locally without an AWS account and you don't care if the data is real.

**Use shimanism when** you want the AWS-shape API to land on something other than AWS in *production* — another cloud, a Kubernetes operator, or a self-hosted backend — rerouting cloud services one at a time.

### MinIO / Cloudflare R2 / Backblaze B2 / Wasabi

These are **S3-compatible storage backends**. They implement the S3 wire on top of their own storage engine. From the client's perspective they look like S3; from the operator's perspective they're standalone storage products.

| Axis | MinIO / R2 / B2 | shimanism |
|---|---|---|
| Role | Backend (a place to put bytes) | Frontend translation layer |
| Services covered | Storage only | Storage + Secrets + Queue + Pub/Sub + RDBMS + Cache + Functions + API Gateway (and growing) |
| API surface | S3-compatible only | AWS + GCP + Azure source APIs |
| Composition | Used *as* a backend | Uses MinIO / R2 / etc. *as* the storage backend |

**Use MinIO directly when** you want an S3-compatible object store, full stop. No multi-cloud translation needed.

**Use shimanism when** you want `gcloud storage` and the Azure Blob SDK to *also* read/write the MinIO data, plus other services beyond storage. shimanism uses MinIO as its K8s-peer storage backend.

### Crossplane

[Crossplane](https://www.crossplane.io/) is a Kubernetes CR-based control plane for multi-cloud infrastructure. It ships managed-resource CRDs per provider (`Bucket`, `RDSInstance`, etc.) and lets platform teams compose them into higher-level `XR` (composite resource) abstractions via `XRD` + `Composition` definitions. All of it is reconciled by controllers against real cloud APIs.

The headline difference: **Crossplane requires you to stop using your cloud SDK / IaC provider and adopt its CRD model.** Your existing `aws-sdk-go` / `boto3` calls don't talk to Crossplane; your existing `hashicorp/aws` Terraform plans don't either. You re-express each resource as a Crossplane CR (`XRD` + `Composition`) and reconcile via `kubectl`. The application code that *uses* the resources (reads from S3, sends to SQS, etc.) is unchanged — but the infrastructure-as-code surface is fully replaced.

shimanism inverts that: **the original SDK and IaC keep working unchanged.** `aws s3 mb`, `boto3.client("s3").put_object(...)`, `hashicorp/aws`'s `aws_s3_bucket` resource — same calls, same plans, just pointed at a different endpoint. Crossplane is a new abstraction to learn; shimanism is a redirect.

| Axis | Crossplane | shimanism |
|---|---|---|
| Cloud SDK in your code | Replace usage — provisioning moves to Crossplane CRDs | **Keep the original SDK** (`aws-sdk-go`, `boto3`, `cloud.google.com/go/...`, Azure SDK) |
| IaC tooling | Replace with Crossplane CRs / Compositions | **Keep `hashicorp/aws`, `hashicorp/google`, `hashicorp/azurerm`** — same modules, same plans |
| Abstraction layer | New control-plane API (CRDs) | Wire-protocol passthrough (cloud's existing APIs) |
| Runtime | Kubernetes-required | Standalone Go binary (also runnable on K8s) |
| Data-plane proxying | No (control plane only) | Both control + data plane |

**Use Crossplane when** you want a Kubernetes-native unified control plane for your multi-cloud infra, and you're willing to express resources as CRDs.

**Use shimanism when** you want to keep your existing cloud SDK + IaC tooling and reroute cloud services one at a time without rewriting anything.

### Dapr

[Dapr](https://dapr.io/) is a distributed-app runtime with multi-cloud building-block bindings: state stores, pub/sub, secrets, bindings to messaging systems, etc. Apps call the Dapr sidecar via HTTP or its SDK; the sidecar fans out to the configured backend (AWS, GCP, Azure, on-prem).

| Axis | Dapr | shimanism |
|---|---|---|
| Application API | Dapr's own (HTTP + SDKs) | Source cloud's existing API |
| Rewrite required | Yes (apps call Dapr APIs) | No (apps unchanged) |
| Target audience | Greenfield apps wanting portability | Brownfield apps needing migration |
| Service scope | Generic primitives (state, pub/sub, secrets, etc) | Cloud-shaped services with their full APIs |

**Use Dapr when** you're building a new app that should be portable from day one and you're willing to adopt Dapr's API as the standard.

**Use shimanism when** you have existing AWS / GCP / Azure code and you want to move it without a rewrite.

### gocloud.dev (Go Cloud Development Kit)

[gocloud.dev](https://gocloud.dev/) is a Go library by the Go team with cloud-agnostic abstractions for blob storage, pub/sub, secrets, runtime variables, and SQL. Apps import `gocloud.dev/blob` instead of `cloud.google.com/go/storage` or `aws-sdk-go`.

| Axis | gocloud.dev | shimanism |
|---|---|---|
| Layer | Application SDK | Wire protocol |
| Languages | Go only | Any (it's a network proxy) |
| Migration story | Rewrite from cloud SDK → gocloud.dev SDK first | Keep cloud SDK; redirect endpoint |
| Coverage | Generic abstractions (blob, pubsub, etc) | Specific cloud APIs (S3, SQS, RDS, etc) |

**Use gocloud.dev when** you're writing a Go application from scratch and want a portable SDK.

**Use shimanism when** the application is already written (in any language) against a specific cloud's SDK, and you want to change what cloud it actually runs against without touching the code.

### Pulumi / Terraform / AWS CDK

These are infrastructure-as-code tools with multi-cloud provider plugins. They define and reconcile resources; they don't proxy runtime API calls.

| Axis | Pulumi / Terraform / CDK | shimanism |
|---|---|---|
| Layer | IaC (provisioning) | Runtime (data + control plane) |
| Cross-cloud story | Provider plugins (one per cloud) — code is provider-specific | Source cloud's API is the universal interface; the destination changes underneath |
| Composition with shimanism | Works *with* shimanism — your `hashicorp/aws` Terraform plans can be pointed at the shim's AWS frontend, and the resources land on GCS / Azure / etc. | — |

**Use Pulumi / Terraform / CDK when** you want to manage cloud infra. You'll continue using them with shimanism — your existing modules just point at the shim's endpoint.

**Use shimanism when** you want the runtime API calls (and the IaC plan/apply calls) to land on a different cloud.

### OpenStack Swift S3 middleware / Ceph RGW / Garage / NooBaa / s3proxy

S3-compatible APIs on top of various storage backends:

- **[Swift S3 middleware](https://docs.openstack.org/swift/latest/s3_compat.html)** — S3-compatible front for OpenStack Swift.
- **[Ceph RGW](https://docs.ceph.com/en/latest/radosgw/s3/)** — S3 + Swift-compatible front for Ceph object storage.
- **[Garage](https://garagehq.deuxfleurs.fr/)** — distributed S3-compatible object store for self-hosted / edge.
- **[NooBaa](https://www.noobaa.io/) (OpenShift Multicloud Object Gateway)** — federates many S3-compatible backends behind one S3 endpoint.
- **[s3proxy](https://github.com/gaul/s3proxy)** — S3-compatible front for Apache jclouds blob-store backends.

All single-frontend (S3-shape), single-service (object storage). shimanism overlaps in spirit but spans many services and many frontend shapes.

| Axis | S3-compat storage shims | shimanism |
|---|---|---|
| Source APIs | S3 only | AWS + GCP + Azure |
| Backends | Their respective storage layer | Many (real AWS / GCP / Azure / K8s peers / inmem) |
| Scope | Storage only | Many service families |
| Maturity | Stable, narrow | Earlier, broader |

**Use one of these when** you have a Swift / Ceph / Garage / NooBaa / jclouds deployment and need to expose S3-shape access to *its* storage.

**Use shimanism when** you need multi-frontend × multi-backend matrix coverage across multiple service families, not just object storage.

### Apache Libcloud / Apache jclouds

[Apache Libcloud](https://libcloud.apache.org/) (Python) and [Apache jclouds](https://jclouds.apache.org/) (Java) are neutral cloud-provider SDKs — single library API that talks to many clouds underneath.

| Axis | Libcloud / jclouds | shimanism |
|---|---|---|
| Layer | Application SDK (Python / Java) | Wire protocol |
| Code change required | Yes — apps import the neutral SDK | No — keep the cloud SDK |
| Languages | Python (Libcloud), Java/Scala/JVM (jclouds) | Any (wire-level proxy) |
| Target audience | Greenfield apps that want portability | Brownfield apps that need migration |

Same trade-off as [gocloud.dev / Dapr](#dapr): you adopt their API up-front to gain portability. shimanism preserves the existing cloud SDK and intercepts at the wire.

### OpenTofu

[OpenTofu](https://opentofu.org/) is the open-source fork of Terraform under the Linux Foundation. Same IaC model, same providers, same plan/apply cycle — drop-in compatible with most Terraform configurations.

shimanism works the same way with OpenTofu as with Terraform: your `hashicorp/aws` provider (or its OpenTofu-compatible equivalent) plans against the shim's endpoint override, and the resources land wherever the shim's backend points. No special handling needed.

If you've moved off Terraform to OpenTofu, your `endpoints { ... }` blocks continue working unchanged with shimanism.

## When *not* to use shimanism

- You're building a new application that should be portable from day one → consider Dapr, gocloud.dev, or designing around vendor-agnostic protocols (Kubernetes, OCI, OTel).
- You only need local-dev emulation of AWS → LocalStack is purpose-built for that.
- You want IaC-level multi-cloud control → Crossplane or Pulumi.
- The service you want isn't in [README.md § shimmed services](../README.md#shimmed-services) — open an issue, or build the shim using the framework described in [docs/development.md § adding a new service](development.md#adding-a-new-service).

## When shimanism is the right tool

- You have existing code (in any language) against an AWS / GCP / Azure SDK, and you want to move *some* of it to a different cloud without a rewrite.
- You want to reroute cloud services one at a time on a stable on-ramp.
- You want to keep your Terraform modules, CLI scripts, and CI/CD pipelines unchanged while the underlying cloud shifts.
- You need honest cross-cloud behavior — explicit failures where features don't translate, never silent degradation or fake success.

## Cross-link

- [README.md](../README.md) — project overview + service catalog.
- [PHILOSOPHY.md](../PHILOSOPHY.md) — the *why* of intersection-only + never-lie.
- [docs/architecture.md](architecture.md) — how the layering enables the wire-protocol shim approach.
- [doc/CROSS_CLOUD_ROUTING.md](../doc/CROSS_CLOUD_ROUTING.md) — the migration story end-to-end.
- [docs/services.md](services.md) — per-service detail.

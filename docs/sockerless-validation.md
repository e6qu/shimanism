# Sockerless validation lane

> Phase 14.A landed (sockerless round-1 closures from sockerless PR #179) and Phase 14.B's current sockerless-backed shim lane is green after sockerless PR #219. Uses `github.com/e6qu/sockerless` simulators to exercise the shim's per-cloud backends without requiring real AWS / GCP / Azure accounts.

## Why sockerless

The shim's backend translation layers (`services/<svc>/backends/{aws,gcs,azureblob,…}`) make outbound calls to the real cloud SDKs. Phase 14.D real-cloud Track A closes BUG-8 + BUG-15 by exercising those backends end-to-end with real cloud credentials. Ahead of that — and for everything else — sockerless lets us catch translation defects in CI-friendly local runs.

Sockerless reimplements the public cloud HTTP wire protocols in-process. Pointing the shim's backend at a sockerless port = real SDK code path, real wire bytes, no real-cloud cost or credential plumbing.

The same property makes sockerless the right vehicle for two things Phase 14 cares about:

1. **Cross-cloud shim verification.** The shim's job is translate (say) an AWS-shaped call → a GCP backend. Verifying that end-to-end needs a target the destination cloud's SDK actually talks to. Sockerless gives us a deterministic in-process target for each destination cloud — no real-cloud cost, no flake, no per-PR billing.
2. **Terraform-provider round-trips.** The matrix Phase 12 established (`TestCrossCloudApply_Roundtrip_<svc>_<cell>`) drives a cloud's Terraform provider against the shim, which forwards to the destination backend. With sockerless backends, the loop closes deterministically: `terraform apply` → shim frontend → shim backend → sockerless simulator → response chain → `terraform plan -refresh-only -detailed-exitcode = 0`.

## What's wired today (Phase 14.A + 14.B)

| Backend | Coverage | Notes |
|---|---|---|
| AWS S3 (`services/storage/backends/aws`) | Full round-trip | sockerless#173 + #174 closed in PR #179. |
| GCS (`services/storage/backends/gcs`) | Full round-trip | Uses the SDK's `STORAGE_EMULATOR_HOST` env var. |
| AWS Secrets Manager (`services/secrets/backends/aws`) | Full round-trip | sockerless#175 closed in PR #179. |
| AWS SQS (`services/queue/backends/aws`) | CreateQueue with Attributes → HeadQueue assertion (VisibilityTimeout + MessageRetentionPeriod) | sockerless#186 closed in PR #180. |
| GCP Pub/Sub queue (`services/queue/backends/gcp`) | Retention round-trip — CreateQueue → SetQueueAttributes → HeadQueue | Clears the shim backend leg of BUG-15; the remaining BUG-15 question is hashicorp/google Terraform state drift. |
| GCP Pub/Sub pubsub (`services/pubsub/backends/gcp`) | Full round-trip — Topic + Subscription + Publish + Receive + Ack | sockerless#182 closed in PR #180 (subscription field preservation). |
| GCP API Gateway (`services/apigateway/backends/gcp`) | Full LRO-style CRUD — CreateGateway (with routes) → DescribeGateway → ListGateways → DeleteGateway | sockerless#177 + #181-188 closed; **SDK leg of BUG-8 cleared**. |
| Azure Blob (`services/storage/backends/azureblob`) | Full round-trip — CreateBucket → PutObject → HeadObject → GetObject → DeleteObject → DeleteBucket | Uses host-based dispatch plus localhost DialContext rewrite; path-style support was fixed upstream too. |
| Azure Key Vault (`services/secrets/backends/azurekv`) | Full secret round-trip — CreateSecret → GetSecretValue → DeleteSecret | KV challenge flow + version listing fixed upstream by PRs #202/#211. |
| GCP Secret Manager (`services/secrets/backends/gcp`) | Full lifecycle/versioning round-trip — CreateSecret → PutSecretValue → HeadSecret → GetSecretValue(latest + explicit version) → ListVersions → ListSecrets → UpdateSecret → DeleteSecret | [sockerless#218](https://github.com/e6qu/sockerless/issues/218) closed by PR #219; no shim workaround carried. |
| GCP Cloud SQL (`services/rdbms/backends/gcp`) | Through-shim AWS RDS frontend CRUD — CreateDBInstance → DescribeDBInstances → ModifyDBInstance → DeleteDBInstance | Uses the Cloud SQL Admin REST SDK pointed at sockerless GCP. |
| GCP Memorystore (`services/cache/backends/gcp`) | Through-shim AWS ElastiCache frontend CRUD — CreateCacheCluster → DescribeCacheClusters → ModifyCacheCluster → DeleteCacheCluster | Uses the Memorystore REST SDK pointed at sockerless GCP. |
| AWS Lambda (`services/functions/backends/aws`) | Through-shim AWS Lambda frontend CRUD — CreateFunction → GetFunction → UpdateFunctionConfiguration → ListFunctions → DeleteFunction | Functions use AWS → AWS because Lambda's required `Role` has no honest GCP/Azure analogue; non-AWS backends reject it loudly. |
| Azure Cache for Redis (`services/cache/backends/azure`) | ARM control-plane CRUD — CreateInstance → DescribeInstance → ListInstances → DeleteInstance | Uses `armredis/v3` against sockerless's `Microsoft.Cache/Redis` provider. Data plane (Redis RESP) is out of scope. |
| Azure PostgreSQL FlexibleServer (`services/rdbms/backends/azure`) | ARM control-plane CRUD — CreateInstance → DescribeInstance → ListInstances → DeleteInstance | Uses `armpostgresqlflexibleservers/v4` against sockerless's `Microsoft.DBforPostgreSQL/flexibleServers`. The PG wire protocol is out of scope. |
| Azure APIM (`services/apigateway/backends/azure`) | ARM control-plane CRUD — CreateGateway → DescribeGateway → ListGateways → DeleteGateway | Uses `armapimanagement/v3`. The test pre-creates the parent `Microsoft.ApiManagement/service` (which real users provision via Terraform/ARM template) before invoking the shim backend. |
| Azure Container Apps (`services/functions/backends/azure`) | ⏸ Lane added, default-skipped | The sim's Container Apps handler invokes the local container runtime to start a replica (matching real Azure's behavior, where the underlying execution is opaque to the caller). Requires a docker/podman daemon AND a pre-pulled image. Set `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` to a known-pullable image reference to opt in. |
| Azure Service Bus queue (`services/queue/backends/azure`) | Admin-only CRUD — CreateQueue → SetQueueAttributes (LockDuration + DefaultMessageTimeToLive) → HeadQueue → ListQueues → DeleteQueue | Uses `azservicebus/admin` against sockerless's namespace-level ATOM XML admin protocol ([sockerless PR #225](https://github.com/e6qu/sockerless/pull/225)). AMQP Send/Receive is out of scope until sockerless exposes raw AMQP-over-TCP transport ([sockerless#230](https://github.com/e6qu/sockerless/issues/230)): the existing AMQP-over-WebSocket from [sockerless PR #229](https://github.com/e6qu/sockerless/pull/229) would require WebSocket-dial code in the test driver, leaking sockerless's transport choice into the shim's test layer. The shim's test driver is the cloud SDK; transport beneath the SDK should be the SDK's business, not ours. |
| Azure Service Bus pubsub (`services/pubsub/backends/azure`) | Admin-only CRUD — CreateTopic → CreateSubscription → ListTopics → ListSubscriptions → DeleteSubscription → DeleteTopic | Same admin-only scope as queues; Publish / Receive blocked on the same upstream AMQP-over-TCP gap. |
| Azure Blob multipart (`services/storage/backends/azureblob`) | Multipart round-trip — CreateMultipartUpload → UploadPart × N → ListParts → CompleteMultipartUpload → GetObject (asserts concatenated body) | Exercises the shim's `StageBlock` / `CommitBlockList` / `GetBlockList` code path against sockerless's block-blob staging support ([sockerless PR #229](https://github.com/e6qu/sockerless/pull/229)). Closes the missing-multipart gap in the existing Azure Blob lane. |
| AWS SNS and API Gateway v2 source frontends | Through-shim AWS source SDK cells now cover pubsub and apigateway against sockerless GCP backends | See the table below. |
| GCP Cloud Run + Cloud Functions, Azure Container Apps + Functions Sites | Sims work; through-shim functions lane currently uses AWS destination for the source-shape reason above | No silent source-field dropping. |

## Through-shim cross-cloud E2E cells

The backend rows above prove that shimanism's destination-cloud backend adapters can drive sockerless. The through-shim cells prove the full route users care about:

```text
source-cloud SDK -> shimanism frontend -> shimanism backend -> sockerless destination-cloud simulator
```

Storage has one green cell per requested migration direction:

| Test | Route |
|---|---|
| `TestSockerless_E2E_AWSFrontendToGCSBackend` | AWS S3 SDK -> S3 frontend -> GCS backend -> sockerless GCP |
| `TestSockerless_E2E_GCSFrontendToAzureBlobBackend` | GCS SDK -> GCS frontend -> Azure Blob backend -> sockerless Azure |
| `TestSockerless_E2E_AzureBlobFrontendToAWSBackend` | Azure Blob SDK -> Azure Blob frontend -> AWS S3 backend -> sockerless AWS |

The same through-shim route is now covered for every service family:

| Test | Route |
|---|---|
| `TestSockerless_AWSSecretsFrontendToGCPBackend_RoundTrip` | AWS Secrets Manager SDK -> Secrets Manager frontend -> GCP Secret Manager backend -> sockerless GCP |
| `TestSockerless_AWSSQSFrontendToGCPBackend_MessageRoundTrip` | AWS SQS SDK -> SQS frontend -> GCP Pub/Sub queue backend -> sockerless GCP |
| `TestSockerless_AWSSNSFrontendToGCPBackend_Fanout` | AWS SNS/SQS SDKs -> SNS/SQS frontends -> GCP Pub/Sub backend -> sockerless GCP |
| `TestSockerless_AWSRDSFrontendToGCPBackend_CRUD` | AWS RDS SDK -> RDS frontend -> GCP Cloud SQL backend -> sockerless GCP |
| `TestSockerless_AWSElastiCacheFrontendToGCPBackend_CRUD` | AWS ElastiCache SDK -> ElastiCache frontend -> GCP Memorystore backend -> sockerless GCP |
| `TestSockerless_AWSLambdaFrontendToAWSBackend_CRUD` | AWS Lambda SDK -> Lambda frontend -> AWS Lambda backend -> sockerless AWS |
| `TestSockerless_AWSAPIGatewayFrontendToGCPBackend_CRUD` | AWS API Gateway v2 SDK -> API Gateway frontend -> GCP API Gateway backend -> sockerless GCP |

## Running the lane locally

Requires a local clone of sockerless:

```sh
git clone --depth=1 https://github.com/e6qu/sockerless.git /tmp/sockerless
make sockerless             # builds sims, generates cert, runs TestSockerless_* tests
```

Set `SOCKERLESS_DIR` to override the default `/tmp/sockerless` location.

The script:

1. Builds the AWS + GCP + Azure simulator binaries with `-tags noui` (no UI dist required).
2. Generates a self-signed RSA-2048 cert in `/tmp/sockerless-tls/`. The aws-sdk-go-v2 SDK refuses to send streaming-signed payloads over plain HTTP, so the AWS sim runs under TLS.
3. Starts the sims on test-only ports (`:14566` AWS, `:14567` GCP, `:14569` Azure).
4. Runs `go test -run '^TestSockerless_'` in the storage, secrets, queue, pubsub, rdbms, cache, functions, and apigateway conformance packages with the right env vars to point the shim's backends at the sims.
5. Tears the sims down on exit.

## Sockerless issues filed upstream

### Initial fidelity gaps (round-1, all closed)

| Issue | Summary |
|---|---|
| [#173](https://github.com/e6qu/sockerless/issues/173) | ✅ closed — AWS S3 routes mounted under `/s3/` URL prefix instead of `/`. |
| [#174](https://github.com/e6qu/sockerless/issues/174) | ✅ closed — AWS S3 simulator stored the SDK's `aws-chunked` request-body envelope verbatim. |
| [#175](https://github.com/e6qu/sockerless/issues/175) | ✅ closed — AWS Secrets Manager simulator was missing `ListSecretVersionIds`. |

### Missing-service asks (round-1, all closed in sockerless PR #179)

| Issue | Cloud | Status |
|---|---|---|
| [#176](https://github.com/e6qu/sockerless/issues/176) | AWS — SQS / SNS / APIGW v1+v2 / RDS / ElastiCache | ✅ closed |
| [#177](https://github.com/e6qu/sockerless/issues/177) | GCP — Pub/Sub / Secret Manager / Cloud SQL / Memorystore / API Gateway | ✅ closed |
| [#178](https://github.com/e6qu/sockerless/issues/178) | Azure — Blob+KV data plane / Service Bus / PG / Redis / APIM | ✅ closed |

### Later fidelity bugs (all closed as of sockerless PR #219)

| Issue | Summary |
|---|---|
| [#181-188](https://github.com/e6qu/sockerless/issues/181) | ✅ closed by PR #180 — round-2 fidelity drift across Azure Redis, GCP Pub/Sub / Secret Manager / Cloud SQL, Azure KV, and AWS SQS. |
| [#189-191](https://github.com/e6qu/sockerless/issues/189) | ✅ closed by PR #192 plus follow-up for #190 — Pub/Sub PATCH, Azure Blob path-style dispatch, Azure KV secret URL scheme. |
| [#193-199](https://github.com/e6qu/sockerless/issues/193) | ✅ closed by PRs #200/#202 — KV challenge flow, AWS RDS/ElastiCache defaults, Azure Service Bus REST, S3/GCS/Lambda gaps. |
| [#201](https://github.com/e6qu/sockerless/issues/201) | ✅ closed by PR #202 — S3 bucket-level PUT subresources. |
| [#203-210](https://github.com/e6qu/sockerless/issues/203) | ✅ closed by PR #211 plus PR #216 follow-ups after reopens — KV versions, APIGW routing, Azure Functions config, AWS/GCP/Azure deeper Terraform-provider surfaces. |
| [#213-215](https://github.com/e6qu/sockerless/issues/213) | ✅ closed by PR #216 — Azure Tags API, Service Bus authorizationRules, AWS IAM/API Gateway v1 gaps. |
| [#218](https://github.com/e6qu/sockerless/issues/218) | ✅ closed by PR #219 — GCP Secret Manager ListSecretVersions, UpdateSecret, and DeleteSecret handlers. |

As of the `phase-183-sockerless-all-services` verification run, `make sockerless` passed all current shim lanes, including the full GCP Secret Manager lifecycle/versioning lane that had been blocked by [#218](https://github.com/e6qu/sockerless/issues/218), the three storage through-shim cross-cloud E2E cells, and the new all-service-family through-shim cells tracked by [shimanism#24](https://github.com/e6qu/shimanism/issues/24).

## Extending to a new service

1. Confirm the relevant sockerless simulator implements the operations you need (`/tmp/sockerless/simulators/<provider>/`).
2. Add a `TestSockerless_<Service>_<Op>` test in `services/<svc>/conformance/sockerless_test.go` (same env-controlled skip pattern as `services/storage/conformance/sockerless_test.go`).
3. If the sim needs a different startup contract (TLS / non-TLS / extra env), extend `scripts/run-sockerless-storage.sh` (or split into a per-service script + a top-level `make sockerless` aggregator).
4. File any fidelity gaps as fully self-contained issues on `e6qu/sockerless` — repro should be runnable with only sockerless checked out, no references to this repo.

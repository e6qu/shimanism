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
| GCP Cloud SQL, Memorystore | Sims work; lanes not yet added | 14.B follow-on. |
| Azure Service Bus, PG FlexibleServer, Cache Redis, APIM | Sims work; lanes not yet added | 14.B follow-on. |
| AWS SNS, RDS, ElastiCache, API Gateway v1+v2 | Sims work; lanes not yet added | 14.B follow-on. |
| AWS Lambda, GCP Cloud Run + Cloud Functions, Azure Container Apps + Functions Sites | Sims work; lanes not yet added | 14.B follow-on. |

## Running the lane locally

Requires a local clone of sockerless:

```sh
git clone --depth=1 https://github.com/e6qu/sockerless.git /tmp/sockerless
make sockerless-storage     # builds sims, generates cert, runs TestSockerless_* tests
```

Set `SOCKERLESS_DIR` to override the default `/tmp/sockerless` location.

The script:

1. Builds the AWS + GCP + Azure simulator binaries with `-tags noui` (no UI dist required).
2. Generates a self-signed RSA-2048 cert in `/tmp/sockerless-tls/`. The aws-sdk-go-v2 SDK refuses to send streaming-signed payloads over plain HTTP, so the AWS sim runs under TLS.
3. Starts the sims on test-only ports (`:14566` AWS, `:14567` GCP, `:14568` Azure).
4. Runs `go test -run '^TestSockerless_'` in the storage, secrets, queue, pubsub, and apigateway conformance packages with the right env vars to point the shim's backends at the sims.
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

As of the PR #219 verification run, `make sockerless-storage` passed all current shim lanes, including the full GCP Secret Manager lifecycle/versioning lane that had been blocked by [#218](https://github.com/e6qu/sockerless/issues/218).

## Extending to a new service

1. Confirm the relevant sockerless simulator implements the operations you need (`/tmp/sockerless/simulators/<provider>/`).
2. Add a `TestSockerless_<Service>_<Op>` test in `services/<svc>/conformance/sockerless_test.go` (same env-controlled skip pattern as `services/storage/conformance/sockerless_test.go`).
3. If the sim needs a different startup contract (TLS / non-TLS / extra env), extend `scripts/run-sockerless-storage.sh` (or split into a per-service script + a top-level `make sockerless` aggregator).
4. File any fidelity gaps as fully self-contained issues on `e6qu/sockerless` — repro should be runnable with only sockerless checked out, no references to this repo.

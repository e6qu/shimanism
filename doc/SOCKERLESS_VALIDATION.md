# Sockerless validation lane

> Phase 14.A landed (sockerless round-1 closures from sockerless PR #179) — full AWS S3 + GCS + AWS Secrets Manager round-trip. Phase 14.B + 14.C still pending; gated on the 8 round-2 sockerless issues we filed in the 14.D audit (see [PLAN.md § Phase 14](../PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons)). Uses `github.com/e6qu/sockerless` simulators to exercise the shim's per-cloud backends without requiring real AWS / GCP / Azure accounts.

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
| GCP Pub/Sub queue (`services/queue/backends/gcp`) | CRUD only (Create + Head + Delete). | Retention round-trip (the BUG-15 closure shape) gated on [sockerless#189](https://github.com/e6qu/sockerless/issues/189) — Pub/Sub PATCH not yet wired. |
| GCP Pub/Sub pubsub (`services/pubsub/backends/gcp`) | Full round-trip — Topic + Subscription + Publish + Receive + Ack | sockerless#182 closed in PR #180 (subscription field preservation). |
| GCP API Gateway (`services/apigateway/backends/gcp`) | Full LRO-style CRUD — CreateGateway (with routes) → DescribeGateway → ListGateways → DeleteGateway | sockerless#177 + #181-188 closed; **SDK leg of BUG-8 cleared**. |
| Azure Blob (`services/storage/backends/azureblob`) | Not yet wired | Blob data plane exists in sockerless but only supports host-based dispatch; [sockerless#190](https://github.com/e6qu/sockerless/issues/190) tracks adding path-style (Azure SDK + azurerm provider default). |
| Azure Key Vault (`services/secrets/backends/azurekv`) | Not yet wired | KV data plane exists; [sockerless#191](https://github.com/e6qu/sockerless/issues/191) tracks the secret URL scheme regression. Lane will work under TLS-mode sim today; HTTP-mode breaks. |
| GCP Secret Manager, Cloud SQL, Memorystore | Sims work; lanes not yet added | 14.B follow-on. |
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

1. Builds the AWS + GCP simulator binaries with `-tags noui` (no UI dist required).
2. Generates a self-signed RSA-2048 cert in `/tmp/sockerless-tls/`. The aws-sdk-go-v2 SDK refuses to send streaming-signed payloads over plain HTTP, so the AWS sim runs under TLS.
3. Starts both sims on test-only ports (`:14566` AWS, `:14567` GCP).
4. Runs `go test -run '^TestSockerless_'` in `services/storage/conformance/` with the right env vars to point the shim's backends at the sims.
5. Tears the sims down on exit.

## Sockerless issues filed upstream

### Fidelity gaps (active bugs)

| Issue | Summary |
|---|---|
| [#173](https://github.com/e6qu/sockerless/issues/173) | AWS S3 routes mounted under `/s3/` URL prefix instead of `/`. Breaks SDK / CLI / Terraform-provider default config. Workaround: append `/s3` to the endpoint URL (`https://localhost:4566/s3`). |
| [#174](https://github.com/e6qu/sockerless/issues/174) | AWS S3 simulator stores the SDK's `aws-chunked` request-body envelope verbatim. Uploads via non-seekable bodies (the common case for any streaming upload — HTTP-forwarded, encrypted, compressed) don't round-trip. |
| [#175](https://github.com/e6qu/sockerless/issues/175) | AWS Secrets Manager simulator is missing `ListSecretVersionIds`. Any SDK or shim path that maps version index → UUID hits a 400 `UnknownOperationException`. |

Closing #174 unblocks PutObject / GetObject in the storage lane; closing #175 unblocks GetSecretValue + HeadSecret in the secrets lane.

### Missing-service asks (round-1, all closed in sockerless PR #179)

| Issue | Cloud | Status |
|---|---|---|
| [#176](https://github.com/e6qu/sockerless/issues/176) | AWS — SQS / SNS / APIGW v1+v2 / RDS / ElastiCache | ✅ closed |
| [#177](https://github.com/e6qu/sockerless/issues/177) | GCP — Pub/Sub / Secret Manager / Cloud SQL / Memorystore / API Gateway | ✅ closed |
| [#178](https://github.com/e6qu/sockerless/issues/178) | Azure — Blob+KV data plane / Service Bus / PG / Redis / APIM | ✅ closed |

### Round-3 fidelity bugs (still open, Phase 14.B per-lane audit)

| Issue | Summary |
|---|---|
| [#189](https://github.com/e6qu/sockerless/issues/189) | GCP Pub/Sub `projects.subscriptions.patch` returns 404 — blocks shim's `SetQueueAttributes` and TF-provider `google_pubsub_subscription` updates. **Blocks BUG-15 closure**. |
| [#190](https://github.com/e6qu/sockerless/issues/190) | Azure Blob data plane only supports host-based dispatch; Azure SDK + azurerm provider default to path-style URLs (Azurite-compatible) and 404 against the sim. |
| [#191](https://github.com/e6qu/sockerless/issues/191) | Azure KV secret `id` uses request scheme — partial #184 regression where the keys path hard-codes `https` but the secrets path doesn't. |

## Extending to a new service

1. Confirm the relevant sockerless simulator implements the operations you need (`/tmp/sockerless/simulators/<provider>/`).
2. Add a `TestSockerless_<Service>_<Op>` test in `services/<svc>/conformance/sockerless_test.go` (same env-controlled skip pattern as `services/storage/conformance/sockerless_test.go`).
3. If the sim needs a different startup contract (TLS / non-TLS / extra env), extend `scripts/run-sockerless-storage.sh` (or split into a per-service script + a top-level `make sockerless` aggregator).
4. File any fidelity gaps as fully self-contained issues on `e6qu/sockerless` — repro should be runnable with only sockerless checked out, no references to this repo.

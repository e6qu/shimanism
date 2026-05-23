# Sockerless validation lane

> Phase 14.A landed (sockerless round-1 closures from sockerless PR #179) — full AWS S3 + GCS + AWS Secrets Manager round-trip. Phase 14.B + 14.C still pending; gated on the 8 round-2 sockerless issues we filed in the 14.D audit (see [PLAN.md § Phase 14](../PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons)). Uses `github.com/e6qu/sockerless` simulators to exercise the shim's per-cloud backends without requiring real AWS / GCP / Azure accounts.

## Why sockerless

The shim's backend translation layers (`services/<svc>/backends/{aws,gcs,azureblob,…}`) make outbound calls to the real cloud SDKs. Phase 14.D real-cloud Track A closes BUG-8 + BUG-15 by exercising those backends end-to-end with real cloud credentials. Ahead of that — and for everything else — sockerless lets us catch translation defects in CI-friendly local runs.

Sockerless reimplements the public cloud HTTP wire protocols in-process. Pointing the shim's backend at a sockerless port = real SDK code path, real wire bytes, no real-cloud cost or credential plumbing.

The same property makes sockerless the right vehicle for two things Phase 14 cares about:

1. **Cross-cloud shim verification.** The shim's job is translate (say) an AWS-shaped call → a GCP backend. Verifying that end-to-end needs a target the destination cloud's SDK actually talks to. Sockerless gives us a deterministic in-process target for each destination cloud — no real-cloud cost, no flake, no per-PR billing.
2. **Terraform-provider round-trips.** The matrix Phase 12 established (`TestCrossCloudApply_Roundtrip_<svc>_<cell>`) drives a cloud's Terraform provider against the shim, which forwards to the destination backend. With sockerless backends, the loop closes deterministically: `terraform apply` → shim frontend → shim backend → sockerless simulator → response chain → `terraform plan -refresh-only -detailed-exitcode = 0`.

## What's wired today (Phase 14.A)

| Backend | Coverage | Notes |
|---|---|---|
| AWS S3 (`services/storage/backends/aws`) | **Full round-trip** — CreateBucket → PutObject → HeadObject → GetObject → DeleteObject → DeleteBucket | sockerless#173 + #174 closed. |
| GCS (`services/storage/backends/gcs`) | Full round-trip | Uses the SDK's `STORAGE_EMULATOR_HOST` env var. |
| AWS Secrets Manager (`services/secrets/backends/aws`) | **Full round-trip** — CreateSecret → HeadSecret → GetSecretValue → ListSecrets → DeleteSecret | sockerless#175 closed. |
| Azure Blob (`services/storage/backends/azureblob`) | Not yet wired | Blob data plane added in sockerless PR #179. Adding the lane is a 14.B follow-on. |
| GCP Pub/Sub, Secret Manager, Cloud SQL, Memorystore, API Gateway | Not yet wired | Sims added in sockerless PR #179. Per-service round-2 fidelity issues filed ([#182](https://github.com/e6qu/sockerless/issues/182), [#183](https://github.com/e6qu/sockerless/issues/183), [#187](https://github.com/e6qu/sockerless/issues/187), [#188](https://github.com/e6qu/sockerless/issues/188)) block clean lanes — adding the lanes is a 14.B follow-on as fixes land. |
| Azure Key Vault, Service Bus, PG FlexibleServer, Cache Redis, APIM | Not yet wired | Sims added in sockerless PR #179. Per-service round-2 fidelity issues filed ([#181](https://github.com/e6qu/sockerless/issues/181), [#184](https://github.com/e6qu/sockerless/issues/184), [#185](https://github.com/e6qu/sockerless/issues/185)) block clean lanes — adding the lanes is a 14.B follow-on. |
| AWS SQS, SNS, RDS, ElastiCache, API Gateway v1+v2 | Not yet wired | Sims added in sockerless PR #179. SQS-specific round-2 fidelity issue ([#186](https://github.com/e6qu/sockerless/issues/186)) blocks the SQS lane. |
| AWS Lambda, GCP Cloud Run + Cloud Functions, Azure Container Apps + Functions Sites | Not yet wired | Sims existed pre-PR #179. Adding the functions lane is a 14.B follow-on; no known fidelity bugs blocking. |

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

### Missing-service asks (per-cloud rollups)

| Issue | Cloud | Services |
|---|---|---|
| [#176](https://github.com/e6qu/sockerless/issues/176) | AWS | SQS, SNS, API Gateway v1 + v2, RDS / Aurora, ElastiCache |
| [#177](https://github.com/e6qu/sockerless/issues/177) | GCP | Pub/Sub, Secret Manager, Cloud SQL Admin, Memorystore, API Gateway |
| [#178](https://github.com/e6qu/sockerless/issues/178) | Azure | Blob data plane, Key Vault data plane, Service Bus (ARM + data), Database for PostgreSQL FlexibleServer, Cache for Redis, API Management |

Each rollup lists per-service yield-per-LOC ordering suggestions for sockerless maintainers. None of these block PR #20 — they're filed so consumers (and Track A's eventual real-cloud lane) have a documented upstream path.

## Extending to a new service

1. Confirm the relevant sockerless simulator implements the operations you need (`/tmp/sockerless/simulators/<provider>/`).
2. Add a `TestSockerless_<Service>_<Op>` test in `services/<svc>/conformance/sockerless_test.go` (same env-controlled skip pattern as `services/storage/conformance/sockerless_test.go`).
3. If the sim needs a different startup contract (TLS / non-TLS / extra env), extend `scripts/run-sockerless-storage.sh` (or split into a per-service script + a top-level `make sockerless` aggregator).
4. File any fidelity gaps as fully self-contained issues on `e6qu/sockerless` — repro should be runnable with only sockerless checked out, no references to this repo.

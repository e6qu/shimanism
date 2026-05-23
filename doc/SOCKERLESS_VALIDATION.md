# Sockerless validation lane

> Phase 13.D.1 first cut, with Phase 14 expansion gated on upstream sockerless fixes (see [PLAN.md § Phase 14](../PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons)). Uses `github.com/e6qu/sockerless` simulators to exercise the shim's per-cloud backends without requiring real AWS / GCP / Azure accounts.

## Why sockerless

The shim's backend translation layers (`services/<svc>/backends/{aws,gcs,azureblob,…}`) make outbound calls to the real cloud SDKs. Phase 14.D real-cloud Track A closes BUG-8 + BUG-15 by exercising those backends end-to-end with real cloud credentials. Ahead of that — and for everything else — sockerless lets us catch translation defects in CI-friendly local runs.

Sockerless reimplements the public cloud HTTP wire protocols in-process. Pointing the shim's backend at a sockerless port = real SDK code path, real wire bytes, no real-cloud cost or credential plumbing.

The same property makes sockerless the right vehicle for two things Phase 14 cares about:

1. **Cross-cloud shim verification.** The shim's job is translate (say) an AWS-shaped call → a GCP backend. Verifying that end-to-end needs a target the destination cloud's SDK actually talks to. Sockerless gives us a deterministic in-process target for each destination cloud — no real-cloud cost, no flake, no per-PR billing.
2. **Terraform-provider round-trips.** The matrix Phase 12 established (`TestCrossCloudApply_Roundtrip_<svc>_<cell>`) drives a cloud's Terraform provider against the shim, which forwards to the destination backend. With sockerless backends, the loop closes deterministically: `terraform apply` → shim frontend → shim backend → sockerless simulator → response chain → `terraform plan -refresh-only -detailed-exitcode = 0`.

## What's wired today

| Backend | Coverage | Notes |
|---|---|---|
| AWS S3 (`services/storage/backends/aws`) | Bucket lifecycle | PutObject / GetObject round-trip blocked on [sockerless#174](https://github.com/e6qu/sockerless/issues/174) (sim persists the SDK's `aws-chunked` envelope verbatim). |
| GCS (`services/storage/backends/gcs`) | Full round-trip — CreateBucket → PutObject → GetObject → DeleteObject → DeleteBucket | Uses the SDK's `STORAGE_EMULATOR_HOST` env var. |
| Azure Blob (`services/storage/backends/azureblob`) | Not validated | Sockerless's Azure sim advertises blob endpoints in storage-account responses but only implements the Azure Files data plane. |
| AWS Secrets Manager (`services/secrets/backends/aws`) | CreateSecret + ListSecrets + DeleteSecret | HeadSecret + GetSecretValue blocked on [sockerless#175](https://github.com/e6qu/sockerless/issues/175) (sim is missing `ListSecretVersionIds`, which the shim's backend uses for monotonic-version-number derivation). |

Other shim services (rdbms, cache, functions, queue, pubsub, apigateway) are not wired yet; the sims that exist for adjacent backends (Cloud Functions v2, Cloud Run Jobs, Container Apps Environments, Container Apps Jobs, Azure Functions Sites) will land in follow-on sub-phases under Phase 13.D.

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

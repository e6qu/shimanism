# Sockerless validation lane

> Phase 13.D first cut. Uses `github.com/e6qu/sockerless` simulators to exercise the shim's per-cloud backends without requiring real AWS / GCP / Azure accounts.

## Why sockerless

The shim's backend translation layers (`services/<svc>/backends/{aws,gcs,azureblob,…}`) make outbound calls to the real cloud SDKs. Real-cloud Track A (Phase 13.D) closes BUG-8 + BUG-15 by exercising those backends end-to-end with real cloud credentials, but ahead of that, sockerless lets us catch translation defects in CI-friendly local runs.

Sockerless reimplements the public cloud HTTP wire protocols in-process. Pointing the shim's backend at a sockerless port = real SDK code path, real wire bytes, no real-cloud cost or credential plumbing.

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

## Sockerless fidelity gaps tracked upstream

| Issue | Summary |
|---|---|
| [#173](https://github.com/e6qu/sockerless/issues/173) | AWS S3 routes mounted under `/s3/` URL prefix instead of `/`. Breaks SDK / CLI / Terraform-provider default config. Workaround: append `/s3` to the endpoint URL (`https://localhost:4566/s3`). |
| [#174](https://github.com/e6qu/sockerless/issues/174) | AWS S3 simulator stores the SDK's `aws-chunked` request-body envelope verbatim. Uploads via non-seekable bodies (the common case for any streaming upload — HTTP-forwarded, encrypted, compressed) don't round-trip. |
| [#175](https://github.com/e6qu/sockerless/issues/175) | AWS Secrets Manager simulator is missing `ListSecretVersionIds`. Any SDK or shim path that maps version index → UUID hits a 400 `UnknownOperationException`. |

Closing #174 unblocks PutObject / GetObject in the storage lane; closing #175 unblocks GetSecretValue + HeadSecret in the secrets lane.

## Extending to a new service

1. Confirm the relevant sockerless simulator implements the operations you need (`/tmp/sockerless/simulators/<provider>/`).
2. Add a `TestSockerless_<Service>_<Op>` test in `services/<svc>/conformance/sockerless_test.go` (same env-controlled skip pattern as `services/storage/conformance/sockerless_test.go`).
3. If the sim needs a different startup contract (TLS / non-TLS / extra env), extend `scripts/run-sockerless-storage.sh` (or split into a per-service script + a top-level `make sockerless` aggregator).
4. File any fidelity gaps as fully self-contained issues on `e6qu/sockerless` — repro should be runnable with only sockerless checked out, no references to this repo.

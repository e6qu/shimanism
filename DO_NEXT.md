# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** Read top-to-bottom; pick up Phase 13 without re-deriving context.

## Where we are

- **Phase 13 in-flight on `phase-13` (PR #20).** All 7 Azure + 8 GCP frontends carry the spec-drift contract via blank import; 5 Azure (13.A.1-5) + 1 GCP (13.B.1) ship as full handler migrations through `gen.HandlerWithOptions` / path-shape inspection; the other 2 Azure (13.A.6 azure_blob, 13.A.7 azure_apim) + 7 GCP (13.B.2-8) carry blank-import only and defer full migration. 13.C production RS256 JWKS landed on both `gcpbearer` + `azurebearer`. 13.D.1 sockerless lane landed (storage + secrets); three sockerless fidelity gaps filed upstream as self-contained issues ([#173](https://github.com/e6qu/sockerless/issues/173), [#174](https://github.com/e6qu/sockerless/issues/174), [#175](https://github.com/e6qu/sockerless/issues/175)). 13.D.2 (real-cloud Track A — closes BUG-8 + reclassifies BUG-15) remains the next sub-phase; sockerless doesn't simulate GCP API Gateway / Pub/Sub so it can't substitute. All 18 required CI checks green on the current head.
- **Last merged:** PR #19 (Phase 12) at `778e8e9` on `main`, 2026-05-22.

## Session-start checklist

1. `git fetch origin && git checkout phase-13 && git pull` — sync.
2. `gh pr list --state open` — verify the Phase 13 PR (or create one when the first sub-phase is ready for review).
3. Read this file + [STATUS.md](STATUS.md) snapshot.
4. Skim open BUGs (2 entries below; both absorbed into 13.D).
5. Pick the next sub-phase from "Next concrete actions" — `13.A.2 azure_containerapps` is up after the current 13.A.1.

## Next concrete actions (in priority order)

### Phase 13.A — Azure adapter migration

The reference impl is `internal/secrets/frontends/azure_keyvault/server.go` (Phase 12.A.1/2). Pattern: `Server` implements `gen.ServerInterface`, `srv.mux = gen.HandlerWithOptions(srv, gen.StdHTTPServerOptions{})`, out-of-intersection ops return `notImplemented(w, "OpName")` with the Azure error envelope. See [PLAN.md § 13.A](PLAN.md#13a--azure-adapter-migration) for the per-frontend ordering.

**13.A.1 — `azure_redis`** — ✅ landed. 6 intersection ops wired through `gen.ServerInterface`; 35 out-of-intersection return Azure error envelope via `notImplemented`. `TestAzureGen_Cache_HandlerDispatch` posts a sample Create through the gen mux.

**13.A.2 — `azure_containerapps`** — ✅ landed. 5 intersection ops + 6 stubs.

**13.A.3 — `azure_dbadmin`** — ✅ landed. 10 intersection ops + 56 stubs (largest ARM gen).

**13.A.4 — `azure_servicebus` (queue)** — ✅ landed. Hybrid dispatch: hand-written regex for messaging data plane (`/messages/...`), hand-written regex into `gen.ServerInterface` methods for admin (Entity*, ListEntities). The gen mux can't be used: Go 1.22's ServeMux refuses the upstream spec's overlapping patterns (`/{entityName}` vs `/{topicName}/subscriptions` both match `GET /x/subscriptions`).

**13.A.5 — `azure_servicebus_topics` (pubsub)** — ✅ landed. Same hybrid pattern as 13.A.4. Wires topics + subscriptions admin URLs into gen.EntityPut/Get/Delete/ListEntities + gen.SubscriptionPut/Get/Delete/ListSubscriptions; data-plane Publish/Peek/Ack/Renew stay hand-written.

**13.B.1 — `gcp_secretmanager`** — ✅ full migration. Hand-written regex tables retired. ServeHTTP dispatches by path-shape inspection. `:destroy` surprise documented (no-op success matches the provider's existing tolerance pattern).

**13.B.2-8 — Remaining 7 GCP frontends (gcp_apigateway / gcp_memorystore / gcp_cloudrun / gcp_pubsub × 2 / gcp_cloudsql / gcs)** — ✅ spec-drift contract established via blank-import of `services/<svc>/gen/gcp`. The existing per-frontend regex dispatch keeps working (tests pin behaviour); the blank import makes the build-time dependency on the gen inventory explicit so deleting the gen file fails fast at build rather than at test time. Full path-shape-inspection migration (like 13.B.1) is a follow-on refactor — the existing dispatchers + `TestGCPRoutes_<Svc>_FrontendDispatchCoverage` tests already cover the spec-drift contract; the additional rewrite would be cosmetic.

**13.A.6 — `azure_blob`** — ◐ spec-drift contract via blank import landed; full handler migration deferred. The gen.ServerInterface has 69 methods and uses query-discriminated URLs (`?comp=list`, `?restype=service&comp=properties`) that Go 1.22's ServeMux doesn't natively dispatch on. Full migration needs the same hand-written-dispatch hybrid pattern 13.A.4/5 used + ~58 stub methods. The blank import gives the build-time spec-drift gate now; the full handler migration is a Phase-13-follow-on.

**13.A.7 — `azure_apim`** — ◐ spec-drift contract via blank import landed. The vendored APIM spec is intentionally minimal (0 spec operations); gen.ServerInterface is empty. The blank import documents the dependency. There's no further migration to do here until the spec is broadened.

### Phase 13.B — GCP adapter migration

Pattern: retire per-frontend regex tables, dispatch through `gen.gcp.Match()` / `MatchAll()`. The hand-written disambiguation layer for overloaded `v1/{+name}` patterns stays (Secret Manager, Pub/Sub). See [PLAN.md § 13.B](PLAN.md#13b--gcp-adapter-migration) for the per-frontend ordering.

**First sub-phase: 13.B.1 — `internal/secrets/frontends/gcp_secretmanager`.**
- 32 gen routes. Overloaded `v1/{+name}` means `MatchAll` + a small `pickByName(parent string)` helper in the frontend.
- Validation: existing `services/secrets/conformance/*` stays green; the `TestGCPRoutes_Secrets_FrontendDispatchCoverage` test in conformance already pins the dispatch shape.

### Phase 13.C — Production RS256 JWKS ✅ landed

Both `gcpbearer` and `azurebearer` accept RS256-signed JWTs in addition to test-mode HS256. Config via `Options.JWKSURL` (URL-fetched + cached + kid-rotation re-fetch) or `Options.JWKS` (in-process). Unit tests at `internal/{gcpbearer,azurebearer}/rs256_test.go`. See [doc/VERIFIERS.md § Production deployment path](doc/VERIFIERS.md#production-deployment-path-phase-13c--landed). Deployment-time choice between modes is config-only; no architectural change.

### Phase 13.D — Real-cloud Track A

Two slices:

- **13.D.1 sockerless validation lane** — ✅ landed. `make sockerless-storage` builds the AWS + GCP simulator binaries from a local clone of `github.com/e6qu/sockerless`, starts them on test-only ports (TLS for AWS, HTTP for GCP), and runs `TestSockerless_*` in `services/storage/conformance/sockerless_test.go` + `services/secrets/conformance/sockerless_test.go`. AWS S3 bucket lifecycle + GCS full round-trip + AWS Secrets Manager CreateSecret/ListSecrets/DeleteSecret pass. Three upstream gaps filed against sockerless ([#173 — S3 `/s3/` URL prefix](https://github.com/e6qu/sockerless/issues/173), [#174 — aws-chunked envelope stored verbatim](https://github.com/e6qu/sockerless/issues/174), [#175 — missing ListSecretVersionIds](https://github.com/e6qu/sockerless/issues/175)); #174 keeps AWS PutObject/GetObject out of the storage lane and #175 keeps HeadSecret/GetSecretValue out of the secrets lane. See [doc/SOCKERLESS_VALIDATION.md](doc/SOCKERLESS_VALIDATION.md).
- **13.D.2 real-cloud Track A** — pending. Still requires AWS / GCP / Azure accounts. Closes / reclassifies BUG-8 + BUG-15. Real-signed signature-verification conformance. Sockerless doesn't simulate GCP API Gateway or GCP Pub/Sub, so neither bug can be closed via the sockerless lane.

### Phase 13.E — Cross-cloud Apply matrix expansion (optional)

Phase 12 ships one cell per service (typically AWS → K8s peer). Expanding to other source/dest pairs is mechanical; pick up only as deployment scenarios demand.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- One PR per phase — all Phase 13 work lands on `phase-13`.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- Reuse-over-reinvention.

## Open bugs (2) — both absorbed into Phase 13.D.2 (real-cloud Track A)

Sockerless doesn't simulate GCP API Gateway or Pub/Sub, so neither can be closed via the 13.D.1 sockerless lane that landed on PR #20.

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. **13.D.2 only.**
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3; **13.D.2** real-cloud walk pending.

## Follow-ons (deferred from 13.D.1)

Explicit list of work this PR explored but did not finish, in priority order. Each is a candidate for the next sub-phase / PR; none block PR #20.

1. **Sockerless#174 close → AWS S3 PutObject/GetObject round-trip.** Once sockerless fixes the `aws-chunked` envelope-decoding gap, re-enable the round-trip assertion in `TestSockerless_AWS_BucketLifecycle` (or replace it with a dedicated `TestSockerless_AWS_S3_RoundTrip`).
2. **Sockerless#175 close → AWS Secrets Manager HeadSecret + GetSecretValue.** Once sockerless adds `ListSecretVersionIds`, extend `TestSockerless_AWSSecretsManager_RoundTrip` to assert metadata + value reads.
3. **Sockerless functions lane (task #110).** AWS Lambda + GCP Cloud Run + Azure Container Apps + Azure Functions Sites are all in sockerless. Wire the shim's three functions backends to them with the same pattern this PR established for storage + secrets. Same script entry-point (`scripts/run-sockerless-storage.sh` → rename to `run-sockerless-lane.sh` when this lands).
4. **Azure Blob full handler migration (13.A.6 deferred).** 69-method `gen.ServerInterface`; needs the Service-Bus hybrid-dispatch pattern (13.A.4/5) + ~58 stubs. Blank-import contract landed in PR #20; full migration deferred.
5. **GCP 7 frontends full migration (13.B.2-8 deferred).** Cosmetic refactor; existing regex dispatch already pinned to `gen.gcp.Routes` by per-service `TestGCPRoutes_<Svc>_FrontendDispatchCoverage` tests. Blank-import contract landed; full migration deferred.
6. **13.D.2 real-cloud Track A.** Requires live AWS/GCP/Azure accounts. Closes BUG-8, reclassifies BUG-15, lands real-signed signature-verification conformance. No sockerless substitute.
7. **13.E cross-cloud Apply matrix expansion.** Optional; demand-driven. Phase 12 shipped one cell per service.

## Validation lanes to monitor

- `make codegen-check` — regenerates every gen file + runs `inject-provenance`; CI's `codegen deterministic` job runs the same.
- `make spec-freshness` — informational; CI's weekly workflow surfaces upstream drift.
- `make test` — every Phase 12 smoke / coverage / regression / round-trip / preprocessor-unit test.
- Per-frontend conformance lanes (minio / gcs / vault / nats / cnpg / knative / envoy / redisop / azureblob) — every adapter migration in 13.A / 13.B must keep these green.

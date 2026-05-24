# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** Read top-to-bottom; pick up Phase 13 without re-deriving context.

## Where we are

- **Phase 14 in-flight on `phase-14`.** Branched from `main` at `3cf9e13` (PR #20 merged) on 2026-05-24. **14.A landed**: sockerless PR #179 closed all 6 round-1 issues; shim assertions re-enabled. **14.D round-2 audit done**: 8 issues filed (#181-188), all closed in sockerless PR #180. **14.B in flight**: new per-service shim lanes (pubsub GCP, queue GCP+AWS, apigateway GCP) join the storage/secrets lanes from 14.A. **GCP API Gateway shim-backend ↔ sockerless lane passes** — clears the SDK leg of BUG-8 closure. **Round-3 audit found 3 more sockerless issues** ([#189](https://github.com/e6qu/sockerless/issues/189) Pub/Sub PATCH missing — blocks BUG-15 closure via retention round-trip, [#190](https://github.com/e6qu/sockerless/issues/190) Azure Blob path-style URLs 404, [#191](https://github.com/e6qu/sockerless/issues/191) Azure KV secret HTTP scheme). 14.C (full handler migrations for 9 blank-import frontends) remains pending; independent of sockerless.
- **Last merged:** PR #20 (Phase 13) at `3cf9e13` on `main`, 2026-05-24.

## Session-start checklist

1. `git fetch origin && git checkout phase-13 && git pull` — sync.
2. `gh pr list --state open` — verify PR #20 is still the in-flight one (or that PR #20 merged and you're on a fresh branch).
3. Read this file + [STATUS.md](STATUS.md) snapshot.
4. Skim open BUGs (2 entries below; both absorbed into 13.D.2 — real-cloud Track A).
5. **If returning from sockerless upstream work:** check which sockerless issues landed (see "Resume after sockerless work" below) and re-enable the corresponding shim tests.
6. Otherwise: pick the next sub-phase from "Follow-ons (deferred from 13.D.1)" below in priority order.

## Resume after sockerless round-3 fixes

14.A done (round-1 closed in sockerless PR #179). 14.D round-2 done (round-2 #181-188 closed in sockerless PR #180). 14.B is mid-flight — five lanes pass today (storage AWS+GCS, secrets AWS, queue AWS+GCP, pubsub GCP, apigateway GCP). Three round-3 issues filed; each blocks one more lane:

| Sockerless issue | When closed, do this in the shim |
|---|---|
| [#189](https://github.com/e6qu/sockerless/issues/189) — Pub/Sub `subscriptions.patch` returns 404 | Re-instate the retention round-trip in `TestSockerless_GCP_Queue_CRUD` (currently the test only does CRUD, not SetQueueAttributes). The retention round-trip is the BUG-15 closure check. |
| [#190](https://github.com/e6qu/sockerless/issues/190) — Azure Blob host-based-only dispatch | Add `TestSockerless_Azure_Blob_*` in `services/storage/conformance/sockerless_test.go` using path-style URLs (the SDK + provider default). |
| [#191](https://github.com/e6qu/sockerless/issues/191) — Azure KV secret HTTP scheme | Add `TestSockerless_Azure_KeyVault_*` in `services/secrets/conformance/sockerless_test.go` and assert all returned ids/kids start with `https://`. |

Other lanes worth adding (gated on no upstream blocker today):

- AWS Lambda functions via shim's `functions/backends/aws`.
- GCP Secret Manager via shim's `secrets/backends/gcp`.
- Azure Key Vault via shim's `secrets/backends/azurekv` (will surface the #191 gap on HTTP sims; works under TLS).
- GCP Cloud SQL via shim's `rdbms/backends/gcp`.
- Azure PostgreSQL FlexibleServer via shim's `rdbms/backends/azurepg`.
- Azure Cache for Redis via shim's `cache/backends/azureredis`.
- Azure APIM via shim's `apigateway/backends/azureapim`.
- Azure Service Bus via shim's `queue/backends/azuresb` + `pubsub/backends/azuresb`.

**Closing #189 closes BUG-15 directly** (retention round-trip works through the shim's SetQueueAttributes → PATCH path).

**BUG-8 SDK leg is already cleared** (the GCP APIGW shim backend → sockerless lane passes); the TF-provider angle remains in Phase 14.D as the residual real-cloud Track A piece.

Always update `doc/SOCKERLESS_VALIDATION.md` + `BUGS.md` § Sockerless when a tracked gap closes.

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

## Open bugs (2) — absorbed into Phase 14.D (real-cloud Track A residual)

Sockerless doesn't simulate GCP API Gateway or Pub/Sub today, so neither can be closed via the 13.D.1 sockerless lane that landed on PR #20. Closure paths:

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. Closes via **14.B.2** if sockerless#177 adds API Gateway; otherwise via **14.D** real-cloud walk.
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3. Reclassifies via **14.B.2** if sockerless#177 adds Pub/Sub; otherwise via **14.D** real-cloud walk.

## Follow-ons — all rolled into Phase 14

Everything this PR explored but did not finish has been bundled into **Phase 14** ([PLAN.md § Phase 14](PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons)). The premise: sockerless simulators are the right vehicle for cross-cloud shim verification + Terraform-provider round-trip testing at CI tempo, so Phase 14 unlocks as the upstream sockerless issues we filed close. Real-cloud Track A becomes a residual for whatever sockerless can't cover.

Priority order (per [PLAN.md § Phase 14 sub-phases](PLAN.md#sub-phases)):

| Track | Work | Sockerless dependency |
|---|---|---|
| 14.A | Re-enable shim assertions blocked by 3 sockerless fidelity bugs (drop `/s3` workaround; S3 round-trip; SM HeadSecret + GetSecretValue). | [#173](https://github.com/e6qu/sockerless/issues/173) / [#174](https://github.com/e6qu/sockerless/issues/174) / [#175](https://github.com/e6qu/sockerless/issues/175) closing. |
| 14.B | Add new sockerless service lanes (AWS SQS/SNS/APIGW/RDS/EC; GCP Pub/Sub/Secrets/SQL/Memorystore/APIGW; Azure Blob+KV data plane / Service Bus / PG / Redis / APIM). | [#176](https://github.com/e6qu/sockerless/issues/176) / [#177](https://github.com/e6qu/sockerless/issues/177) / [#178](https://github.com/e6qu/sockerless/issues/178) closing. |
| 14.C | Full handler migrations for the 9 Phase-13 blank-import frontends (`azure_blob` + 7 GCP + `azure_apim`-on-spec-broadening). | — (independent of sockerless). |
| 14.D | Real-cloud Track A residual — close/reclassify BUG-8 + BUG-15 for any portion 14.B doesn't cover. | sockerless#177 result. |
| 14.E | Cross-cloud Apply matrix expansion, driven by 14.B lanes. | 14.B in progress. |

None of these block PR #20.

## Validation lanes to monitor

- `make codegen-check` — regenerates every gen file + runs `inject-provenance`; CI's `codegen deterministic` job runs the same.
- `make spec-freshness` — informational; CI's weekly workflow surfaces upstream drift.
- `make test` — every Phase 12 smoke / coverage / regression / round-trip / preprocessor-unit test.
- Per-frontend conformance lanes (minio / gcs / vault / nats / cnpg / knative / envoy / redisop / azureblob) — every adapter migration in 13.A / 13.B must keep these green.

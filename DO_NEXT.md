# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** Read top-to-bottom; pick up Phase 14 without re-deriving context.

## Where we are

- **Phase 14 in-flight on branch `phase-14-bundled-bce-bug24`.** This branch bundles 14.B Azure ARM backend-adapter lanes with file-and-defer status updates for 14.C / 14.E / BUG-24 expansion. PR open once docs + branch are pushed.
- **Sockerless lane now reports 23 passing + 1 default-skipped** against sockerless `f858d66`. New green lanes:
  - `TestSockerless_Azure_Cache_Redis_CRUD` — Microsoft.Cache/Redis ARM CRUD via armredis/v3.
  - `TestSockerless_Azure_RDBMS_PostgreSQL_CRUD` — Microsoft.DBforPostgreSQL/flexibleServers via armpostgresqlflexibleservers/v4.
  - `TestSockerless_Azure_APIGateway_APIM_CRUD` — Microsoft.ApiManagement/service+apis via armapimanagement/v3, with parent Service pre-created.
- **`TestSockerless_Azure_Functions_ContainerApps_CRUD`** is wired but default-skipped (BUG-35). Opts in via `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE`.
- **Two new upstream sockerless gaps surfaced this PR.** [sockerless#223](https://github.com/e6qu/sockerless/issues/223) (BUG-34) — Azure SB namespace-level ATOM XML admin protocol not implemented, blocks SB queue + pubsub lanes. [sockerless#224](https://github.com/e6qu/sockerless/issues/224) closed as not-a-bug (sockerless legitimately does real container execution).
- **GCP Secret Manager is wired.** The real backend lane uses the official `cloud.google.com/go/secretmanager/apiv1` REST client against sockerless and covers CreateSecret, PutSecretValue, HeadSecret, GetSecretValue(latest + explicit version), ListVersions, ListSecrets, UpdateSecret, and DeleteSecret. No workaround, fake, mock, or partial test is carried.
- **Current sockerless coverage:** storage AWS S3 + GCS + Azure Blob backend-adapter lanes plus storage through-shim E2E cells for AWS -> GCP, GCP -> Azure, and Azure -> AWS; secrets AWS Secrets Manager + GCP Secret Manager + Azure Key Vault; queue AWS SQS + GCP Pub/Sub queue; pubsub GCP Pub/Sub; apigateway GCP API Gateway.
- **Still local/open:** BUG-8 and BUG-15 are not upstream-sockerless blockers. BUG-8 is the hashicorp/google API Gateway Terraform endpoint/OAuth leg. BUG-15 is the hashicorp/google `message_retention_duration` state-drift question; the shim's GCP queue backend retention PATCH/read path is green. BUG-24 tracks expanding through-shim sockerless E2E beyond storage.
- **Last merged:** PR #37 (fix the end-to-end-walkthrough fidelity bug cluster, BUG-30..33) on `main`, 2026-05-26.

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Create a new branch from `main` for the next Phase 14 sub-phase before editing.
3. If sockerless changed again, `git -C /tmp/sockerless pull --ff-only`, rebuild the three sims, and rerun `make sockerless`.
4. Read [STATUS.md](STATUS.md) + this file. Skim the two open local bugs below.
5. Pick the next Phase 14 item: choose a different remaining sockerless lane, finish 14.C handler migrations, or continue real-cloud Track A residuals.

## Resume after sockerless work

14.A done (round-1 closed in sockerless PR #179). 14.D audit rounds through PR #216 are done, and #218 closed in PR #219 after the GCP Secret Manager lifecycle gap was filed. There are no open upstream sockerless issues at the time of this update. 14.B's current shim lane is green.

If sockerless lands more changes, re-run:

```sh
git -C /tmp/sockerless pull --ff-only
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/aws/simulator-aws /tmp/sockerless/simulators/aws
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/gcp/simulator-gcp /tmp/sockerless/simulators/gcp
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/azure/simulator-azure /tmp/sockerless/simulators/azure
make sockerless
```

Other lanes worth adding (gated on no upstream blocker today):

- AWS Lambda functions via shim's `functions/backends/aws`.
- GCP Cloud SQL via shim's `rdbms/backends/gcp`.
- Azure PostgreSQL FlexibleServer via shim's `rdbms/backends/azurepg`.
- Azure Cache for Redis via shim's `cache/backends/azureredis`.
- Azure APIM via shim's `apigateway/backends/azureapim`.
- Azure Service Bus via shim's `queue/backends/azuresb` + `pubsub/backends/azuresb`.

**BUG-15 backend leg is cleared**: the GCP queue backend now exercises SetQueueAttributes → Pub/Sub PATCH → HeadQueue and asserts `MessageRetentionSeconds = 604800` via `TestSockerless_GCP_Queue_RetentionRoundTrip`.

**BUG-8 SDK/backend leg is cleared**: the GCP APIGW shim backend → sockerless lane passes. The TF-provider angle remains the local open bug.

Always update `docs/sockerless-validation.md` + `BUGS.md` § Sockerless when a tracked gap closes.

## Next concrete actions (in priority order)

1. Land the current PR (`phase-14-bundled-bce-bug24`): 3 new Azure ARM backend-adapter lanes (Redis / PostgreSQL / APIM) + scaffolded Container Apps lane + 2 new BUGs filed (BUG-34 / BUG-35) + 2 upstream issues filed at e6qu/sockerless.
2. **Unblock BUG-34** by watching for [sockerless#223](https://github.com/e6qu/sockerless/issues/223) to close (Azure SB namespace ATOM XML), then wire `TestSockerless_Azure_ServiceBus_Queue_*` + `TestSockerless_Azure_ServiceBus_Topic_*` lanes against the now-supported protocol.
3. **Close BUG-35** by either (a) extending `scripts/run-sockerless-storage.sh` to pre-pull a small public Container Apps image and set `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` before invoking `go test`, or (b) asking upstream for a no-op-image mode.
4. **14.C handler migrations** (still independent of sockerless):
   - `gcp_apigateway` (502 lines), `gcp_memorystore` (319), `gcp_cloudrun` (305), gcp_pubsub × 2, gcp_cloudsql, gcs — replace regex tables with path-shape inspection like `gcp_secretmanager`. Each is its own PR; existing `TestGCPRoutes_*_FrontendDispatchCoverage` pins the dispatch shape so the refactor is mechanically safe.
   - `azure_blob` (69 ops) — needs the Service-Bus-style hybrid pattern; biggest single chunk.
5. **14.E cross-cloud Apply cell expansion** — driven by the new sockerless lanes; can add Azure-source-and-destination cells now that the ARM lanes are green.
6. **Track A residuals** (BUG-8 / BUG-15) — still need real-cloud GCP credentials.

## Historical migration context

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

Both `gcpbearer` and `azurebearer` accept RS256-signed JWTs in addition to test-mode HS256. Config via `Options.JWKSURL` (URL-fetched + cached + kid-rotation re-fetch) or `Options.JWKS` (in-process). Unit tests at `internal/{gcpbearer,azurebearer}/rs256_test.go`. See [docs/verifiers.md § Production deployment path](docs/verifiers.md#production-deployment-path-phase-13c--landed). Deployment-time choice between modes is config-only; no architectural change.

### Phase 13.D — Real-cloud Track A

Two slices:

- **13.D.1 sockerless validation lane** — ✅ landed. `make sockerless-storage` builds the AWS + GCP simulator binaries from a local clone of `github.com/e6qu/sockerless`, starts them on test-only ports (TLS for AWS, HTTP for GCP), and runs `TestSockerless_*` in `services/storage/conformance/sockerless_test.go` + `services/secrets/conformance/sockerless_test.go`. AWS S3 bucket lifecycle + GCS full round-trip + AWS Secrets Manager CreateSecret/ListSecrets/DeleteSecret pass. Three upstream gaps filed against sockerless ([#173 — S3 `/s3/` URL prefix](https://github.com/e6qu/sockerless/issues/173), [#174 — aws-chunked envelope stored verbatim](https://github.com/e6qu/sockerless/issues/174), [#175 — missing ListSecretVersionIds](https://github.com/e6qu/sockerless/issues/175)); #174 keeps AWS PutObject/GetObject out of the storage lane and #175 keeps HeadSecret/GetSecretValue out of the secrets lane. See [docs/sockerless-validation.md](docs/sockerless-validation.md).
- **13.D.2 real-cloud Track A** — pending. Still requires AWS / GCP / Azure accounts for real-signed signature-verification conformance and the remaining Terraform-provider legs of BUG-8 / BUG-15.

### Phase 13.E — Cross-cloud Apply matrix expansion (optional)

Phase 12 ships one cell per service (typically AWS → K8s peer). Expanding to other source/dest pairs is mechanical; pick up only as deployment scenarios demand.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- One branch/PR per phase or sub-phase. PR #21 is merged; start a new branch from `main` for the next Phase 14 chunk.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- Reuse-over-reinvention.

## Open bugs (3) — absorbed into Phase 14

Sockerless no longer blocks BUG-8 or BUG-15. Both are now local/provider residuals:

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. The GCP APIGW backend lane passes against sockerless; the Terraform-provider leg still needs Track A or explicit provider endpoint wiring.
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. The GCP queue backend retention PATCH/read path passes against sockerless; the remaining question is whether hashicorp/google shows the same `message_retention_duration` state drift against real GCP.
- **BUG-24** (P2) — sockerless/conformance. Storage now has source-client -> shim frontend -> shim backend -> sockerless cross-cloud E2E cells; secrets, queue, pubsub, rdbms, cache, functions, and apigateway need the same pattern.

## Follow-ons — all rolled into Phase 14

Everything this PR explored but did not finish has been bundled into **Phase 14** ([PLAN.md § Phase 14](PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons)). The premise: sockerless simulators are the right vehicle for cross-cloud shim verification + Terraform-provider round-trip testing at CI tempo, so Phase 14 unlocks as the upstream sockerless issues we filed close. Real-cloud Track A becomes a residual for whatever sockerless can't cover.

Priority order (per [PLAN.md § Phase 14 sub-phases](PLAN.md#sub-phases)):

| Track | Work | Sockerless dependency |
|---|---|---|
| 14.A | Re-enable shim assertions blocked by 3 sockerless fidelity bugs (drop `/s3` workaround; S3 round-trip; SM HeadSecret + GetSecretValue). | ✅ landed after [#173](https://github.com/e6qu/sockerless/issues/173) / [#174](https://github.com/e6qu/sockerless/issues/174) / [#175](https://github.com/e6qu/sockerless/issues/175). |
| 14.B | Add new sockerless service lanes (AWS SQS/SNS/APIGW/RDS/EC; GCP Pub/Sub/Secrets/SQL/Memorystore/APIGW; Azure Blob+KV data plane / Service Bus / PG / Redis / APIM). | ◐ current 10-test lane green after sockerless PR #219; additional lanes optional follow-on. |
| 14.C | Full handler migrations for the 9 Phase-13 blank-import frontends (`azure_blob` + 7 GCP + `azure_apim`-on-spec-broadening). | — (independent of sockerless). |
| 14.D | Real-cloud Track A residual — close/reclassify BUG-8 + BUG-15 Terraform-provider legs and real-signed verifier conformance. | No sockerless blocker remains. |
| 14.E | Cross-cloud Apply matrix expansion, driven by 14.B lanes. | 14.B in progress. |

None of these block the current green sockerless lane.

## Validation lanes to monitor

- `make codegen-check` — regenerates every gen file + runs `inject-provenance`; CI's `codegen deterministic` job runs the same.
- `make spec-freshness` — informational; CI's weekly workflow surfaces upstream drift.
- `make test` — every Phase 12 smoke / coverage / regression / round-trip / preprocessor-unit test.
- Per-frontend conformance lanes (minio / gcs / vault / nats / cnpg / knative / envoy / redisop / azureblob) — every adapter migration in 13.A / 13.B must keep these green.

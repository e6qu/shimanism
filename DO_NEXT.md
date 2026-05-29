# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** Read top-to-bottom; pick up Phase 14 without re-deriving context.

## Where we are

- **3-PR plan: 2/3 shipped; PR 3 (Track A) blocked.** PR #46/#47/#48/#49/#50 all landed 2026-05-27.
- **BUG-24 reverse-direction coverage is now complete** — every service family has both cross-cloud directions (PR #50).
- **14.E ARM-shimming — DIRECTION CORRECTION.** PRs #51 / #52 / #53 / #54 introduced ARM-shimming fakes (synthetic vault/account responses, in-process `Track*` state, mock-Microsoft-Entra, hardcoded `listKeys` synthetic) that violate shimanism's "no fakes / stateless shim" rules. The in-flight revert PR removes these. The honest path for cross-cloud Apply: sockerless PR #259 (merged 2026-05-28) added configurable Azure ARM data-plane endpoint emission via `SIM_AZURE_ARM_EXTERNAL_DATA_PLANE_URLS_JSON`; a follow-on PR will wire shimanism's harness to set this env var pointing at the shim's data-plane URLs, letting `azurerm → sockerless real ARM → shim data plane → backend` compose without any shim-side fakes.
- **Phase 13.A is fully closed.** Every Azure frontend has full `gen.ServerInterface` impl.
- `make sockerless` reports **43 passing + 0 skipped** locally after the revert (PR #54's ARM cells removed).
- **Storage matrix complete 3×3** — single-shot + multipart + copy across AWS S3 + GCS + Azure Blob.
- **Service Bus matrix complete** — admin (ATOM XML) + Send/Receive data-plane (raw AMQP/TLS via `azservicebus.ClientOptions.CustomEndpoint`).
- **Azure ARM lanes complete** for Redis / PG / APIM via custom `arm.ClientOptions.Cloud.ResourceManager.Endpoint`.
- **GCP Cloud Run lane** uses `google.golang.org/api/run/v2` against sockerless's Cloud Run handler.
- **Through-shim cross-cloud cells cover both directions for cache / secrets / queue** (AWS↔GCP). Other families still cover one direction; reverse-direction expansion is ongoing BUG-24 work.
- **All 7 GCP frontends migrated** from `regexp` tables to `strings.CutPrefix` + `strings.Split` + `segs[N]` dispatch. `regexp` import retired from all per-frontend GCP handlers.
- **All Azure frontends carry full `gen.ServerInterface` impls** with the `var _ gen.ServerInterface = (*Server)(nil)` compile-time gate.
- **Open BUGs (2):** BUG-8 + BUG-15 (Track A, real GCP needed). BUG-35 closed in PR #48 after sockerless PR #245 derived ACA image platforms from the resolved manifest.
- **Upstream watch:** zero open sockerless issues. PR #245 closed #243 + #244.
- **Last merged:** PR #47 — Phase 13.A.6 `azure_blob` full handler migration, 2026-05-27.

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. If `/tmp/sockerless` is stale: `git -C /tmp/sockerless pull --ff-only`, rebuild sims, rerun `make sockerless` to confirm 33+1 baseline before editing.
3. Create a new branch from `main` for the next PR.
4. Read [STATUS.md](STATUS.md) + this file. Skim BUGS.md § Open.
5. Pick from the 3-PR closure plan below.

## The 3-PR closure plan — status

- **PR 1 — ✅ shipped as PR #46** (2026-05-27). BUG-35 shim-side plumbing + GCP Cloud Run lane + 3 reverse-direction cells (cache / secrets / queue, all GCP→AWS) + all 7 14.C GCP frontend migrations.
- **PR 2 — ✅ shipped as PR #47** (2026-05-27). Phase 13.A.6 `azure_blob` full `gen.ServerInterface` impl: 12 in-intersection bridges + 57 out-of-intersection stubs. Phase 13.A officially closed.
- **PR 3 — blocked on real-cloud credentials.**

### PR 3 — "Track A residuals" — BLOCKED ON INFRA

Real-cloud lanes for BUG-8 (hashicorp/google API Gateway TF endpoint/OAuth leg) + BUG-15 (`message_retention_duration` state-drift question) + real-signed verifier conformance. Requires AWS / GCP / Azure accounts. Not actionable until infra exists.

## Practical next chunks (while Track A is blocked)

1. ~~BUG-24 reverse-direction expansion.~~ ✅ Shipped in PR #50.
2. **14.E cross-cloud Apply via sockerless-driven ARM.** Three of four sockerless prerequisites landed (#259 endpoint emission, #260 deterministic 64-byte storage keys, #262 RS256 JWKS-published Azure AD tokens). PR #58 wires the honest path end-to-end through the data plane, but exposed a fourth gap: the literal `primary_blob_endpoint` emitted by #259 (e.g. `http://localhost:14581/`) is rejected by the `azurerm` provider parser, which requires `{account}.blob.{suffix}` with a matching suffix published by `/metadata/endpoints`. **Filed [sockerless#269](https://github.com/e6qu/sockerless/issues/269)** for interpolated emission (`http://{account}.blob.localhost:14581/`) + metadata-side suffix advertisement. `TestSockerless_E2E_AzureBlob_Through_Shim_ApplyTF` is currently skipped pointing at that issue; re-enable once it lands.
   - Pre-#269, the upstream composition already verified: TLS path, sockerless ARM resource lifecycle, shim's harness URL emission, listKeys/key derivation alignment. The remaining gap is purely endpoint shape.
   - **Next services (after #269):** Key Vault (sockerless#262 RS256 makes Bearer verification feasible). Service Bus admin + queues + topics (sockerless already emits SAS connection strings).
3. ~~Watch sockerless#244.~~ ✅ Done in PR #49.

### Lesson: ARM shimming via fakes was the wrong design

PRs #51–#54 built `internal/storage/frontends/azure_arm_storageaccounts/`, `internal/secrets/frontends/azure_arm_keyvault/`, `internal/mockaad/`, an `armResourcesStub` middleware, in-process `Track*` state, synthetic `StorageAccountsListKeys` with hardcoded keys matching the harness verifier, and a synthetic Microsoft Entra OIDC endpoint. Every one of these is a "canned-response path" / "fake HTTP server" / "in-memory stand-in for real cloud state" — violations of the no-fakes rule. The user [stopped the work mid-flight](https://github.com/e6qu/shimanism/pull/55#issuecomment-4564061276) and pointed out the violation. The honest answer was always sockerless: ARM with real state lives there. Filed [sockerless#257](https://github.com/e6qu/sockerless/issues/257), maintainer landed [#259](https://github.com/e6qu/sockerless/pull/259) within hours. Revert PR (this PR) cleans up the shim-side fakes; future PR wires through the honest path.

## Sockerless rebuild + lane run (when needed)

If sockerless changes:

```sh
git -C /tmp/sockerless pull --ff-only
cd /tmp/sockerless/simulators/aws && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-aws .
cd /tmp/sockerless/simulators/gcp && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-gcp .
cd /tmp/sockerless/simulators/azure && GOWORK=off CGO_ENABLED=0 go build -tags noui -o ./simulator-azure .
cd /Users/zardoz/projects/shimanism && make sockerless
```

The Azure sim now needs `SIM_SERVICEBUS_AMQP_LISTEN_ADDR` set on a separate port (handled by `scripts/run-sockerless-storage.sh` since PR #42's merge).

## Standing rules (carried forward)

- File or reopen upstream sockerless issues for any gap surfaced by shim work; never paper over with a workaround in shim test code.
- Test driver is the cloud SDK / CLI / Terraform provider; transport beneath that is the SDK's business (no WebSocket / AMQP / protocol code in test code).
- `make sockerless` baseline as of PR #46: **37 passing + 1 documented-skipped** (Container Apps, awaiting sockerless#244).

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

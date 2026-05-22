# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** Read top-to-bottom; pick up Phase 13 without re-deriving context.

## Where we are

- **Phase 13 in-flight on `phase-13`.** 13.A.1 (`azure_redis`) migrated through `gen.HandlerWithOptions`. The rest of 13.A (6 Azure frontends), 13.B (8 GCP frontends), 13.C (RS256 JWKS), 13.D (Track A — BUG-8, BUG-15) pending. Sub-phase table in [PLAN.md § Phase 13](PLAN.md#phase-13--full-adapter-migration--production-auth--real-cloud-track-a).
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

**13.A.2 — `azure_containerapps`** — ✅ landed. 5 intersection ops (CreateOrUpdate / Get / Delete / Update / ListByResourceGroup); 6 out-of-intersection stubs. `Properties` is the anonymous struct gen.ContainerApp emits — populated via JSON round-trip from a `map[string]any` literal so we don't restate the anonymous struct at each call site.

**Next sub-phase: 13.A.3 — `internal/queue/frontends/azure_servicebus` OR `internal/pubsub/frontends/azure_servicebus_topics`.**
- **Hazard:** the gen spec uses lowercase `subscriptions` in paths; the existing hand-written frontend + conformance tests use capital `Subscriptions` (historical Azure REST admin URL form). Migration needs a pre-dispatch URL-case normalizer OR a hybrid (gen mux for entity URLs, hand-written regex for the data-plane `/messages/...` URLs which aren't in the admin spec at all).
- Service Bus is also the spec shared between queue + pubsub — fix once, ship twice.

**Next sub-phase: 13.A.4 — `internal/apigateway/frontends/azure_apim`.**
- The vendored APIM spec is intentionally minimal (0 spec operations). The gen.ServerInterface is empty. Adapter migration here is "types-only" — there's no `gen.HandlerWithOptions` mux to swap in; the work is wiring the gen wire types into request decoding + response encoding for the existing handlers. May be worth deferring as out-of-scope-for-Phase-13 since there's no spec contract to migrate to.

**Next sub-phase: 13.A.6 — `internal/rdbms/frontends/azure_dbadmin`** (PostgreSQL FlexibleServer).
- gen interface has 66 methods (~6 in intersection). Largest stub-count migration. Server struct is a proper Go struct after BUG-20 flatten.

**Next sub-phase: 13.A.7 — `internal/storage/frontends/azure_blob`.**
- gen interface has 69 methods (Blob data-plane). Biggest hand-written frontend (620 LOC). Different shape — data-plane spec with `x-ms-paths` flattened in Phase 12.A.15.

### Phase 13.B — GCP adapter migration

Pattern: retire per-frontend regex tables, dispatch through `gen.gcp.Match()` / `MatchAll()`. The hand-written disambiguation layer for overloaded `v1/{+name}` patterns stays (Secret Manager, Pub/Sub). See [PLAN.md § 13.B](PLAN.md#13b--gcp-adapter-migration) for the per-frontend ordering.

**First sub-phase: 13.B.1 — `internal/secrets/frontends/gcp_secretmanager`.**
- 32 gen routes. Overloaded `v1/{+name}` means `MatchAll` + a small `pickByName(parent string)` helper in the frontend.
- Validation: existing `services/secrets/conformance/*` stays green; the `TestGCPRoutes_Secrets_FrontendDispatchCoverage` test in conformance already pins the dispatch shape.

### Phase 13.C — Production RS256 JWKS

Wire real Microsoft Entra + Google JWKS in `internal/azurebearer/` + `internal/gcpbearer/`. Test mode HS256 stays default. See [doc/VERIFIERS.md § Production deployment path](doc/VERIFIERS.md#production-deployment-path-phase-13c). Add `TestAzureBearer_RealJWKS_*` / `TestGCPBearer_RealJWKS_*` against a mocked JWKS endpoint.

### Phase 13.D — Real-cloud Track A

Requires AWS / GCP / Azure accounts. Closes / reclassifies BUG-8 + BUG-15 (see Open bugs below). Real-signed signature-verification conformance.

### Phase 13.E — Cross-cloud Apply matrix expansion (optional)

Phase 12 ships one cell per service (typically AWS → K8s peer). Expanding to other source/dest pairs is mechanical; pick up only as deployment scenarios demand.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- One PR per phase — all Phase 13 work lands on `phase-13`.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.
- Fidelity to the source cloud's API; real backends only; tests from official client surfaces.
- Reuse-over-reinvention.

## Open bugs (2) — both absorbed into Phase 13.D

- **BUG-8** (P3) — apigateway/gcp-tf-frontend. `hashicorp/google` API Gateway endpoint-override + real OAuth signing. **Track A only.**
- **BUG-15** (P3) — queue/gcp-frontend. GCP Pub/Sub retention plan/apply asymmetry. Partial fix landed in Phase 10.3; **Track A** real-cloud walk pending.

## Validation lanes to monitor

- `make codegen-check` — regenerates every gen file + runs `inject-provenance`; CI's `codegen deterministic` job runs the same.
- `make spec-freshness` — informational; CI's weekly workflow surfaces upstream drift.
- `make test` — every Phase 12 smoke / coverage / regression / round-trip / preprocessor-unit test.
- Per-frontend conformance lanes (minio / gcs / vault / nats / cnpg / knative / envoy / redisop / azureblob) — every adapter migration in 13.A / 13.B must keep these green.

# shimanism — What We Did

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md).

> Reverse chronological. One section per phase. The *why*, the surprises, the root causes — not per-PR detail. For commit-level history, `git log`. For per-bug detail, [BUGS.md](BUGS.md).

## Phase 12 — Cross-cloud cell expansion + Phase 11 follow-ons (in-flight on `phase-12`, PR #19)

Two tracks per [PLAN.md § Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons). Track 2 (Phase 11 follow-ons) is where the work has gone so far; Track 1 (cross-cloud cells) is partially already proven by Phase 10's cross-cloud Apply tests and gets revisited later in the phase.

### Track 2.A — Broader Azure spec-driven migration

The 11.4 pilot proved `cmd/azure-codegen` end-to-end on a single handler (SetSecret) in `azure_keyvault`. Phase 12 takes it the rest of the way:

- **12.A.1** completes `azure_keyvault`'s type-level migration: every handler emits `gen.SecretBundle` / `gen.SecretAttributes` / `gen.SecretListResult` / `gen.DeletedSecretBundle` instead of hand-rolled wire shapes. 78 LOC of local types retired.
- **12.A.2** retires the regex router. `Server` implements `gen.ServerInterface`; `gen.HandlerWithOptions` does dispatch. Spec operations outside the cross-cloud secrets intersection (Backup / Restore / UpdateSecret / GetDeletedSecret*) return a canonical "operation not supported" envelope.
- **12.A.3** validates the pipeline scales to a second Azure data-plane spec: vendored Azure Service Bus 2021-05.
- **12.A.7** unblocks ARM-shaped specs via a common-types inliner. Azure ARM specs `$ref` shared definitions in `common-types/resource-management/v<N>/<file>.json` by relative path. `kin-openapi`'s loader refuses external refs by default. The inliner merges every reachable common-types file's `definitions`/`parameters` into the main spec at the v2 layer (before ToV3) and rewrites the external refs to local pointers. First ARM proof point: Azure Cache for Redis (216 KB generated).
- **12.A.8** vendors common-types v1–v6 and teaches the inliner three relative-ref forms (full path, same-version sibling, cross-version sibling). APIM minimal spec lands as second ARM proof. Azure-generated code moves to `services/<svc>/gen/azure/` package `azure` so it doesn't collide with AWS Smithy gen in the same `gen/` namespace.
- **12.A.9** adds the multi-file sibling inliner: `./<file>.json` refs from the main spec resolve against the spec's own directory. ContainerApps + its `CommonDefinitions.json` sibling generate cleanly.
- **12.A.10** pre-rewrites merged-content refs to local form before stuffing them into the doc, so the outer walk doesn't re-classify shorthand refs against the wrong context. Skips `./examples/<file>` x-ms-examples refs.

All 8 Azure specs generate end-to-end via `make codegen`:
  services/secrets/gen/azure/azure_keyvault.gen.go         (data-plane)
  services/queue/gen/azure/azure_servicebus.gen.go         (data-plane)
  services/pubsub/gen/azure/azure_servicebus.gen.go        (data-plane, shared spec)
  services/cache/gen/azure/azure_redis.gen.go              (ARM)
  services/apigateway/gen/azure/azure_apimanagement.gen.go (ARM, minimal)
  services/functions/gen/azure/azure_containerapps.gen.go  (ARM, multi-file)
  services/rdbms/gen/azure/azure_postgresql.gen.go         (ARM, x-ms-enum collisions)
  services/storage/gen/azure/azure_blob.gen.go             (data-plane, 60 x-ms-paths + parameter/header gating + LeaseDuration collision)

- **12.A.20** unlocks Blob — the largest data-plane spec — with two additional preprocessor improvements. (i) `promoteXMsEnumName` now tracks "non-schema depth" through `parameters`/`headers` containers; a top-level `parameters.AccessTierOptional` has `x-ms-enum.name = "AccessTier"` matching `definitions.AccessTier`, but rewriting the parameter to a `$ref` to the schema breaks v2→v3 conversion (parameter refs must point at parameter objects). Headers have the same shape and the same problem (`x-ms-copy-status` with `x-ms-enum.name = "CopyStatusType"` matching `definitions.CopyStatus`). The walker now suppresses promotion under those containers; a `schema` or `items` sub-key resets the depth so body-parameter schemas still get the inline-→-ref rewrite. (ii) A new `dedupeParameterDefNameCollisions` stage stamps `x-go-name: <N>Parameter` when `parameters.<N>` and `definitions.<N>` share a name — Blob ships such a collision for `LeaseDuration` (string enum schema vs integer header parameter). Output: 1.1 MB / 25k lines.
- **12.A.24** closes BUG-20 with `flattenARMAllOf`. ARM resource definitions use the pattern `{ type: object, allOf: [{$ref: TrackedResource}], properties: {own props} }`; oapi-codegen sees the 1-element allOf and emits `type X = TrackedResource` — a Go type alias that silently discards the schema's own properties. The new preprocessor stage walks every top-level definitions.X, and when X has both allOf (with local $refs) AND its own properties, inlines the referenced schemas' properties into X then drops the allOf array. Local properties win on key collision (spec-author override). Iterates until no definition changes to handle inheritance chains. Result: ContainerApp / RedisResource / Server (PostgreSQL FlexibleServer) emit as proper Go structs with their own field sets — unblocks Azure ARM adapter migration.
- **12.0.4** stamps a `_provenance` top-level key into every vendored spec JSON. User asked vendored schemas to self-document their origin "in them"; JSON has no comment syntax, but a top-level field is the closest analogue and codegen tools tolerate unknown keys. New `cmd/inject-provenance` reads a SOURCES.md table and writes `{upstream_repo, upstream_path, upstream_license, pinned_at, fetched_utc, note}` as the first key of each spec; idempotent on re-run; preserves source-file key ordering for everything else (encoding/json's map iteration would otherwise re-sort lexicographically and diff massively against verbatim upstream). Applied to 24 service specs + 26 common-types files. `scripts/fetch-aws-spec.sh` now runs the injector after download.

### Surprises along the way

- **Two SDK idioms aren't in the upstream OpenAPI spec.** `GET /secrets/{name}/` (trailing slash, empty version) and `GET /secrets/{name}` (no slash) both mean "latest version" to the Azure SDK; the spec only emits the two-segment `GET /secrets/{secret-name}/{secret-version}` route. Handled by a pre-dispatch pass in `azure_keyvault`'s `ServeHTTP`.
- **`gen.StdHTTPServerOptions.Middlewares` are per-route, not outer.** They wrap inside each `ServerInterfaceWrapper` method, applied after the mux dispatches. Trailing-slash normalisation has to happen BEFORE the mux sees the path — so it sits outside `HandlerWithOptions`.
- **`kin-openapi`'s empty-`AllOf` shim shows up everywhere.** Component schemas (KV's `DeletionRecoveryLevel`), path-item parameters (SB's `{minLength: 1}` strings on `entityName` etc.), and response/request-body headers all get empty `AllOf: []` after v2→v3 conversion. `normalizeAllOf` walks all the spots so `oapi-codegen.MergeSchemas` doesn't panic on `allOf[0]`.
- **Type-name collisions across AWS+Azure gen forced a subpackage split.** Container Apps' `Runtime` (a struct) and Lambda's `Runtime` (an enum) collide if both gens land in the same `gen/` package. Azure gen now lives at `services/<svc>/gen/azure/` with `package azure`.
- **The inliner walks back into merged content.** First version of the inliner copied common-types definitions into `doc.definitions` verbatim. Then the outer doc walk re-entered them and tried to classify their original `./X.json` shorthand refs against the spec dir (the wrong context). Fix: rewrite refs to local form before merging.
- **Azure x-ms-examples create false sibling refs.** Spec files carry `./examples/X.json` refs for ARM example payloads — `oapi-codegen` ignores them, but the inliner has no reason to follow them either. Classifier skips them.
- **Inline-enum collisions need `x-ms-enum` honored.** Azure spec authors use `x-ms-enum.name` to say "this inline enum IS the top-level enum of the same name." oapi-codegen ignores the extension; the inline schema gets a Go name derived from the property path (`HighAvailabilityMode` for `HighAvailability.properties.mode`) which collides with the standalone `definitions/HighAvailabilityMode`. The `promoteXMsEnumName` preprocessor walks the spec, finds inline schemas whose `x-ms-enum.name` matches a top-level definition's `x-ms-enum.name`, and rewrites the inline to a `$ref` to the top-level. (Promoting `x-ms-enum.name` to `x-go-name` on the ref-target schema doesn't work — oapi-codegen interprets `x-go-name` on a property's referenced schema as the FIELD name, collapsing distinct properties onto a single name. The right call site for `x-go-name` overrides is each ref, not the ref target — out of scope for this preprocessor.)
- **Codegen non-determinism from map iteration.** `for k, sub := range v` over maps iterates in non-deterministic order; when a spec references multiple common-types versions (Container Apps refs both v3 and v5 of types.json) the file processed first wins on shared definition names, and runs can differ. `sortedKeys()` everywhere fixed it; codegen output is now byte-identical across runs.
- **The preprocessor was rewriting parameters and headers.** First crack at the Blob spec landed `bad data in #/components/schemas/AccessTier (expecting ref to parameter object)`. Root cause: `promoteXMsEnumName` was treating every inline `x-ms-enum` the same regardless of whether it was a schema, a parameter, or a header. Top-level `parameters.AccessTierOptional` is a parameter object with an inline enum + `x-ms-enum.name = "AccessTier"`; the preprocessor rewrote it to `{$ref: "#/definitions/AccessTier"}`, which is a parameter pointing at a schema — invalid. Same trap with response headers (`x-ms-copy-status` with `x-ms-enum.name = "CopyStatusType"` matching `definitions.CopyStatus`). Fix: track "non-schema depth" through the walk; suppress promotion under `parameters`/`headers` containers but reset on `schema`/`items` so body-parameter schemas still get rewritten.
- **A parameter named the same as a definition.** Even with the preprocessor calmed down, oapi-codegen rejected Blob with `duplicate typename 'LeaseDuration'`. Blob declares both `definitions.LeaseDuration` (string enum "infinite"|"fixed") and `parameters.LeaseDuration` (integer header `x-ms-lease-duration`). The definition is the natural reuse target (many properties $ref it); renaming the schema would force every $ref to rewrite. The cleaner fix is stamping `x-go-name: LeaseDurationParameter` on the colliding parameter — localised to one node. `dedupeParameterDefNameCollisions` does that generically, so the next time some other Azure spec ships a similar collision the preprocessor handles it without per-spec babysitting.

## Phase 11 — tighten the wire boundary (CLOSED — PR #18, `phase-11`)

The big restructuring phase: replace hand-written wire layers with spec-driven generated stubs across every AWS-shaped frontend, and wire real signature verification (BUG-18) at the new decode boundary. Started with codex review correcting several wrong premises in the initial plan (SigV4 in `signer/v4` is signer-only not verifier; `golang.org/x/oauth2` is token-acquisition not JWT verification; Azure Key Vault is Bearer-only not SharedKey; the existing Smithy emitter was REST-XML-only — extending to awsJson / awsQuery / restJson1 is new emitter work, not a routing-table addition).

### Four wire-protocol emitter paths

Pre-Phase 11 the emitter at `internal/codegen/emit/` only spoke REST-XML (S3). Phase 11 added three more templates + protocol detection:

- **awsJson1_x** (`template_awsjson.tmpl`) — POST `/` dispatched by `X-Amz-Target` header; JSON request + response bodies; `__type` + `X-Amzn-Errortype` error envelope. Powers Secrets Manager (1_1) and SQS (1_0).
- **restJson1** (`template_restjson.tmpl`) — HTTP-route dispatched (method + URI template, same as REST-XML); JSON bodies + awsJson-shaped error envelope. Powers Lambda + APIGW v2.
- **awsQuery** (`template_awsquery.tmpl`) — POST `/` dispatched by `Action` form parameter; form-encoded request; XML response wrapped in `<OpResponse><OpResult>...</OpResult><ResponseMetadata>...</ResponseMetadata></OpResponse>`. Powers SNS, RDS, ElastiCache.

Each template lives alongside REST-XML and is selected by `pickTemplate()` based on the service shape's protocol trait. The emitter detects `aws.protocols#awsJson1_1` / `#awsJson1_0` / `#awsQuery` / `#restJson1` / `#restXml` and routes to the right template.

### Three runtime helper packages

- `internal/awsjson/` — Router (X-Amz-Target dispatch), BackendError, EpochTime (epoch-seconds timestamp serialisation; Go's default RFC3339 broke awsJson1_x SDK compat), QueryCompatibleCode (legacy SDK error-code header for SQS).
- `internal/awsquery/` — Router (Action dispatch), BackendError, WriteResult (OpResponse/OpResult/ResponseMetadata envelope), WithForm/FormFromContext (gives adapters access to raw form values for collections the template doesn't decode).
- The existing `internal/restxml/` is shared by REST-XML and restJson1 (only the body encoding differs — restJson1 emits JSON, REST-XML emits XML; both use the same router).

### Spec-driven AWS frontends — 8/8 migrated

| Service | Wire | Hand-written LOC deleted |
|---|---|---|
| storage / aws_s3 | REST-XML | (pre-Phase 11, already spec-driven) |
| secrets / aws_secretsmanager | awsJson1_1 | 865 |
| queue / aws_sqs | awsJson1_0 | 679 |
| pubsub / aws_sns | awsQuery | 615 |
| rdbms / aws_rds | awsQuery | 436 |
| cache / aws_elasticache | awsQuery | 275 |
| functions / aws_lambda | restJson1 | 493 |
| apigateway / aws_apigatewayv2 | restJson1 | 490 |
| **Total** | | **3853** |

Each migration follows the same pattern: write a per-service adapter implementing the generated `<Service>Backend` interface; the adapter translates each generated request type into the existing domain layer and back. Helpers (ARN forging, ID encoding, status mapping) carry verbatim from the hand-written wire.

### Signature verification — BUG-18 P1 → P3

Four verifier packages, each with HMAC-SHA256 test mode + per-cloud error envelope + Middleware() variant + unit-test coverage:

- `internal/sigv4verifier/` — re-uses `aws-sdk-go-v2/aws/signer/v4`'s canonical-request building blocks. Verify reconstructs the canonical request from the incoming HTTP request, re-signs with the looked-up secret, constant-time compares. Handles clock skew (±15 min default), credential-scope validation, body buffer-and-restore for downstream handlers.
- `internal/gcpbearer/` — HS256 JWT with iss/aud/exp/iat claims validation. Production RS256 JWKS path is a follow-on. Documents the opaque-OAuth2-access-token gap (those require a network round-trip to tokeninfo per request).
- `internal/azurebearer/` — HS256 JWT + Microsoft Entra-style claims. WithChallenge option emits the WWW-Authenticate header Key Vault's SDK requires to trigger its token-acquisition retry.
- `internal/azuresharedkey/` — HMAC-SHA256 over Azure Storage's canonical string (12-line header block + canonical x-ms-* headers + canonical resource path/query). Used by Blob; Key Vault uses Bearer, Service Bus uses SAS / Entra ID.

24/24 service-frontends are now wrapped: 5 SigV4 (AWS) + 8 GCP bearer + 8 Azure bearer + 3 Azure SharedKey (Storage) + 3 storage (one each for AWS/GCP/Azure). Bypass gated on `SHIMANISM_TEST_UNAUTHENTICATED=1` (harness `init()` sets it so existing conformance lanes that use `aws.AnonymousCredentials{}` / `option.WithoutAuthentication()` / `fakeAzureCred` keep passing during the conformance-lane rewrite). The bypass reads env on every call (no `sync.Once` caching) so per-test `t.Setenv` flips take effect.

The reject path is enforced end-to-end: 23 unit tests across the 4 verifier packages + 3 end-to-end SigV4 reject conformance tests in `services/secrets/conformance/aws_sigv4_test.go` prove the verifier rejects unsigned / wrong-key / tampered requests with the correct source-cloud error envelope. The positive-case `TestAWSSigV4_AcceptsSignedRequest` is deferred — needs header normalisation work (Content-Length set at sign-time vs. transport-time, httptest Host quirks).

### Surprises along the way

- **Smithy emitter is REST-XML-shaped under the hood.** Generated handlers `import restxml`, route from `smithy.api#http` operation traits, encode XML responses. Codex review correctly flagged the original plan's "extending to AWS surfaces beyond S3 is a routing-table addition" as wrong — `awsJson1_1`, `awsJson1_0`, `awsQuery`, `restJson1` are each new protocol serde at the emitter level. Phase 11 added them all.
- **awsJson1_x timestamps are epoch-seconds floats, not RFC3339.** Go's default `time.Time.MarshalJSON` emits RFC3339 strings; AWS SDKs reject them. New `awsjson.EpochTime` type with HMAC-aware MarshalJSON; emitter substitutes `*awsjson.EpochTime` for `*time.Time` when the protocol uses epoch-seconds.
- **awsQuery list element names differ per spec.** SNS uses `<member>` (the protocol default); RDS / ElastiCache use `<DBInstance>` / `<CacheCluster>` (via `@xmlName` traits). The emitter respects the trait when set; falls back to `<member>` for awsQuery, target-shape short-name for REST-XML.
- **awsQuery error envelope's outer element is awkward to emit.** `xml.Marshal(result)` produces `<ResultType>...</ResultType>` by default; wrapping that inside `<OpResult>` produced double-nested output the SDK couldn't parse. `WriteResult` now strips the result struct's own outer element so its fields inline cleanly inside the OpResult wrapper.
- **APIGateway v2 frontend carries per-process state for the multi-step deploy flow.** AWS splits gateway creation into Api + Routes + Integrations + Deployment; the domain has a single atomic DeployGateway. The adapter retains the pending-routes / integration-IDs map in per-process memory — explicitly documented as a known compromise of the stateless-shim rule (the deployed routing table itself still lives in the backend; only in-flight accumulation is per-process).
- **Lambda's required-Role makes cross-cloud Create-via-Lambda-SDK intersection-out.** The AWS Lambda SDK enforces Role as a required client-side field; non-AWS backends honestly reject non-empty Role. The matrix test for non-AWS cells now asserts the InvalidParameterValueException (negative conformance for the cross-cloud Role contract).
- **`http.Request.Form` is populated lazily.** `awsquery.Router` calls `r.ParseForm()` in ServeHTTP; generated per-op handlers then stash `r.Form` on the request context so adapters can retrieve it via `awsquery.FormFromContext(ctx)` for collection decoding the template doesn't emit (SNS MessageAttributes, etc).

### Phase 11.14 closer — BUG-18 closed end-to-end

The closer turned out to be substantially more than "flip a flag." Each cloud needed a specific fix to make end-to-end signed conformance work:

- **AWS-CLI vs. aws-sdk-go-v2 SigV4 divergence.** `aws-sdk-go-v2/aws/signer/v4`'s `Signer.SignHTTP` auto-includes `Content-Length` in the SignedHeaders list when `ContentLength > 0`; boto3 (the `aws` CLI's signer) does not. The verifier that re-used `v4.SignHTTP` produced a canonical request with one extra signed header vs. what the CLI actually signed, so CLI-driven conformance failed. Fix: implement SigV4 from scratch in `internal/sigv4verifier/canonical.go`. The verifier computes the canonical request using ONLY the `SignedHeaders` list the original client declared, with explicit special-cases for Host (from `r.Host`) and Content-Length (from `r.ContentLength` since `net/http` stores it out-of-band). This handles every signer uniformly because it follows the spec, not any one SDK's auto-detection. Same `canonical.go` also handles SigV4 presigned URLs — query-string signature, `UNSIGNED-PAYLOAD`, canonical query excludes `X-Amz-Signature` from itself.

- **GCP test JWT helper.** `internal/gcpbearer/testjwt.go` builds well-formed HS256 JWTs the verifier accepts; conformance tests assemble bearer tokens via `option.WithTokenSource(oauth2.StaticTokenSource{gcpbearer.TestJWT(...)})` per service audience. 17 GCP-shaped tests migrated from `option.WithoutAuthentication()` to signed tokens; gcloud CLI tests use `CLOUDSDK_AUTH_ACCESS_TOKEN=<jwt>`; Terraform tests thread the JWT into the `google` provider's `access_token`.

- **Azure test JWT + SharedKey helpers.** `internal/azurebearer/testjwt.go` symmetric to gcpbearer. Conformance tests use `azcore.TokenCredential` implementations that return a real signed JWT; raw-HTTP Azure Service Bus REST tests inject `Authorization: Bearer <jwt>` per request. Storage Blob tests switch from `NewClientWithNoCredential` to `NewSharedKeyCredential` with the verifier's trusted (account, key) pair base64-encoded. `azuresharedkey` verifier defect surfaced and fixed: was using `r.URL.Path` (URL-decoded by net/http) but the azblob SDK signs over `r.URL.EscapedPath()`; object keys with slashes produced spurious signature mismatches.

- **awsQuery map-shape XML marshal.** Go's `encoding/xml` doesn't natively serialise map types. The Smithy emitter generated `TopicAttributesMap = map[string]string` (and friends) tagged with the XML field name, but the runtime emitted them as empty elements. terraform-provider-aws's SNS importer parsed the empty `<Attributes/>` and concluded the topic didn't exist. Fix: emitter now generates a `MarshalXML` method per Smithy map shape that writes `<Field><entry><key>...</key><value>...</value></entry>...</Field>` in sorted-key order.

- **SNS GetTopicAttributes fidelity gaps.** hashicorp/aws's importer parses the `Policy` field via the AWS IAM-policy parser and aborts the import on empty / `{}`. Adapter now emits the canonical SNS default-policy JSON document (Version 2008-10-17, `__default_policy_ID`, every default Allow action). Separately, terraform-provider-aws's `aws_sns_topic` Create flow unconditionally calls `SetTopicAttributes` for every feedback-sample-rate / feedback-role-ARN / KMS / fifo / tracing attribute, even when HCL doesn't declare them. Returning `InvalidParameter` for these blocked every `aws_sns_topic` apply. Adapter now no-ops these AWS-only attributes via an explicit `awsOnlySNSAttribute` allowlist; attributes that would change cross-cloud state (DisplayName, custom Policy) still reject non-default values.

Bypass dropped from harness `init()` — no per-cloud bypass env var is set anymore. Every conformance test signs end-to-end with verification enforced. BUG-18 marked Closed.

### Phase 11.4 — Azure Key Vault oapi-codegen pilot

The Azure spec-driven lane lands as a pilot, proving the toolchain end-to-end without rewriting every Azure frontend in this PR.

Azure publishes data-plane specs as Swagger 2.0 (OpenAPI v2) today; the upstream v3 cutover is still in progress. The path that works:

1. Vendor the spec (`services/secrets/spec/azure-keyvault-secrets.json`, `Azure/azure-rest-api-specs` commit `9473ef10`, MIT).
2. `cmd/azure-codegen` (new driver) converts v2 → v3 in memory via `kin-openapi/openapi2conv.ToV3`.
3. Calls `oapi-codegen.Generate(v3, …)` as a library to emit Go types + std-net-http `ServerInterface`.

Two upstream-tooling defects surfaced during the pilot and got worked around in `cmd/azure-codegen` itself:

- **`kin-openapi` attaches empty `AllOf: []` to scalar enum schemas during v2→v3 conversion** (e.g. Key Vault's `DeletionRecoveryLevel`). `oapi-codegen.MergeSchemas` then panics with out-of-range `allOf[0]`. `normalizeAllOf` walks every schema in the converted spec and nil-replaces empty `AllOf` slices before generation.
- **`kin-openapi` preserves global host-template parameters as operation-level `$ref`s without resolving them** (Azure's `vaultBaseUrl` pattern). Not a blocker — the resulting refs point at well-formed component parameters and oapi-codegen handles them; documented in `cmd/azure-codegen` so a future reader doesn't relitigate.

Pilot proof-point: `internal/secrets/frontends/azure_keyvault/server.go`'s `setSecret` handler now decodes via `gen.SecretSetParameters` (spec-driven) instead of the hand-written `setSecretRequest`. Full secrets conformance (AWS + GCP + Azure SDK / CLI / Terraform / cross-cloud / matrix) passes with the pilot in place. The remaining handlers stay on hand-written wire types — the pilot's job was to prove the toolchain produces SDK-wire-compatible types, not to delete the existing frontend wholesale. `make codegen` now runs an `azure-codegen` sub-loop after the Smithy loop; `services/secrets/azure-codegen.json` is the manifest (parallel to the AWS `codegen.json`).

### Track 2.B — GCP routing emitter

New `cmd/gcp-codegen` driver reads a Google API Discovery JSON document and emits `Routes []Route` — `(HTTPMethod, URIPattern, OperationID)` triples that downstream frontends compile to their dispatch flavour of choice. Per AGENTS.md decision #11 the emitter is *routing-only*; wire types reuse `google.golang.org/api/<svc>/v1` (the same Discovery-generated source the SDK uses, so re-emitting types would duplicate them).

Vendored Discovery JSON + generated route inventories for all 8 GCP services in one chunk:

| Service | Discovery host | Routes |
|---|---|---|
| storage | `storage.googleapis.com` v1 | 108 |
| secrets | `secretmanager.googleapis.com` v1 | 32 |
| queue / pubsub | `pubsub.googleapis.com` v1 | 72 each |
| rdbms | `sqladmin.googleapis.com` v1 | 100 |
| cache | `redis.googleapis.com` v1 | 71 |
| functions | `run.googleapis.com` v2 | 84 |
| apigateway | `apigateway.googleapis.com` v1 | 56 |

`make codegen` now runs three pipelines in series: AWS Smithy → Azure (oapi-codegen library) → GCP (Discovery routing).

Adapter migrations that swap the existing GCP frontends' hand-written regex dispatch for the generated `gen.gcp.Routes` inventory are mechanical follow-on work — the existing frontends keep passing conformance, so the migration is dispatch-consistency + spec-drift detection, not fidelity.

First concrete consumer of the inventory: `TestGCPRoutes_*` in `services/secrets/conformance/gcp_routes_test.go`. Asserts that `gen.gcp.Routes` is non-empty + sorted + covers the cross-cloud secrets-intersection operations (`secretmanager.projects.secrets.{create,get,delete,list,addVersion,versions.access,versions.list}`). A rename or removal upstream surfaces as a test failure on the next regeneration. Same pattern applies to every other service once its cross-cloud intersection op-IDs are codified.

### Phase 11 deferrals → Phase 12 follow-on tracks

All three deferrals are absorbed into [Phase 12](PLAN.md#phase-12--cross-cloud-migration-cell-expansion--phase-11-follow-ons) Track 2 so the wire-boundary work stays in one continuous arc:

- **12.A — Broader Azure spec-driven migration.** Pilot covers `SetSecret` only; migrating the rest of `azure_keyvault` + the other 7 Azure frontends to the generated `ServerInterface` uses the same `cmd/azure-codegen` pipeline.
- **12.B — GCP routing emitter** + 8 GCP adapter migrations. Hand-written GCP frontends already use Discovery-generated wire types; the emitter adds dispatch consistency + spec-drift detection.
- **12.C — Production RS256 JWKS** for real Google / Microsoft Entra tokens. Test mode is HS256 with a static shared key; the verifier comments document the production code path (`google.golang.org/api/idtoken.Validate`, Microsoft's JWKS).

Track-A continuations (real-cloud comparison required, not Phase 12-bound): BUG-15 (queue/gcp retention drift), BUG-8 (apigateway/gcp-tf), real-cloud signature verification.

## Phase 10 — cross-cloud `terraform apply` through the shim (PR #17, merged 2026-05-21 at `ebc30f7`)

The write-side proof, symmetric to Phase 9's read-side. A user writes AWS-shape Terraform; `terraform apply` creates / updates / destroys the resource in cloud B through shimanism, with the source-cloud provider unaware of the translation. Eight BUGs closed; all 8 services have active drift assertions; full developer + contributing docs under `docs/`.

### What closing BUG-2 actually required

BUG-2 (queue `SetQueueAttributes`) had carried through 5 phases. The cycle of failed closes followed a recurring shape: someone added `SetQueueAttributes` to a backend, the provider's `WaitForStateEqual` after CreateQueue still timed out, the work got shelved. The Phase 10 close took 4 distinct moves:

1. **Domain extension.** `domain.Queues` gained `SetQueueAttributes(name, QueueAttributes)`. Zero-valued fields = "leave unchanged" (AWS-merge semantics; same shape as `UpdateSecretOptions` from BUG-17).
2. **Per-backend honest implementations.** inmem patches in place; AWS calls `SetQueueAttributes`; GCP `subscriptions.patch` (only honors ackDeadline + retention, others ignored as documented); Azure `GetQueue → UpdateQueue` read-modify-write; NATS `UpdateStream` + `UpdateConsumer`.
3. **Read-side attribute surface.** `attributesToAWS` extended to emit all the AWS-specific attribute keys the hashicorp/aws provider sets schema-defaults for (`Policy`, `RedrivePolicy`, `KmsMasterKeyId`, `FifoQueue`, etc). Empty/zero values for out-of-intersection attributes — honest defaults representing "no extra features configured," not fabricated state.
4. **awsQueryCompatible legacy error codes.** The `x-amzn-query-error` header maps the new Smithy error codes to their legacy Query-XML equivalents (notably `AWS.SimpleQueueService.NonExistentQueue` for `QueueDoesNotExist`). hashicorp/aws's wait functions are keyed on the legacy codes; without the header they'd treat a delete-confirmation 404 as an unrecoverable error.

The same shape — domain extension + per-backend impl + read-side surface + error-code compatibility — applied to BUG-13 (functions Role/Publish), BUG-16 (rdbms paths/defaults), and BUG-17 (secrets UpdateSecret/TagResource). Phase 10.3 is the methodical application of this template across the open Apply-blocking BUGs.

### Cross-cloud asymmetries: documented, not faked

Phase 10.7's storage cell (`TestCrossCloudApply_Roundtrip_StorageAWStoGCS`) proves the cross-cloud Apply headline. The other six services document specific cross-cloud asymmetries that make a single-PR close infeasible:

- **secrets AWS→Azure:** AWS Secrets Manager's CreateSecret accepts value-less creates; Azure Key Vault genuinely doesn't (SetSecret is the only create path and requires Value). The provider's separate `aws_secretsmanager_secret` + `aws_secretsmanager_secret_version` resources mean the user can't seed a value at create time through HCL.
- **queue/pubsub AWS→GCP:** hashicorp/aws's `WaitForStateEqual` after CreateQueue + SetQueueAttributes expects all SQS-shape attributes to round-trip exactly. GCP Pub/Sub honors visibility_timeout_seconds + message_retention_seconds; DelaySeconds + MaxMessageSize don't have GCP analogs.
- **cache/rdbms/functions/apigateway:** AWS-shape Apply requires post-create reconcile state (parameter-groups, subnet-groups, LayerVersions, multi-step Create) that GCP's equivalent services don't surface.

Each is honest cross-cloud behavior — not a shim bug. The destinations genuinely don't have the source's concepts. Real migration tools handle these via fixture-side workarounds + identity/networking rebinding on the destination; that's the Track A follow-on.

### Codex review pass

Two codex reviews ran against the PR — one on the docs, one on the code. Five P2 silent-fallback / consistency fixes landed in response (SNS SetTopicAttributes value-aware reject, GCP queue SetQueueAttributes honest-reject for unsupported attrs, CreateQueue tags compensating-delete rollback on tag failure, AWS secret UntagResource diff-based reconciliation, Lambda Role/Publish honest-reject on non-AWS backends + matching matrix test fixture update). The honest-reject of Role on non-AWS backends then forced an alignment of the Knative conformance test — `TestInvokeConnectivity_Knative` now creates the function via the backend directly (AWS Lambda SDK's required-Role contract is intersection-out for non-AWS backends).

### Closing the philosophical loop

Phase 10 makes shimanism a **cross-cloud IaC control-plane migration tool** — not a full migration tool. Data movement, secret value-history transfer, DB snapshots/replication, cache warmup, queued-message drain, function artifact transfer, IAM rebinding, DNS swap — those remain user responsibilities (or other tools' jobs). `docs/migration.md` documents this scope explicitly.

## Phase 10.1 — close BUG-5 (stateless GCP `Operations.Get`)

Carried since Phase 5. Codex's Phase 10 review flagged it as the hard gate for Phase 10 — `terraform apply` against GCP-shape frontends hangs without long-running-operation polling, so it has to land *before* any Apply cell runs. PR #16 closed it across all four GCP-shape frontends in one sweep: rdbms (Cloud SQL Admin) `/v1/projects/{p}/operations/{op}`, cache (Memorystore) and apigateway (API Gateway) `/v1/projects/{p}/locations/{l}/operations/{op}`, functions (Cloud Run) `/v2/projects/{p}/locations/{l}/operations/{op}`.

**Stateless polling via Name-encoded target.** The Operation `Name` encodes `(opType, target)`. A polling client GETs the operation; the shim parses the name, looks up the underlying resource, and maps its current `domain.Status` to RUNNING / DONE. For delete ops, `NoSuchResource` signals DONE. **No shim-side operation table** — every poll re-derives status from the backend's actual state. `Operations.List` returns empty: there's no honest way to enumerate past operations without state, and SDK polling paths only call `Get`. Documented as intentional, not a gap.

## Phase 9 — cross-cloud `terraform import` (PR #13 + PR #16)

The read-side proof. The thesis: if the shim is honest, then `terraform import` against an A-shaped HCL pointing at a backend cloud B should round-trip — `terraform plan` after import sees no drift, because the shim translates the B-side state back into the A-side shape with full fidelity.

### `shimctl env` and the endpoint-override registry

Migration users don't write endpoint-override boilerplate by hand. Sub-phase 9.1 added `shimctl env`, which prints the env-var / SDK / CLI / Terraform overrides needed to route a given (cloud, service) pair through the shim. The registry lives at `internal/clientconfig/overrides.yaml` and enumerates the per-cloud override knobs the official tooling actually exposes. This is what makes the migration story runnable from a README.

### The per-service `INTERSECTION.md` audits

Every wire-level operation each frontend serves got classified into one of three categories: (1) real work — must dispatch to a real backend call; (2) feature genuinely unset — returns the cloud's real "unset" envelope (e.g. `NOT_FOUND` for an absent sub-resource); (3) out of intersection — returns the cloud's real "not supported" envelope. A fourth implicit category — "returns something plausible without doing real work" — is by definition a fake and got filed as a bug or removed.

This audit surfaced **three real fidelity gaps that had been hiding under matrix-test passes**: GCP API Gateway frontend missing the `Apis` + `ApiConfigs` endpoint families entirely (BUG-9), Azure APIM frontend missing the `Operations` subresource (BUG-10), and AWS APIGW v2 frontend's 404 envelope missing the `__type` field (BUG-11). All three got fixed in Phase 9.

### Six real fidelity fixes surfaced by the import tests

Phase 9.5 wasn't just test-writing — every service's import driver found something: XML double-nesting in restxml responses (AWS frontend marshalling); missing Policy JSON sub-resource; missing tag-list handlers (category-2 honest-empty); missing selection-expression defaults (apigateway); missing Lambda subresources; missing RDS ARN. Each got filed as a bug and fixed inline. **No fakes survived** — the import path doesn't pass until every Read the provider issues has an honest answer.

### Docs roll-up lesson

PR #13 squash-merged with all 8 services' cross-cloud import tests on tree, but the closer commit that updated phase docs from "six services" to "all 8 services" was still in flight on the branch and didn't make the squash. PR #16 fixed the doc narrative drift. **Lesson: docs-roll-up commits at the end of a multi-chunk PR are race-prone with the merge fire.** For Phase 10 onward, doc updates happen *with* each granular commit, not as a single tail.

## Phase 8 — API Gateway (PR #13 co-merged)

Control-plane shim for HTTP API gateways. Same shape as Phases 5–7 — provision + return URL, clients HTTP-request the URL — with one new wrinkle: a *set of routes* to translate to backend-native primitives that vary wildly across clouds.

- **Declarative-replace via `DeployGateway(routes)`.** AWS lets you mutate individual routes; GCP atomically deploys an OpenAPI document; Azure replaces APIM operations one at a time; Envoy Gateway swaps the HTTPRoute set. Cross-cloud "patch one route" is impossible. The intersection is **publish a full routing table atomically**.
- **restJson1 with @jsonName traits.** The Smithy spec explicitly declares @jsonName camelCase traits on every field. The aws-sdk-go-v2 client silently drops fields whose JSON tag doesn't match — no error, fields go nil. Phase 7's Lambda spec lacked these overrides, which is why PascalCase tags worked there; Phase 8 forced the issue.
- **Envoy Gateway via dynamic client.** `gateway.networking.k8s.io/v1` via dynamic client + unstructured CRs (`Gateway` + `HTTPRoute`). Each shim Gateway maps to one Envoy Gateway CR plus N HTTPRoutes labeled `shim.apigateway/gateway=<name>` so DeployGateway can atomically wipe-and-replace.
- **Exit criterion: `TestRouteServes_Envoy`.** Stands up an echo Deployment + Service, registers Gateway + Integration + Route via the AWS frontend, then HTTP-GETs the route through Envoy's Service via port-forward. End-to-end chain has no fakes; if any link breaks, the request fails.

## Phase 7 — Functions (PR #12)

Control-plane shim for container-image function deployments. Data plane is HTTP — simpler to test than PG wire protocol or RESP, but with one twist: the URL has to actually route to the deployed container, end-to-end.

- **Container image only.** ZIP-package Lambda is out of intersection. All four backends natively support container images; ZIP is AWS-specific. Cross-cloud function deployment via the shim means shipping a registry image, not a source bundle.
- **restJson1 — the fourth AWS wire protocol family in the shim.** Phases 1+2 used S3 XML / awsJson1_1. Phase 3 used awsJson1_0 (SQS). Phases 4+5+6 used awsQuery (SNS, RDS, ElastiCache). Phase 7's Lambda is **restJson1**: real REST routes with JSON bodies.
- **Events + auth-on-invoke deferred.** Cross-cloud event-source mappings (CloudWatch / EventBridge / Eventarc / Pub/Sub triggers / Event Grid) have completely different shapes; HTTP-trigger only. IAM-gated invocation deferred for the same reason.
- **Knative URL routing nuance.** The exit-criterion test (`TestInvokeConnectivity_Knative`) hits a Knative-deployed container through `kubectl port-forward svc/<name>` plus a `req.Host = <ksvc URL host>` header — bypasses the gateway's Host-based dispatch entirely. Endpoint URLs across backends: Knative `Service.status.url`; Cloud Run `Service.uri`; Container Apps `Ingress.Fqdn`; AWS Lambda emits an `aws-lambda://<arn>` placeholder (Lambda Function URLs require a separate `CreateFunctionUrlConfig` op — out of intersection).

## Phase 6 — Managed Redis (PR #11)

Near-mirror of Phase 5 — control plane only; K8s peer is Redis Operator (OT-CONTAINER-KIT) instead of CloudNativePG; exit criterion is `redis-cli PING → PONG` through the shim-returned Connection block. Mostly mechanical re-application of the Phase 5 architecture.

- **Smaller intersection.** 11-op rdbms collapses to 6 ops for cache (Create/Delete/Describe/List/Modify/Reboot Instance). Snapshot/restore deferred — cross-cloud Redis snapshot semantics are too divergent (AWS → S3, GCP → GCS export, Azure → backup containers, Redis Operator → BackupRestore CRs).
- **awsQuery thrice in a row.** ElastiCache, RDS, and SNS all use awsQuery — by the third instance, a new awsQuery frontend is essentially dispatch + struct shapes. Envelope plumbing carried over verbatim from Phase 4 + Phase 5.
- **GCP Memorystore auth differs from RDBMS.** AUTH is fetched via a separate `GetAuthString` endpoint; the shim deliberately doesn't call that on every Describe to keep the op cheap. Auth surfaces exclusively at create time, matching AWS.

## Phase 5 — Managed RDBMS (PR #10)

Control-plane only — the load-bearing shape change versus Phases 1-4.

- **The shim is invisible to every SQL statement.** Storage / Secrets / Queue / Pubsub all sit on the data path: every wire-protocol message goes through the shim. RDBMS doesn't. The shim provisions a DB instance via the cloud's control-plane API and returns a `Connection` block (host, port, master username, database name). Clients open a *direct* connection. **Exit criterion: `psql` opens a real connection to the cnpg-provisioned cluster through the shim-returned Connection block and runs `SELECT 1`.** It either works or it doesn't — no "the shim faked it" is possible.
- **Async semantics, surfaced explicitly.** DB provisioning takes minutes — the shim can't pretend to be synchronous. Explicit `Status` enum (`Creating`, `Available`, `Modifying`, `Rebooting`, `Deleting`) on every domain `Instance`; clients poll `DescribeInstance` until `Available`.
- **CloudNativePG via dynamic client (not the cnpg-api Go module).** Loose dependency on cnpg's release cadence; the trade-off is hand-coded field paths into unstructured maps. Same pattern repeated for Redis Operator (Phase 6), Knative (Phase 7), Envoy Gateway (Phase 8).
- **Master password handling.** Returned exactly once at `CreateInstance` via `CreateInstanceResult.MasterPassword`; never re-emitted on Describe. cnpg stores the password in a Kubernetes `Secret`; the shim re-reads that Secret on each `DescribeInstance` (no shim-side credential cache).
- **Filed BUG-5.** GCP Cloud SQL Admin frontend returned `PENDING` `Operation` envelopes but didn't implement the polling endpoint. Carried until Phase 10.1's stateless-`Operations.Get` fix.

## Phase 4 — Pub/Sub (PR #9)

Topic-fanout sibling of Phase 3.

- **Topic ≠ Subscription is the load-bearing change.** Phase 3's queue domain collapsed (topic, subscription) onto one Queue. Phase 4 can't: fanout needs subscriptions addressable independently (each has its own ack-deadline + delivery queue). Pubsub domain has two resource types and 12 ops. Receive is per-Subscription; Publish is per-Topic; there's no per-Topic Receive.
- **AWS dual-protocol surface.** Real AWS pub/sub is SNS for publish, SQS for receive — SNS subscriptions deliver to SQS queues. The shim's AWS frontend mirrors this: an SNS handler (awsQuery, XML) for publish, and a *slim* SQS-shaped receive frontend (no CreateQueue, no SendMessage — those don't belong on a fanout-only data plane). The `StartPubsubServerAWS` harness returns two URLs.
- **NATS JetStream throughout, not core.** JetStream streams with `InterestPolicy` retention; durable pull consumers. AWS / GCP / Azure subscriptions are *always* durable — toggling NATS to non-durable just for one knob would diverge the K8s peer from the cloud surfaces.
- **Azure's 4-part receipt handle.** `<topic>|<sub>|<messageID>|<lockToken>` so Ack + RenewLock can reconstruct the URL `/{topic}/Subscriptions/{sub}/messages/{id}/{lock}` with no shim-side state.

## Phase 3 — Queue (PR #8)

Three frontends × five backends × three driver types applied to message queueing. 8-op intersection.

- **Receipt handles are the hard part.** Each cloud emits a different opaque token; the shim is stateless, so the handle has to round-trip without a shim-side index. AWS → passes through unchanged; GCP `AckId` → passes through unchanged; NATS → receipt = reply subject (ack via `+ACK` to that subject); Azure → composite `<messageID>|<lockToken>` reconstructing the URL on ack.
- **Hybrid SDK + REST for Azure.** `azure-sdk-for-go/sdk/messaging/azservicebus` high-level receive returns a `*azservicebus.ReceivedMessage` Go object the caller must hold to call `CompleteMessage(msg)` — violates statelessness. Fix: SDK for stateless ops (Create / Delete / List / Send / Receive); raw HTTP REST + SAS-token signing for Complete + RenewLock, reconstructing the URL from the receipt handle alone.
- **GCP: `google.golang.org/api/pubsub/v1` over the streaming-first `cloud.google.com/go/pubsub`.** The high-level streaming client opens a long-lived gRPC stream and dispatches via callbacks — doesn't fit per-call REST. Discovery-generated synchronous REST SDK matches the shim's request-response shape exactly. Same package supplies wire types for the frontend too.
- **AMQP / ARM cells deferred via documented skip.** Azure SDK drives Service Bus over AMQP; the shim's Azure frontend speaks REST only. `az servicebus` CLI + Terraform `azurerm` are AMQP / ARM, skipped with rationale; raw-HTTP REST is the conformance contract.
- **Cross-cloud caps for uniformity.** VisibilityTimeoutSeconds capped at 600s (GCP max); WaitTimeSeconds capped at 20s (AWS max). Higher values would silently fail on the lower-cap backend.
- **Filed BUG-2** (SetQueueAttributes gap). Carried 5 phases; closed in Phase 10.3.

## Phase 2 — Secrets management (PR #7)

Same N × N discipline as Phase 1, smaller surface (7-op intersection). Three frontends (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × five backends (inmem, Vault as K8s peer, AWS / GCP / Azure passthrough) × three driver types.

- **The stateless invariant landed first.** Before any code: **the shim binary holds no state of record**. Locked into AGENTS.md, PLAN.md decision #12, STATUS.md invariants, plus a PHILOSOPHY.md koan (*The Empty Hands*). The Phase 2 design then bent to comply: version-handle translation (AWS UUID ↔ monotonic uint64, Azure GUID ↔ monotonic uint64) is **derived per-request by listing versions and sorting by creation timestamp**. Earlier drafts stored the mapping in a shim-owned sidecar; the rewrite cuts that — the data already lives in the backend, the shim just re-reads.
- **Versions modeled monotonically in the domain.** AWS UUID + stage labels (`AWSCURRENT`, `AWSPREVIOUS`) → resolved inside the AWS frontend via listing. GCP / Vault → 1:1 native. Azure GUID → monotonic derived per-request.
- **Conformance surprises.** AWS ARN normaliser bug (stripped `-XXXXXX` 7-char suffix from any ARN; broke Terraform test secrets with `-`-separated names — fix: only strip on real-AWS-region ARNs, never shim-issued). `GetResourcePolicy` probe handler added (TF refreshes call it on every DescribeSecret; resource policies are IAM-side, out of intersection — probe returns canonical "no policy attached"). GCP's `:enable` / `:disable` treated as no-op probes (no per-version enabled state in the cross-cloud intersection). Azure SDK forced `httptest.NewTLSServer` + 401 + WWW-Authenticate challenge issuance — bearer tokens refused over plain HTTP. `az keyvault secret` data-plane URL is hard-coded to `*.vault.azure.net`; CLI cell skipped with rationale.

## Phase 1 — Object storage (PR #6)

Phase 1 was the foundation phase — codegen pipeline, conformance harness, CI matrix all built alongside the first real user. Largest API surface across all 8 services; richest auth + content semantics.

- **Three spec-format ingestion pipelines.** AWS Smithy → custom Go server-stub emitter at `internal/codegen/` (no official Smithy → Go server generator exists; `smithy-go` is client-side). GCP Discovery + Azure OpenAPI v3 were planned-then-deferred to Phase 11 (only AWS Smithy is spec-driven today).
- **Streaming throughout.** `httpPayload` blob members are `io.Reader` (in) / `io.ReadCloser` (out); object bodies never buffer through the shim. Carried into every subsequent phase as architectural baseline.
- **Neutral domain interface.** `internal/storage/domain/` between wire codec and cloud-specific backend. Every backend (MinIO, AWS S3 passthrough, GCS, Azure Blob) implements `domain.Storage` directly. Frontends adapt wire-protocol shapes to the domain.
- **Cross-cloud CopyObject.** Azure's CopyBlob is asynchronous (returns a copy ID; clients poll). The shim's CopyObject backend translates this to a synchronous-looking call by polling the destination — but fails loud if the poll loop exceeds the deadline rather than silently returning a stale state. Same posture every subsequent async-op phase carries forward.
- **Multipart ETag parity.** Each cloud computes multipart object ETag differently (S3 multipart is `MD5(parts) + "-N"`; GCS is composite-object hash; Azure differs again). `domain.MultipartETag` is a typed string that backends produce and frontends round-trip without reinterpreting.
- **BUG-1 (router `ForbiddenQueries`).** Required-only route matching meant `GET /{Bucket}/{Key+}?tagging=` silently fell through to GetObject. Fix: `restxml.RouteOptions.ForbiddenQueries` rejects routes when any named query is present; codegen emits the S3 feature-query list. GetObjectTagging + GetObjectAcl added as object-level probes (canonical "no tags" / "default ACL") so the TF AWS provider's aws_s3_object Read step gets a faithful response.

## Pre-phase — Repo bootstrap + continuity docs (PR #1, PR #2; merged 2026-05-18)

Repo created with the branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce-style definitions. AGENTS.md (CLAUDE.md is a symlink) as the operational rules. Continuity files (STATUS, DO_NEXT, PLAN, WHAT_WE_DID, BUGS) and Phase-0 CI checks (branch-rebased, symlinks-resolve, continuity-docs-present) wired into the main-branch ruleset as required status checks.

The continuity-file design is what makes coding-agent sessions survive context compaction: a fresh session reading STATUS + DO_NEXT + AGENTS should be productive without re-deriving anything from older conversations. PLAN.md is the single source of truth for the roadmap; per-phase planning lives inline as a section in PLAN.md, not in separate `PHASE_X_PLAN.md` files.

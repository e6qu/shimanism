# shimanism — What We Did

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md).

> Reverse chronological. One section per phase. The *why*, the surprises, the root causes — not per-PR detail. For commit-level history, `git log`. For per-bug detail, [BUGS.md](BUGS.md). For pipeline + verifier architecture, [doc/CODEGEN.md](doc/CODEGEN.md) + [doc/VERIFIERS.md](doc/VERIFIERS.md).

## Phase 14 — In flight (`phase-14` branch)

Branched from `main` at `3cf9e13` (PR #20 merged) on 2026-05-24. The branch already carries 14.A landed + 14.D fidelity audit done.

**14.B/14.D current state after sockerless PR #219.** The upstream simulator audit loop is clear. After the first Phase 14 commits, the user merged additional sockerless fix PRs (#200, #202, #211, #216, #219). Each time, the lane was rebuilt locally and re-probed; gaps were reopened or filed with full reproductions when fixes were partial. The current state on 2026-05-25:

- `/tmp/sockerless` is at `06ee3a5` (sockerless PR #219, merged 2026-05-25).
- [sockerless#218](https://github.com/e6qu/sockerless/issues/218) is closed; no upstream sockerless blocker is open at this checkpoint.
- `make sockerless-storage` passes all 10 current shim lanes: storage AWS S3 / GCS / Azure Blob; secrets AWS Secrets Manager / GCP Secret Manager / Azure Key Vault; queue AWS SQS / GCP Pub/Sub queue; pubsub GCP Pub/Sub; apigateway GCP API Gateway.
- BUG-8 is narrowed to the hashicorp/google API Gateway Terraform leg; the GCP APIGW backend/SDK leg is green.
- BUG-15 is narrowed to the hashicorp/google Terraform state-drift question; the GCP queue backend retention PATCH/read round-trip is green.

The extra sockerless issues surfaced after the original round-3 commit were: #193-199, #201, #203-210, #213-215, and #218. The important lesson was the same as the earlier audit: a green simulator PR still needs post-merge probes because several fixes were partial on first landing (#190, #193, #196, #209, #210). PR #216 closed the last five audit items (#209, #210, #213, #214, #215); PR #219 closed the GCP Secret Manager lifecycle gap (#218).

**GCP Secret Manager lane added after upstream fix.** The next planned 14.B lane was `services/secrets/backends/gcp` against sockerless using the official `cloud.google.com/go/secretmanager/apiv1` REST client. The first probe found sockerless supports create/add/access but missed:

- `GET /v1/projects/{project}/secrets/{secret}/versions` (`ListSecretVersions`) — backend `ListVersions` returns `NoSuchSecret`.
- `PATCH /v1/projects/{project}/secrets/{secret}?updateMask=labels` (`UpdateSecret`).
- `DELETE /v1/projects/{project}/secrets/{secret}` (`DeleteSecret`).

Filed [e6qu/sockerless#218](https://github.com/e6qu/sockerless/issues/218) with curl reproduction and expected REST contracts before adding any shim test. After the user merged sockerless PR #219, rebuilt the GCP sim and added `TestSockerless_GCPSecretManager_RoundTrip`, covering CreateSecret, PutSecretValue, HeadSecret, GetSecretValue(latest + explicit version), ListVersions, ListSecrets, UpdateSecret, and DeleteSecret. No local simulator patch or shim workaround is carried.

**14.A — sockerless round-1 fixes landed.** While Phase 14's continuity docs landed on PR #20, the user shepherded sockerless PR #179 (their "Phase 173" umbrella) closing all six of our round-1 issues (#173 S3 prefix, #174 aws-chunked envelope, #175 missing ListSecretVersionIds, #176/#177/#178 missing AWS/GCP/Azure services). With the simulators rebuilt:

- Dropped the `/s3` URL workaround from `scripts/run-sockerless-storage.sh` + the test-file comment.
- Renamed `TestSockerless_AWS_BucketLifecycle` → `TestSockerless_AWS_S3RoundTrip` and added `PutObject` (non-seekable body, exercises aws-chunked) + `HeadObject` + `GetObject` with full body equality.
- Re-enabled `HeadSecret` + `GetSecretValue` assertions in `TestSockerless_AWSSecretsManager_RoundTrip` (previously skipped on the missing ListSecretVersionIds path).

`make sockerless-storage` now passes three lanes: AWS S3 full round-trip, GCS full round-trip, AWS Secrets Manager full round-trip.

**14.D — fidelity audit done.** With sockerless's now-larger surface area, ran SDK-shaped probes across every newly added service. Eight fidelity gaps surfaced and were filed (no shim references; each issue carries its own self-contained reproduction):

- **[#181](https://github.com/e6qu/sockerless/issues/181)** Azure Cache for Redis ARM route only matches capital `Redis` — lowercase (which the SDK + azurerm provider use) returns 404. Pure case-sensitivity miss; ARM is supposed to be case-insensitive.
- **[#182](https://github.com/e6qu/sockerless/issues/182)** GCP Pub/Sub `Subscription` create + get drop 5 of 7 fields on response (`messageRetentionDuration`, `retainAckedMessages`, `expirationPolicy`, `enableMessageOrdering`, `filter`). **This is the same drift shape as BUG-15.** Closing #182 likely closes BUG-15 against sockerless without real-cloud Track A.
- **[#183](https://github.com/e6qu/sockerless/issues/183)** GCP Secret Manager `ListSecrets` returns GCS-shaped 404. Root cause is a routing leak: any unhandled `GET /v1/{...}` request falls through to the GCS handler which interprets the path as `{bucket=v1}/{object=...}`. The same shape also breaks `/v1/operations` (noted in a comment on the issue).
- **[#184](https://github.com/e6qu/sockerless/issues/184)** Azure Key Vault response `id` and `kid` URLs have a duplicated host segment + `http://` scheme: `http://kv.vault.kv.vault.azure.net/...`. Real Key Vault uses `https://{vault}.vault.azure.net/...`.
- **[#185](https://github.com/e6qu/sockerless/issues/185)** Azure Key Vault key creation returns a placeholder modulus literal `"n":"sim-generated-modulus"` instead of a base64url-encoded RSA modulus. Breaks any JWKS / signature-verification integration test against the sim.
- **[#186](https://github.com/e6qu/sockerless/issues/186)** AWS SQS `CreateQueue` accepts user-set queue attributes but `GetQueueAttributes` echoes only `VisibilityTimeout` — `MessageRetentionPeriod`, `DelaySeconds`, etc. are silently dropped. Same shape as #182, different protocol.
- **[#187](https://github.com/e6qu/sockerless/issues/187)** GCP Cloud SQL `selfLink` is a relative URL (`/v1/projects/.../instances/...`). Real GCP returns `https://sqladmin.googleapis.com/v1/...`.
- **[#188](https://github.com/e6qu/sockerless/issues/188)** GCP Secret Manager `versions/latest:access` echoes the literal alias `latest` in the response `name` instead of resolving to the concrete version number. Version-tracking flows break.

14.B's current validation lane is green after the later sockerless fixes. 14.C remains pending; additional 14.B service lanes are optional follow-on work.

**14.B initial lane expansion (post sockerless PR #180).** With the round-2 fidelity gaps closed in sockerless PR #180, 4 new shim lanes landed on top of the round-1 set:

- **GCP Pub/Sub pubsub** — `services/pubsub/conformance/sockerless_test.go::TestSockerless_GCP_Pubsub_RoundTrip`. Topic + Subscription + Publish + Receive + Ack against the shim's `pubsub/backends/gcp`.
- **GCP Pub/Sub queue** — now `services/queue/conformance/sockerless_test.go::TestSockerless_GCP_Queue_RetentionRoundTrip`. The final form includes CreateQueue → SetQueueAttributes → HeadQueue and asserts `MessageRetentionSeconds = 604800`.
- **AWS SQS queue** — same file `::TestSockerless_AWS_Queue_AttributeRoundTrip`. Asserts `VisibilityTimeout` + `MessageRetentionPeriod` round-trip via `CreateQueue` Attributes → `HeadQueue`.
- **GCP API Gateway** — `services/apigateway/conformance/sockerless_test.go::TestSockerless_GCP_APIGateway_CRUD`. Exercises `CreateGateway` (with routes → triggers `DeployGateway` → Api + ApiConfig + Gateway materialize) → `DescribeGateway` → `ListGateways` → `DeleteGateway`. **The SDK leg of BUG-8 is now cleared** — the TF-provider angle remains Phase 14.D residual.

**Round-3 audit, 3 more sockerless issues filed** (all closed later):

- **[e6qu/sockerless#189](https://github.com/e6qu/sockerless/issues/189)** — GCP Pub/Sub `projects.subscriptions.patch` returns 404 (only PUT is wired). Blocks shim's `SetQueueAttributes` and the BUG-15 retention round-trip.
- **[e6qu/sockerless#190](https://github.com/e6qu/sockerless/issues/190)** — Azure Blob data plane only supports host-based dispatch; Azurite-compatible path-style URLs (the Azure SDK + azurerm provider default) return 404. Blocks the Azure Blob lane.
- **[e6qu/sockerless#191](https://github.com/e6qu/sockerless/issues/191)** — Azure Key Vault secret `id` uses request scheme; partial #184 regression. Real Azure KV always uses `https://`. Lane works under TLS sim but not HTTP.

The later audit rounds closed those blockers and added the Azure Blob + Azure KV lanes, bringing `make sockerless-storage` to 9 passing tests.

## Phase 13 — closed (PR #20 merged 2026-05-24)

13.A, 13.B, 13.C all landed on PR #20 and are covered in their per-track sections of [PLAN.md § Phase 13](PLAN.md#phase-13--full-adapter-migration--production-auth--real-cloud-track-a). The notes here cover what was surprising in the 13.D sockerless slice.

**13.D.1 sockerless storage lane.** The user redirected Track A through `github.com/e6qu/sockerless` simulators before standing up real cloud accounts — same goal (catch translation defects in the AWS / GCP / Azure backend layers), no real-cloud cost.

Six issues filed upstream as fully self-contained reports (no shim references; sockerless maintainers can pick up the repros without reading our repo) — three fidelity bugs and three missing-service rollups:

- **[e6qu/sockerless#173](https://github.com/e6qu/sockerless/issues/173) — S3 mounted under `/s3/` URL prefix.** The AWS sim registers S3 ops at `GET /s3`, `PUT /s3/{bucket}`, etc. instead of the wire-protocol root. AWS SDK / CLI / TF-provider clients with `--endpoint-url=http://localhost:4566` hit `405 Method Not Allowed` on every S3 op. Workaround: append `/s3` to the endpoint URL. The simulator's own SDK tests use this workaround (`o.BaseEndpoint = aws.String(baseURL + "/s3")`).
- **[e6qu/sockerless#174](https://github.com/e6qu/sockerless/issues/174) — `aws-chunked` envelope stored verbatim.** When the aws-sdk-go-v2 `PutObject` is called with a non-seekable body, it uses `Transfer-Encoding: aws-chunked` framing. Real S3 unwraps that server-side. The sim writes the framed bytes (chunk-size hex line + chunk body + zero-size chunk + trailing checksum header) into its object store, so subsequent `GetObject` returns the framed envelope literally instead of the payload. Reproduces with an 11-byte string ending up as a 52-byte stored object. Blocks AWS PutObject/GetObject in the storage lane; doesn't affect bucket lifecycle.
- **[e6qu/sockerless#175](https://github.com/e6qu/sockerless/issues/175) — AWS Secrets Manager missing `ListSecretVersionIds`.** The sim wires 10 SM operations but not the version-listing one. The shim's `GetSecretValue` + `HeadSecret` both call into the version-ID mapping path (the shim translates monotonic uint64 ↔ Secrets Manager's UUID `VersionId` per-request, since the shim is stateless), so both surface a 400 `UnknownOperationException` against the sim. `CreateSecret` + `ListSecrets` + `DeleteSecret` work — exercised in the lane.

And three missing-service rollups — one per cloud, each listing the services we'd want to translate against but that aren't simulated today. Each issue lists per-service suggested yield-per-LOC ordering for the maintainers:

- **[e6qu/sockerless#176](https://github.com/e6qu/sockerless/issues/176) — AWS sim:** SQS, SNS, API Gateway v1 + v2, RDS / Aurora, ElastiCache.
- **[e6qu/sockerless#177](https://github.com/e6qu/sockerless/issues/177) — GCP sim:** Pub/Sub, Secret Manager, Cloud SQL Admin, Memorystore, API Gateway.
- **[e6qu/sockerless#178](https://github.com/e6qu/sockerless/issues/178) — Azure sim:** Blob data plane (URL is advertised in ARM responses but no handlers exist), Key Vault data plane, Service Bus (ARM + simplified data plane), Database for PostgreSQL FlexibleServer, Cache for Redis, API Management.

Two additional shim-side observations:

- The SDK refuses streaming-signed payloads over plain HTTP. Sockerless ships HTTP-by-default with optional TLS via `SIM_TLS_CERT` / `SIM_TLS_KEY`. The `make sockerless-storage` script generates an ephemeral self-signed cert in `/tmp/sockerless-tls/` so the AWS sim runs under TLS; the shim's S3 client trusts it via `AWS_S3_CONFORMANCE_INSECURE_TLS=1` → `InsecureSkipVerify`.
- Azure Blob is not implemented by sockerless — only Azure Files. The Azure sim advertises blob endpoint URLs in storage-account ARM responses (`https://{accountName}.blob.localhost:4568/`), but only the `file` service type has actual data-plane handlers; everything else falls through to a mock service-properties response.

GCS was the clean lane — sockerless implements the full `/storage/v1/b/...` REST surface and the SDK's `STORAGE_EMULATOR_HOST` env driver does the right thing (`option.WithEndpoint` doesn't, because it doesn't reroute every API surface the SDK touches). Full CreateBucket → PutObject → GetObject → DeleteObject → DeleteBucket round-trip passes.

The lane is opt-in via `make sockerless-storage`; CI's existing storage matrix stays inmem/minio. See [doc/SOCKERLESS_VALIDATION.md](doc/SOCKERLESS_VALIDATION.md) for the operational doc.

**Explicit list of work deferred to follow-on PRs — bundled into Phase 14.** What this PR did *not* finish should be just as visible as what it did. All of these have been hoisted into a new Phase 14 in [PLAN.md](PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons) with explicit upstream-sockerless-issue dependencies per item.

The Phase 14 framing matters because it makes the *consumer* of sockerless's evolution explicit: sockerless's simulator implementations are what give us cross-cloud shim verification + Terraform-provider round-trip testing at CI tempo, without real-cloud cost. Every shim translation layer becomes exercisable end-to-end against a deterministic in-process sim instead of (or in addition to) a real cloud account.

| Deferred item | Lands in | Sockerless dep |
|---|---|---|
| Azure Blob full handler migration (13.A.6) | 14.C | — |
| Azure APIM full handler migration (13.A.7) | 14.C | — (waits on upstream APIM spec broadening) |
| 7 GCP frontends full migration (13.B.2-8) | 14.C | — |
| AWS S3 PutObject/GetObject round-trip in sockerless lane | 14.A.2 | [#174](https://github.com/e6qu/sockerless/issues/174) close |
| AWS Secrets Manager HeadSecret + GetSecretValue in sockerless lane | 14.A.3 | [#175](https://github.com/e6qu/sockerless/issues/175) close |
| Drop the `/s3` URL workaround | 14.A.1 | [#173](https://github.com/e6qu/sockerless/issues/173) close |
| Azure Blob sockerless lane | 14.B.3 | [#178](https://github.com/e6qu/sockerless/issues/178) close |
| Sockerless functions / queue / pubsub / rdbms / cache / apigateway lanes | 14.B.1-3 | [#176](https://github.com/e6qu/sockerless/issues/176) / [#177](https://github.com/e6qu/sockerless/issues/177) / [#178](https://github.com/e6qu/sockerless/issues/178) close |
| BUG-8 + BUG-15 closure / reclassification | 14.B.2 (preferred) or 14.D fallback | sockerless#177 (GCP APIGW + Pub/Sub portions) |
| Real-signed sigv4 / Workload Identity / Entra ID conformance | 14.D | (sockerless can't fully substitute — requires real-cloud identity issuers) |
| Cross-cloud Apply matrix expansion | 14.E | 14.B in progress |

## Phase 12 — Spec-driven toolchain landing (PR #19, at exit)

The toolchain phase. Phase 11 spec-drove all 8 AWS frontends + 24/24 frontends got signature verification. Phase 12 took the Azure + GCP lanes to the same place, with 82+ granular commits.

**Track 2.A — Azure (8/8 specs codegen end-to-end).** `cmd/azure-codegen` runs an 8-stage preprocessor before `kin-openapi/openapi2conv.ToV3` + `oapi-codegen` (see [doc/CODEGEN.md](doc/CODEGEN.md) for the stage-by-stage table). Each preprocessor stage was driven by a real spec quirk:

- **Common-types inliner** (12.A.7/8/9/10) — Azure ARM specs `$ref` shared definitions in `common-types/resource-management/v<N>/<file>.json` by relative path; `kin-openapi`'s loader refuses external refs. The inliner merges every reachable common-types file's `definitions`/`parameters` into the main spec at the v2 layer. Vendored v1–v6 common-types, taught the inliner three relative-ref forms (full path, same-version sibling, cross-version sibling), the multi-file sibling case (`./<file>.json` resolving against the spec's own dir), and the `./examples/<file>` skip.
- **`promoteXMsEnumName`** (12.A.12) — Azure spec authors use `x-ms-enum.name` to say "this inline enum IS the top-level enum of the same name." oapi-codegen ignores the extension; the inline schema gets a Go name from the property path which collides with the standalone definition. The preprocessor rewrites the inline to a `$ref` to the top-level.
- **Parameter/header depth-gate refinement** (12.A.20) — Azure Blob's `parameters.AccessTierOptional` has `x-ms-enum.name = "AccessTier"` matching `definitions.AccessTier`; rewriting the parameter to a `$ref`-to-schema breaks v2→v3 (parameter refs must point at parameter objects). Same trap with response headers (`x-ms-copy-status` with `x-ms-enum.name = "CopyStatusType"` matching `definitions.CopyStatus`). The walker now tracks "non-schema depth" — suppressed under `parameters`/`headers` containers but reset on `schema`/`items` so body-parameter schemas still get rewritten.
- **`dedupeParameterDefNameCollisions`** (12.A.20.ii) — Blob ships both `definitions.LeaseDuration` (string enum) AND `parameters.LeaseDuration` (integer header). oapi-codegen emitted two `type LeaseDuration` declarations and failed `duplicate typename`. Preprocessor stamps `x-go-name: <N>Parameter` on the parameter.
- **`flattenARMAllOf` — BUG-20** (12.A.24) — ARM resource definitions use `{ allOf: [{$ref: TrackedResource}], properties: {own props} }`; oapi-codegen sees the 1-element allOf and emits `type X = TrackedResource` — a Go type alias that silently discards the schema's own properties. The new stage walks every top-level definition; when it has both allOf (with local $refs) AND own properties, inlines the referenced schemas' properties + required + additionalProperties into the local def, then drops the allOf. Local properties win on key collision. **Iterates until no definition changes** to handle chained inheritance (`X → allOf [Y]; Y → allOf [Z]`); the direct unit test in 12.A.31 caught + fixed the original implementation's premature `allOf`-clear that lost Z's properties through X.
- **`flattenXMSPaths`** (12.A.15) — Azure data-plane specs use `x-ms-paths` to disambiguate same-URL operations by query parameter (e.g. `/?restype=service&comp=properties` vs `/?comp=list`). OpenAPI doesn't model that; preprocessor moves entries into `paths` with the same key (path keys are opaque strings in OpenAPI; distinct query strings → distinct entries).
- **`normalizeAllOf`** (legacy from 11.4) — `kin-openapi`'s v2→v3 converter attaches empty `AllOf: []` to scalar enum schemas; oapi-codegen panics on `allOf[0]`. Nil the empty slice everywhere.
- **Deterministic walk** (12.A.12) — `sortedKeys()` everywhere; multi-version common-types merges produce byte-identical output across runs.

Every preprocessor stage has direct unit tests (`cmd/azure-codegen/main_test.go`); per-service `azure_gen_test.go` asserts ServerInterface kind + a method-count minimum; BUG-20 regression tests in each service decode + JSON-round-trip a realistic ARM body through `ContainerApp` / `RedisResource` / `Server`.

`azure_keyvault` ships fully migrated through `gen.HandlerWithOptions` as the reference impl (Phase 12.A.1/2; Phase 13.A migrates the other 7).

**Track 2.B — GCP routing emitter (8/8 services).** `cmd/gcp-codegen` reads vendored Discovery JSON and emits `Routes []Route` triples + a compiled `*regexp.Regexp` per route + `BasePath` constant + `Match(method, path)` / `MatchAll(method, path)` helpers. Per AGENTS.md #11 the wire types reuse `google.golang.org/api/<svc>/v1`; the emitter is routing-only. Per-service tests assert inventory non-empty + sorted + cross-cloud-intersection ops covered + `BasePath` sane + every route's compiled Pattern matches a template-derived sample path (413 routes total).

**Vendored-spec provenance (12.0.4–12.0.24).** JSON has no comment syntax; closest analogue is a top-level field. `cmd/inject-provenance` writes `_provenance` as the FIRST top-level key of every spec JSON (24 service + 26 common-types = 50 files), preserving source-file key ordering for everything else (encoding/json's map iteration would re-sort lexicographically and diff massively against verbatim upstream). The three fetch scripts (`scripts/fetch-{aws,azure,gcp}-*.sh`) run the injector after download. CI guards: `TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance` (the last surfaced a real Makefile bug — pubsub's Azure gen had a placeholder zero-SHA because the commit lookup used the manifest dir instead of the spec dir; fixed 12.A.36). `make codegen-check` runs `inject-provenance` too, so a SOURCES.md edit without re-injection trips the pre-push lane.

**Spec freshness lane (12.0.1/2/15).** `make spec-freshness` walks every SOURCES.md and reports drift against upstream HEAD. Weekly workflow (Mondays 14:00 UTC). Renovate custom manager tracks vendored-spec SHAs and opens issues when one falls behind.

**Per-service Terraform walkthroughs (12.0.12/13/14).** Every MIGRATION.md gained a copy-pasteable HCL walkthrough — `provider "aws" { endpoints { <svc> = ... } }` against a non-AWS backend; storage also got the symmetric `provider "google" { storage_custom_endpoint = ... }` against a non-GCS backend. HCL pulled from each service's `cross_cloud_import_test.go` fixture so the example is real, not invented.

**Exit criteria all met.** (1) `TestCrossCloudApply_Roundtrip_<svc>_<cell>` exists + passes for all 8 services. (2) 8/8 Azure + 8/8 GCP frontends have gen files compiling + imported by per-service smoke tests. (3) Every MIGRATION.md carries a copy-pasteable Terraform walkthrough. Adapter migration of the 7 remaining Azure + 8 GCP frontends → Phase 13.A/B.

## Phase 11 — Tighten the wire boundary (PR #18, merged 2026-05-22 at `bcd72e5`)

Replace hand-written wire layers with spec-driven generated stubs across every AWS-shaped frontend, and wire real signature verification at the new decode boundary. Codex review of the initial plan corrected several wrong-library assumptions (SigV4 in `signer/v4` is signer-only, not verifier; `golang.org/x/oauth2` is acquisition not verification; Azure Key Vault is Bearer-only not SharedKey; the existing Smithy emitter was REST-XML only — extending to awsJson / awsQuery / restJson1 is new emitter work, not a routing-table addition).

**Four wire-protocol emitter paths.** Pre-Phase 11 the emitter only spoke REST-XML (S3). Phase 11 added three more templates + protocol detection: **awsJson1_x** (POST `/` dispatched by `X-Amz-Target`; JSON request + response; `__type` + `X-Amzn-Errortype` envelope — powers Secrets Manager 1_1 + SQS 1_0); **restJson1** (HTTP-route dispatched same as REST-XML; JSON bodies + awsJson-shaped error envelope — powers Lambda + APIGW v2); **awsQuery** (POST `/` dispatched by `Action` form parameter; form-encoded request; XML response wrapped in `<OpResponse><OpResult>...</OpResult><ResponseMetadata>...</ResponseMetadata></OpResponse>` — powers SNS, RDS, ElastiCache). Three runtime helper packages: `internal/awsjson/`, `internal/awsquery/`, plus `internal/restxml/` shared by REST-XML and restJson1.

**8/8 AWS frontends migrated.** 3,853 LOC of hand-written wire deleted. Each migration follows the same pattern: per-service adapter implementing the generated `<Service>Backend` interface, translating each generated request type into the existing domain layer.

**Signature verification — BUG-18 closed end-to-end.** Four verifier packages wrap all 24 frontends; per-cloud detail in [doc/VERIFIERS.md](doc/VERIFIERS.md). Each cloud needed a specific fix to make end-to-end signed conformance work: manual SigV4 in `canonical.go` accepting both Go-SDK and boto3 signing shapes (the SDK auto-includes `Content-Length` in `SignedHeaders`, boto3 doesn't — divergence broke verification); test JWT helpers per cloud emitting well-formed HS256 tokens the verifiers accept; `azuresharedkey` uses `EscapedPath()` to match azblob SDK canonicalisation. Also surfaced + fixed during the closer: awsQuery map-shape XML marshalling (`MarshalXML` per Smithy map type emitting `<entry><key>...</key><value>...</value></entry>`); SNS GetTopicAttributes empty Policy field rejected by hashicorp/aws's IAM-policy parser (now emits canonical default policy); SNS SetTopicAttributes unconditionally called for feedback-rate/role-ARN attributes terraform-provider-aws sets on every apply (now no-ops AWS-only attributes via explicit allowlist).

**Azure oapi-codegen pilot (11.4).** Toolchain proof — vendor Azure Swagger 2.0, convert v2→v3 via `kin-openapi/openapi2conv`, run `oapi-codegen` as a library. `azure_keyvault`'s `SetSecret` decodes via `gen.SecretSetParameters`. Two upstream-tooling defects worked around inside the driver: `kin-openapi` attaches empty `AllOf: []` to scalar enum schemas (`normalizeAllOf` walks + nils); host-template `$ref` preservation (documented; not a blocker).

**Surprises (knowledge artifacts).**

- **Smithy emitter is REST-XML-shaped under the hood.** Generated handlers `import restxml`, route from `smithy.api#http` traits. awsJson / awsQuery / restJson1 each need new protocol serde at the emitter level, not "routing-table addition".
- **awsJson1_x timestamps are epoch-seconds floats, not RFC3339.** Go's default MarshalJSON emits RFC3339; AWS SDKs reject. New `awsjson.EpochTime` type with custom MarshalJSON.
- **awsQuery list element names differ per spec.** SNS uses `<member>` (protocol default); RDS / ElastiCache use `<DBInstance>` / `<CacheCluster>` (via `@xmlName` traits). Emitter respects trait when set; falls back to `<member>` for awsQuery.
- **awsQuery error envelope's outer element is awkward.** `xml.Marshal(result)` produces `<ResultType>...</ResultType>` by default; wrapping that inside `<OpResult>` produced double-nested output. `WriteResult` strips the result struct's own outer element so its fields inline cleanly.
- **APIGateway v2's multi-step deploy flow carries per-process state.** AWS splits gateway creation into Api + Routes + Integrations + Deployment; the domain has a single atomic DeployGateway. Adapter retains the pending-routes / integration-IDs map in per-process memory — documented compromise of stateless-shim (deployed routing table itself still lives in backend; only in-flight accumulation is per-process).
- **Lambda's required-Role makes cross-cloud Create-via-Lambda-SDK intersection-out.** Non-AWS backends honestly reject non-empty Role with `InvalidParameterValueException`.

**Deferrals → Phase 12 (now closed in PR #19).** Broader Azure spec-driven migration (11.4 continuation), GCP routing emitter (11.5), production RS256 JWKS (now Phase 13.C). Track-A bugs (BUG-8, BUG-15) carried to Phase 13.D.

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

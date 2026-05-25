# Known Bugs

**20 filed · 18 fixed · 2 open · 1 false positive. Upstream sockerless audit issues through #215 are closed as of sockerless PR #216; no upstream sockerless issue currently blocks the shim lane.**

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · rules [AGENTS.md](AGENTS.md).

> **Standing rule:** every CI failure, conformance-test failure, fidelity gap, fake/stub, placeholder, silent fallback, skipped test, or incomplete implementation lands here with a one-liner **before** any fix attempt. Workarounds are bugs and get the same treatment. Per-bug fix detail beyond the one-liner: `git log <commit>` or the linked PR.
>
> When a bug surfaces during a coding-agent session, file the BUG before fixing — even if the fix is a single line. The audit trail is what makes "never lie" enforceable.

## Open — both absorbed into Phase 14

Sockerless now simulates the relevant GCP API Gateway and Pub/Sub backend surfaces. The current backend/SDK legs are green:

- BUG-8: `TestSockerless_GCP_APIGateway_CRUD` clears the shim backend ↔ GCP API Gateway SDK-shaped leg. The remaining bug is specifically the hashicorp/google Terraform endpoint/OAuth leg.
- BUG-15: `TestSockerless_GCP_Queue_RetentionRoundTrip` clears the shim backend retention PATCH/read leg. The remaining bug is specifically the hashicorp/google Terraform state-drift question for `message_retention_duration`.

| ID | Sev | Area | Source-API | One-liner | Phase |
|----|-----|------|------------|-----------|-------|
| BUG-8 | P3 | apigateway/gcp-tf-frontend | `hashicorp/google` | API Gateway endpoint-override attribute name changed across provider major versions and the current provider's API Gateway resource lifecycle requires real OAuth-signed requests the mock httptest server can't sign. `services/apigateway/conformance/gcp_terraform_test.go` is smoke-skipped pending Track A real-cloud TF coverage. The sockerless GCP APIGW backend lane passes; this is now only the Terraform-provider leg. | **14.D** |
| BUG-15 | P3 | queue/gcp-frontend | GCP Pub/Sub `subscriptions.get` | `message_retention_duration = "604800s"` declared in HCL and the shim responding "604800s" at every call, hashicorp/google records `"345600s"` in state. Plan after apply diffs `"345600s" -> "604800s"`. Shim's backend retention PATCH/read path now passes against sockerless; the open question is whether the provider shows the same state drift against real GCP or the shim frontend still misses a provider-needed field. | **14.D** |

## Upstream-tracked (sockerless validation lane)

Sockerless fidelity gaps tracked on `github.com/e6qu/sockerless`. Each is filed as a fully self-contained issue (no shim references; sockerless maintainers can pick up the repro without reading this repo). See [doc/SOCKERLESS_VALIDATION.md](doc/SOCKERLESS_VALIDATION.md) for the wider context.

### Round 1 (Phase 13.D.1) — all closed via [sockerless PR #179](https://github.com/e6qu/sockerless/pull/179) on 2026-05-23

| Upstream | Status |
|---|---|
| [e6qu/sockerless#173](https://github.com/e6qu/sockerless/issues/173) — S3 `/s3/` URL prefix | ✅ closed; PR #179 routed S3 at canonical root. |
| [e6qu/sockerless#174](https://github.com/e6qu/sockerless/issues/174) — `aws-chunked` envelope stored verbatim | ✅ closed; PR #179 added the chunked-encoding decoder. |
| [e6qu/sockerless#175](https://github.com/e6qu/sockerless/issues/175) — missing `ListSecretVersionIds` | ✅ closed; PR #179 added the op + version history. |
| [e6qu/sockerless#176](https://github.com/e6qu/sockerless/issues/176) — AWS missing services | ✅ closed; PR #179 added SQS, SNS, RDS, ElastiCache, APIGW v1+v2. |
| [e6qu/sockerless#177](https://github.com/e6qu/sockerless/issues/177) — GCP missing services | ✅ closed; PR #179 added Pub/Sub, Secret Manager, Cloud SQL, Memorystore, API Gateway. |
| [e6qu/sockerless#178](https://github.com/e6qu/sockerless/issues/178) — Azure missing services | ✅ closed; PR #179 added Blob data plane, Key Vault data plane, Service Bus, PostgreSQL FlexibleServer, Redis Cache, APIM. |

Phase 14.A re-enabled the shim assertions for #173/#174/#175 (storage + secrets lanes now round-trip AWS S3 + AWS Secrets Manager end-to-end). Phase 14.B picks up the new services from #176/#177/#178 as the round-2 fidelity bugs below close.

### Round 2 (Phase 14.D audit) — all closed via [sockerless PR #180](https://github.com/e6qu/sockerless/pull/180) on 2026-05-24

| Upstream | Status |
|---|---|
| [e6qu/sockerless#181](https://github.com/e6qu/sockerless/issues/181) — Azure Cache for Redis ARM case sensitivity | ✅ closed; ARM path-normalization middleware. |
| [e6qu/sockerless#182](https://github.com/e6qu/sockerless/issues/182) — GCP Pub/Sub subscription field drops | ✅ closed; full 7-field round-trip. |
| [e6qu/sockerless#183](https://github.com/e6qu/sockerless/issues/183) — GCP Secret Manager routing leak | ✅ closed; ListSecrets registered explicitly. |
| [e6qu/sockerless#184](https://github.com/e6qu/sockerless/issues/184) — Azure KV malformed kid URLs | ✅ closed; later secret URL regression tracked separately in #191 and also closed. |
| [e6qu/sockerless#185](https://github.com/e6qu/sockerless/issues/185) — Azure KV placeholder modulus | ✅ closed; real RSA modulus emitted. |
| [e6qu/sockerless#186](https://github.com/e6qu/sockerless/issues/186) — AWS SQS attribute drops | ✅ closed; full attribute persistence. |
| [e6qu/sockerless#187](https://github.com/e6qu/sockerless/issues/187) — GCP Cloud SQL relative selfLink | ✅ closed; fully-qualified selfLink. |
| [e6qu/sockerless#188](https://github.com/e6qu/sockerless/issues/188) — GCP Secret Manager `latest` alias | ✅ closed; concrete version number resolved. |

### Round 3 (per-service audit, sockerless PR #180 follow-ups) — all closed

| Upstream | Status |
|---|---|
| [e6qu/sockerless#189](https://github.com/e6qu/sockerless/issues/189) — GCP Pub/Sub `projects.subscriptions.patch` returns 404 | ✅ closed in sockerless PR #192. |
| [e6qu/sockerless#190](https://github.com/e6qu/sockerless/issues/190) — Azure Blob path-style URLs return 404 | ✅ closed after reopen; path-style and host-based Blob dispatch verified. |
| [e6qu/sockerless#191](https://github.com/e6qu/sockerless/issues/191) — Azure KV secret `id` uses request scheme | ✅ closed in sockerless PR #192. |

### Later audit rounds — all closed as of sockerless PR #216

| Upstream | Status |
|---|---|
| [e6qu/sockerless#193](https://github.com/e6qu/sockerless/issues/193) | ✅ closed by PR #202 after a reopen; KV challenge now satisfies Azure SDK tenant parsing. |
| [e6qu/sockerless#194](https://github.com/e6qu/sockerless/issues/194) | ✅ closed by PR #200; AWS RDS / ElastiCache default `EngineVersion` now emits real-shape values instead of empty strings. |
| [e6qu/sockerless#195](https://github.com/e6qu/sockerless/issues/195) | ✅ closed by PR #200; Azure Service Bus REST send/receive status/body semantics fixed. |
| [e6qu/sockerless#196](https://github.com/e6qu/sockerless/issues/196) | ✅ closed by PR #211 after a reopen; S3 multipart/subresource family verified. |
| [e6qu/sockerless#197](https://github.com/e6qu/sockerless/issues/197) | ✅ closed by PR #200; GCP `/v1/operations` no longer falls through to GCS-shaped 404. |
| [e6qu/sockerless#198](https://github.com/e6qu/sockerless/issues/198) | ✅ closed by PR #200; GCS compose/upload gaps and URL scheme drift fixed. |
| [e6qu/sockerless#199](https://github.com/e6qu/sockerless/issues/199) | ✅ closed by PR #200; Lambda versions/aliases/permissions/function URL handlers added. |
| [e6qu/sockerless#201](https://github.com/e6qu/sockerless/issues/201) | ✅ closed by PR #202; S3 bucket-level PUT subresources no longer route to CreateBucket. |
| [e6qu/sockerless#203](https://github.com/e6qu/sockerless/issues/203) | ✅ closed by PR #211; KV secret versions return the paged list shape and versioned values. |
| [e6qu/sockerless#204](https://github.com/e6qu/sockerless/issues/204) | ✅ verified not a bug after re-probe; APIGW v2 deployment response shape matched AWS. |
| [e6qu/sockerless#205](https://github.com/e6qu/sockerless/issues/205) | ✅ closed by PR #211; KV PATCH and deleted-secret surfaces added. |
| [e6qu/sockerless#206](https://github.com/e6qu/sockerless/issues/206) | ✅ closed by PR #211; Azure Functions/App Service config routes added. |
| [e6qu/sockerless#207](https://github.com/e6qu/sockerless/issues/207) | ✅ closed by PR #211; AWS RDS/SNS/SQS per-service missing actions added. |
| [e6qu/sockerless#208](https://github.com/e6qu/sockerless/issues/208) | ✅ closed by PR #211; awsQuery tag action router collision fixed. |
| [e6qu/sockerless#209](https://github.com/e6qu/sockerless/issues/209) | ✅ closed by PR #216 after a reopen; GCP Cloud SQL / Memorystore / Pub/Sub IAM gaps fixed. |
| [e6qu/sockerless#210](https://github.com/e6qu/sockerless/issues/210) | ✅ closed by PR #216 after a reopen; Azure PG / APIM / Redis remaining gaps fixed. |
| [e6qu/sockerless#213](https://github.com/e6qu/sockerless/issues/213) | ✅ closed by PR #216; Azure Resources Tags API added. |
| [e6qu/sockerless#214](https://github.com/e6qu/sockerless/issues/214) | ✅ closed by PR #216; Service Bus authorizationRules/listKeys/regenerateKeys added. |
| [e6qu/sockerless#215](https://github.com/e6qu/sockerless/issues/215) | ✅ closed by PR #216; AWS IAM managed-policy/instance-profile and APIGW v1 response handlers added. |

### Sockerless coverage history

- **Round 1** ([#173-178](https://github.com/e6qu/sockerless/issues/173)) — all closed by sockerless PR #179. Initial fidelity gaps (S3 `/s3/` URL prefix, `aws-chunked` envelope, missing `ListSecretVersionIds`) + missing-service rollups (AWS / GCP / Azure).
- **Round 2** ([#181-188](https://github.com/e6qu/sockerless/issues/181)) — all closed by sockerless PR #180. Per-service fidelity drift across SQS / Pub/Sub / Secret Manager / Cloud SQL / KV / Redis ARM.
- **Round 3** ([#189-191](https://github.com/e6qu/sockerless/issues/189)) — closed by sockerless PR #192 plus the later #190 reopen closure.
- **Later rounds** ([#193-215](https://github.com/e6qu/sockerless/issues/193), excluding unused issue numbers) — closed by sockerless PRs #200, #202, #211, and #216. Current shim lane is green; no upstream sockerless issue is open.

## False positives

| Area | Finding | Why it's not a bug |
|------|---------|--------------------|
| BUG-14 / storage Apply test fixture | `terraform apply` of `aws_s3_bucket` with no `tags` declared, followed by `plan -refresh-only -detailed-exitcode`, surfaces `tags: absent → {}` drift. | The shim returns `<Code>NoSuchTagSet</Code>` 404, identical to real AWS. The drift comes from hashicorp/aws's own behavior: it records `tags = {}` after read regardless of whether the HCL declared tags. Real AWS exhibits the same drift. **Resolution:** Phase 10 apply-test fixtures declare `tags = {}` explicitly. Documented in [`services/storage/APPLY_INTERSECTION.md`](services/storage/APPLY_INTERSECTION.md). |

## Class-of-bug rules (carried forward)

When a new bug fits one of these, tag it with the rule.

- **Fidelity-to-source-API is P0.** If the shim's response shape, header set, status code, error envelope, or async-operation semantics diverge from the cloud's published API, that is a P0 bug. The spec is the contract.
- **No fakes, no fallbacks, no skips.** Synthetic responses, hardcoded values, in-memory state where a real backend was specified, conditional `t.Skip` for missing config — all file as bugs and get real fixes.
- **Out-of-intersection features must fail loud in the source cloud's error vocabulary.** Fabricating a success response, returning a generic 500, or silently degrading to a partial result are all bugs.
- **Each shimmed operation requires SDK + CLI + Terraform conformance in the same commit.** A merged operation without all three driver tests is a bug.
- **K8s peer parity.** When a shimmed operation works against AWS / GCP / Azure backends but not the K8s peer (and no documented platform limitation explains the gap), that is a bug.
- **Spec drift.** When the upstream cloud spec changes shape, the codegen pipeline must regenerate and the translation table must be updated in the same PR. Stale generated code is a bug.
- **Cross-backend sweep on every find.** When a translation gap appears in one (source, backend) pair, the same code paths in the other backend pairs get checked in the same commit.
- **Recorded-interaction drift.** When a cassette / VCR recording silently masks a real-cloud behavior change, the test is a bug.
- **Incompatible-license dependency.** Adding a Go module whose license is not on the [`doc/COMPATIBLE_LICENSES.md`](doc/COMPATIBLE_LICENSES.md) allowlist is a bug — CI's `licenses` job blocks it.

## Resolved history (compressed; commit-log + linked PR have detail)

| ID | Sev | Area | Closed in | One-liner |
|---|---|---|---|---|
| 20 | P2 | azure-codegen / ARM | Phase 12.A.24 (PR #19) | `flattenARMAllOf` preprocessor inlines `{ allOf: [TrackedResource], properties: {own} }` so oapi-codegen emits a struct, not a type alias. ContainerApp / RedisResource / Server (PostgreSQL FlexibleServer) emit as proper Go structs. Phase 12.A.31 caught + fixed a chained-inheritance bug in the same stage. |
| 18 | P3 | all frontends (sig verification) | Phase 11.14 (PR #18) | 4 verifier packages wrap all 24 frontends; bypass dropped; every conformance test signs end-to-end. AWS-CLI compatibility via manual SigV4 in `canonical.go` accepting both Go-SDK and boto3 signing shapes. See [doc/VERIFIERS.md](doc/VERIFIERS.md). |
| 17 | P2 | secrets/domain + frontends | Phase 10.3 (PR #17) | `UpdateSecret` / `TagResource` / `UntagResource` wired through domain + all backends. |
| 16 | P2 | rdbms/gcp-frontend | Phase 10.3 (PR #17) | Route regexes match both `/v1/projects/...` (Go SDK + gcloud) and `/sql/v1beta4/projects/...` (hashicorp/google). Added users + databases canonical envelopes. |
| 13 | P3 | functions / domain + frontends | Phase 10.3 (PR #17) | `domain.Function` gained `Role` + `Publish`; AWS frontend parses + emits; non-AWS backends reject non-empty Role + Publish=true honestly. |
| 12 | P2 | queue/domain + frontends | PR #17 | `domain.Queues` gained `ListQueueTags` / `TagQueue` / `UntagQueue`; per-backend tag storage (inmem / AWS / GCP labels / Azure rejection / NATS metadata). |
| 11 | P3 | apigateway/aws-frontend | Phase 8/9 | restJson1 error envelope now includes `__type` JSON body field + `X-Amzn-Errortype` header. |
| 10 | P2 | apigateway/azure-frontend | Phase 9 | Azure APIM `Operations.{CreateOrUpdate,Get,Delete,List}` subresource added. |
| 9 | P2 | apigateway/gcp-frontend | Phase 9 | GCP API Gateway `Apis` + `ApiConfigs` families added; ApiConfigs.Create parses OpenAPI 2/3 → `domain.Route` → DeployGateway. |
| 7 | P3 | apigateway/azure-cli | Phase 9 | `az cloud register` + `az cloud set` accept custom endpoints at session scope; `TestAzCLI_APIGateway_Smoke` exercises the flow with `t.Cleanup`. |
| 6 | P2 | apigateway/azure-backend | Phase 9 | `armapimanagement/v3 APIClient.BeginDelete` wired with `ifMatch = "*"` + `DeleteRevisions = nil` + `PollUntilDone`. |
| 5 | P3 | rdbms / cache / functions / apigateway (GCP) | Phase 10.1 (PR #16) | All four GCP frontends implement `Operations.Get` statelessly — Operation `Name` encodes `(opType, target)`; polling re-reads the resource. |
| 4 | P2 | queue/nats | Earlier | NATS backend emits `MessageID = strconv.FormatUint(ack.Sequence, 10)` — sequence-only, no `/`. |
| 3 | P1 | queue/nats | Earlier | NATS backend ops wrap caller's context with `withDeadline` before handing to NATS library. |
| 2 | P2 | queue / domain + frontends | Phase 10.3 (PR #17) | `domain.Queues.SetQueueAttributes` wired through all backends. AWS frontend surfaces canonical read-side attributes + `x-amzn-query-error` for awsQueryCompatible. |
| 1 | P2 | restxml router | Phase 1.12 (PR #6) | Router required-only matching meant `?tagging=` silently fell through to GetObject. Fix: `restxml.RouteOptions.ForbiddenQueries`; codegen emits S3 feature-query list. |
| 19 | P3 | queue/conformance | PR #17 | Stale `t.Skip("BUG-2: ...")` removed; the Phase-3 TF cell now exercises the closed-BUG-2 path end-to-end. |

# Known Bugs

**20 filed · 18 fixed · 2 open · 1 false positive. Plus 6 upstream-sockerless issues tracked separately (3 fidelity bugs + 3 missing-service asks).**

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · rules [AGENTS.md](AGENTS.md).

> **Standing rule:** every CI failure, conformance-test failure, fidelity gap, fake/stub, placeholder, silent fallback, skipped test, or incomplete implementation lands here with a one-liner **before** any fix attempt. Workarounds are bugs and get the same treatment. Per-bug fix detail beyond the one-liner: `git log <commit>` or the linked PR.
>
> When a bug surfaces during a coding-agent session, file the BUG before fixing — even if the fix is a single line. The audit trail is what makes "never lie" enforceable.

## Open — both absorbed into Phase 13.D.2 (real-cloud Track A)

Sockerless doesn't simulate GCP API Gateway or GCP Pub/Sub, so neither bug can be closed via the 13.D.1 sockerless lane — both still gate on real cloud accounts.

| ID | Sev | Area | Source-API | One-liner | Phase |
|----|-----|------|------------|-----------|-------|
| BUG-8 | P3 | apigateway/gcp-tf-frontend | `hashicorp/google` | API Gateway endpoint-override attribute name changed across provider major versions and the current provider's API Gateway resource lifecycle requires real OAuth-signed requests the mock httptest server can't sign. `services/apigateway/conformance/gcp_terraform_test.go` is smoke-skipped pending Track A real-cloud TF coverage. | **13.D.2** |
| BUG-15 | P3 | queue/gcp-frontend | GCP Pub/Sub `subscriptions.get` | `message_retention_duration = "604800s"` declared in HCL and the shim responding "604800s" at every call, hashicorp/google records `"345600s"` in state. Plan after apply diffs `"345600s" -> "604800s"`. Shim's HTTP responses contain "604800s" (verified); something in the provider's flatten / state-write path substitutes its schema default. Honest interpretations: (a) provider bug — real GCP exhibits same drift; (b) shim's response is missing a field the provider needs to disable its default-substitution path (`expirationPolicy`, `retainAckedMessages`). Closes false-positive if (a), reopens as a real fix if (b). | **13.D.2** |

## Upstream-tracked (sockerless validation lane)

Sockerless fidelity gaps surfaced while wiring Phase 13.D.1's sockerless lane. Each is filed as a fully self-contained issue on `github.com/e6qu/sockerless` (no shim references; sockerless maintainers can pick up the repro without reading this repo). See [doc/SOCKERLESS_VALIDATION.md](doc/SOCKERLESS_VALIDATION.md) for the wider context.

| Upstream | Filed | Blocks (in shim's lane) | Workaround / status |
|---|---|---|---|
| [e6qu/sockerless#173](https://github.com/e6qu/sockerless/issues/173) — S3 mounted under `/s3/` URL prefix | 2026-05-23 | Out-of-the-box SDK / CLI / TF-provider S3 calls 405 | Append `/s3` to the endpoint URL. Lane works around it. |
| [e6qu/sockerless#174](https://github.com/e6qu/sockerless/issues/174) — `aws-chunked` envelope stored verbatim | 2026-05-23 | AWS S3 PutObject/GetObject round-trip in our lane | No workaround. Lane covers bucket lifecycle only until fixed upstream. |
| [e6qu/sockerless#175](https://github.com/e6qu/sockerless/issues/175) — missing `ListSecretVersionIds` | 2026-05-23 | AWS Secrets Manager HeadSecret + GetSecretValue in our lane | No workaround (the shim's version-mapping path needs it). Lane covers CreateSecret + ListSecrets + DeleteSecret until fixed upstream. |

### Sockerless coverage gaps (deferred — not bugs in sockerless, just service scope)

Sockerless doesn't simulate every cloud service the shim translates. The following backends remain outside 13.D.1's sockerless lane and gate on **13.D.2 real-cloud Track A** instead. **Filed upstream as missing-feature asks** (one roll-up per cloud, with per-service yield-per-LOC suggestions for the maintainers):

| Cloud | Upstream ask |
|---|---|
| AWS | [e6qu/sockerless#176](https://github.com/e6qu/sockerless/issues/176) — SQS, SNS, API Gateway v1 + v2, RDS / Aurora, ElastiCache. |
| GCP | [e6qu/sockerless#177](https://github.com/e6qu/sockerless/issues/177) — Pub/Sub, Secret Manager, Cloud SQL, Memorystore, API Gateway. |
| Azure | [e6qu/sockerless#178](https://github.com/e6qu/sockerless/issues/178) — Blob data plane, Key Vault data plane, Service Bus (ARM + data), Database for PostgreSQL FlexibleServer, Cache for Redis, API Management. |

| Backend | Why no sockerless coverage | Tracked upstream |
|---|---|---|
| Azure Blob data plane | Sockerless's Azure sim implements Azure Files only; blob endpoint URLs are advertised in storage-account ARM responses but the data-plane handlers don't exist. | [#178](https://github.com/e6qu/sockerless/issues/178) |
| GCP API Gateway | Not in sockerless's GCP sim. Blocks closing BUG-8 via this lane. | [#177](https://github.com/e6qu/sockerless/issues/177) |
| GCP Pub/Sub | Not in sockerless's GCP sim. Blocks reclassifying BUG-15 via this lane. | [#177](https://github.com/e6qu/sockerless/issues/177) |
| GCP Cloud SQL | Not in sockerless's GCP sim. | [#177](https://github.com/e6qu/sockerless/issues/177) |
| GCP Memorystore | Not in sockerless's GCP sim. | [#177](https://github.com/e6qu/sockerless/issues/177) |
| GCP Secret Manager | Not in sockerless's GCP sim. | [#177](https://github.com/e6qu/sockerless/issues/177) |
| Azure Key Vault data plane | Not in sockerless's Azure sim (control-plane storage-accounts is in; vault data-plane isn't). | [#178](https://github.com/e6qu/sockerless/issues/178) |
| Azure Service Bus, Azure PostgreSQL, Azure Redis, Azure APIM | Not in sockerless's Azure sim. | [#178](https://github.com/e6qu/sockerless/issues/178) |
| AWS SQS, SNS, API Gateway, RDS, ElastiCache | Not in sockerless's AWS sim. | [#176](https://github.com/e6qu/sockerless/issues/176) |
| AWS Lambda + GCP Cloud Run + Azure Container Apps (Functions) | Sims exist in sockerless but the shim's Functions backends weren't wired to them yet — tracked as a follow-on (see [DO_NEXT.md](DO_NEXT.md#follow-ons-deferred-from-13d1)). | n/a (shim follow-on) |

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

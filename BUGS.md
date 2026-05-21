# Known Bugs

**13 filed · 7 fixed · 6 open · 0 false positives.**

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · rules [AGENTS.md](AGENTS.md).

> **Standing rule:** every CI failure, conformance-test failure, fidelity gap, fake/stub, placeholder, silent fallback, skipped test, or incomplete implementation lands here with a one-liner **before** any fix attempt. Workarounds are bugs and get the same treatment. Per-bug fix detail beyond the one-liner: `git log <commit>` or the linked PR.
>
> When a bug surfaces during a coding-agent session, file the BUG before fixing — even if the fix is a single line. The audit trail is what makes "never lie" enforceable.

## Open

| ID | Sev | Area | Source-API | One-liner |
|----|-----|------|------------|-----------|
| BUG-2 | P2 | queue | AWS SQS `SetQueueAttributes` | Phase 3 intersection is 8 ops; `SetQueueAttributes` (used by `hashicorp/aws aws_sqs_queue` for attribute reconciliation after CreateQueue) is not yet wired through the domain or any backend. Terraform AWS-frontend conformance is ◇-skipped until this lands. Same gap ripples through to `aws_sns_topic_subscription` in Phase 4. Adding it requires the domain method + all 5 backends + the AWS frontend dispatch entry. |
| BUG-3 | P1 | queue/nats | NATS JetStream | DeleteMessage / ChangeVisibility / Publish call `nats.Context(ctx)` or `nc.FlushWithContext(ctx)` with the caller's context. When CI runs `TestQueueMatrix_*` it passes `context.Background()` (no deadline); the NATS library rejects this with "nats: context requires a deadline" and the operation 500s. Conformance-nats lane red. Fix: wrap any backend call's context with a default deadline (30s) before handing it to the NATS library. |
| BUG-4 | P2 | queue/azure-frontend | Azure Service Bus REST | Frontend route `^/([^/]+)/messages/([^/]+)/([^/]+)$` doesn't tolerate `/` inside the messageID or lockToken. NATS backend emits `MessageID = "<stream>/<seq>"`, which breaks the URL when the Azure frontend pairs with the NATS backend in the matrix. Fix: scope the NATS MessageID to a slash-free identifier (stream seq only). Defence-in-depth: matrix-test could URL-escape components, but the right answer is to not have slashes in opaque IDs. |
| BUG-12 | P2 | queue/domain | `domain.Queues` | No tag storage in the queue domain. `ListQueueTags` is wired as a category-2 honest-empty response so `terraform import aws_sqs_queue` works, but `TagQueue` / `UntagQueue` write paths aren't backed. Migration users who set tags via Terraform will see the tags rejected. Fix: add tag storage as a domain primitive + per-backend wiring (AWS native, GCP labels, Azure tags, NATS stream metadata). Surfaced by Phase 9.5's queue import test. |
| BUG-13 | P3 | functions/aws-frontend | AWS Lambda restJson1 | `aws_lambda_function` import succeeds end-to-end, but `terraform plan` after import proposes diffs for three attributes: `memory_size` (0 → 128 default), `publish` (missing → false), `role` (missing → ARN). Real Lambda always returns these; the shim's `functionToAWS()` should default `memory_size` to 128 and surface Role + Publish. Role requires domain extension (the AWS backend takes it via config; other backends don't have a role concept). Fix: short-term — emit defaults in the frontend; long-term — extend `domain.Function` with Role. |
| BUG-6 | P2 | apigateway/azure-backend | Azure APIM `APIClient.Delete` | `armapimanagement/v3` exposes `Delete(ctx, rg, svc, name, deleteRevisions string, options *Options)` requiring an If-Match etag + a deleteRevisions decision; the v1 SDK exposed `Delete(ctx, rg, svc, name)` directly. Phase 8's Azure backend returns `domain.InvalidArgument` honestly until a v3-correct etag-fetching delete is written. Blocks Track A azure-backend conformance for `DeleteGateway`. |
| BUG-7 | P3 | apigateway/azure-cli-frontend | `az` CLI | `az` exposes no documented per-resource endpoint override that targets only APIM (the closest mechanism is the cloud-environment switch, which forces every resource type through the shim — incompatible with multi-cloud workflows). `services/apigateway/conformance/azure_cli_test.go` is smoke-skipped pending this. |
| BUG-8 | P3 | apigateway/gcp-tf-frontend | `hashicorp/google` | API Gateway endpoint-override attribute name changed across provider major versions and the current provider's API Gateway resource lifecycle requires real OAuth-signed requests the mock httptest server can't sign. `services/apigateway/conformance/gcp_terraform_test.go` is smoke-skipped pending Track A real-cloud TF coverage. |

## False positives

| Area | Finding | Why it's not a bug |
|------|---------|--------------------|

*(empty)*

## Class-of-bug rules (carried forward)

These are the failure patterns that recur across services. When a new bug fits one of these, tag it with the rule; when a new pattern emerges across two or more bugs, add a rule.

- **Fidelity-to-source-API is P0.** If the shim's response shape, header set, status code, error envelope, or async-operation semantics diverge from the cloud's published API, that is a P0 bug. The spec is the contract; deviation isn't a feature.
- **No fakes, no fallbacks, no skips.** Synthetic responses, hardcoded values, in-memory state where a real backend was specified, conditional `t.Skip` for missing config — all file as bugs and get real fixes. Tests run or fail loud; never silent.
- **Out-of-intersection features must fail loud in the source cloud's error vocabulary.** Fabricating a success response, returning a generic 500, or silently degrading to a partial result are all bugs. The correct answer is the cloud's own "feature not supported" or equivalent.
- **Each shimmed operation requires SDK + CLI + Terraform conformance in the same commit.** A merged operation without all three driver tests is a bug (per [AGENTS.md](AGENTS.md) testing contract).
- **K8s peer parity.** When a shimmed operation works against AWS / GCP / Azure backends but not the K8s peer (and no documented platform limitation explains the gap), that is a bug — not an "optional surface."
- **Spec drift.** When the upstream cloud spec changes shape (new fields, renamed operations, deprecated paths), the codegen pipeline must regenerate and the translation table must be updated in the same PR. Stale generated code is a bug.
- **Cross-backend sweep on every find.** When a translation gap or fidelity defect appears in one (source, backend) pair, the same code paths in the other backend pairs for that service get checked in the same commit.
- **Recorded-interaction drift.** When a cassette / VCR recording silently masks a real-cloud behavior change, the test is a bug. Nightly live runs are the authoritative tier.
- **Incompatible-license dependency.** Adding a Go module whose license is not on the [`doc/COMPATIBLE_LICENSES.md`](doc/COMPATIBLE_LICENSES.md) allowlist is a bug — it would silently break shimanism's AGPL-3.0 license. CI's `licenses` job blocks it. Connected services (not linked) are exempt; the distinction is in the doc.

## Resolved history (compressed)

| ID | Sev | Area | Source-API | One-liner |
|----|-----|------|------------|-----------|
| 1 | P2 | restxml router | AWS S3 | Router required-only matching meant `GET /{Bucket}/{Key+}?tagging=` (with no GetObjectTagging route registered) silently fell through to GetObject. Fix: `restxml.RouteOptions.ForbiddenQueries` rejects routes when any named query is present; codegen emits the S3 feature-query list for the base object/bucket ops. Plus added GetObjectTagging + GetObjectAcl as object-level probes (canonical "no tags" / "default ACL") so the TF AWS provider's aws_s3_object Read step gets a faithful response instead of a 404. Phase 1.12, on PR #6. |
| 9 | P2 | apigateway/gcp-frontend | GCP API Gateway REST | GCP frontend only had the `Gateways` family; missing `Apis` + `ApiConfigs`. Phase 9.x added them: ApiConfigs.Create parses an OpenAPI 2.0 / 3.0 YAML document, extracts (method, path, x-google-backend.address) tuples into `domain.Route`, and dispatches to `domain.DeployGateway`. SDK conformance test `TestGCPSDK_APIGateway_ApiConfigRouteDeploy` exercises the full Api → ApiConfig → DeployGateway flow. |
| 10 | P2 | apigateway/azure-frontend | Azure APIM REST | Azure frontend only had the `Apis` family; missing `Operations` subresource. Phase 9.x added Operations.{CreateOrUpdate, Get, Delete, List}. Each Operation maps to one `domain.Route` keyed by the operation ID; CreateOrUpdate merges into the existing route table and dispatches `domain.DeployGateway`. Domain `Route` gained an optional `ID` field so frontends can correlate per-route mutations through stateless reads (no shim-side mapping). SDK conformance test `TestAzureSDK_APIGateway_OperationsRouteDeploy` exercises the flow. |
| 11 | P3 | apigateway/aws-frontend | AWS APIGW v2 restJson1 | Error envelope now includes the `__type` field in the JSON body in addition to the existing `X-Amzn-Errortype` header, matching real APIGW v2 fidelity. |
| 5 | P3 | rdbms / cache / functions / apigateway (GCP frontends) | GCP long-running `Operations.Get` | Closed in Phase 10.1: all four GCP-shape frontends now implement `/v1/projects/{p}/operations/{op}` (and `/v2/.../operations/{op}` for Cloud Run, `/v1/projects/{p}/locations/{l}/operations/{op}` for Memorystore / API Gateway). The Operation `Name` encodes `(opType, target)` so a polling client resolves current status by re-reading the underlying resource — stateless, no shim-side operation table. For delete ops, NoSuchInstance signals DONE; for other ops the resource's domain `Status` maps to RUNNING / DONE. |

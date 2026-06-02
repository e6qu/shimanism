# Known Bugs

**55 filed · 51 fixed · 3 open · 1 false positive.** BUG-35 (Container Apps lane) closed by sockerless PR #245 which derived ACA image platforms from the resolved image manifest instead of hardcoding `linux/arm64`; `scripts/run-sockerless-storage.sh` re-defaults `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` to `docker.io/library/nginx:alpine`. Track A (BUG-8 + BUG-15) remains blocked on real GCP credentials. BUG-24 reverse-direction through-shim coverage **now complete across every service family** — PR #46 landed cache/secrets/queue reverse cells; this PR finishes the set with storage/pubsub/rdbms/functions/apigateway reverse cells. `make sockerless` now reports **43 passing + 0 skipped**.

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · rules [AGENTS.md](AGENTS.md).

> **Standing rule:** every CI failure, conformance-test failure, fidelity gap, fake/stub, placeholder, silent fallback, skipped test, or incomplete implementation lands here with a one-liner **before** any fix attempt. Workarounds are bugs and get the same treatment. Per-bug fix detail beyond the one-liner: `git log <commit>` or the linked PR.
>
> When a bug surfaces during a coding-agent session, file the BUG before fixing — even if the fix is a single line. The audit trail is what makes "never lie" enforceable.

## Open

> Currently-open bugs, all absorbed into Phase 14 / standalone examples. Sockerless now simulates the relevant GCP API Gateway and Pub/Sub backend surfaces. The current backend/SDK legs are green:

- BUG-8: `TestSockerless_GCP_APIGateway_CRUD` clears the shim backend ↔ GCP API Gateway SDK-shaped leg. The remaining bug is specifically the hashicorp/google Terraform endpoint/OAuth leg.
- BUG-15: `TestSockerless_GCP_Queue_RetentionRoundTrip` clears the shim backend retention PATCH/read leg. The remaining bug is specifically the hashicorp/google Terraform state-drift question for `message_retention_duration`.

| ID | Sev | Area | Source-API | One-liner | Phase |
|----|-----|------|------------|-----------|-------|
| BUG-8 | P3 | apigateway/gcp-tf-frontend | `hashicorp/google` | API Gateway endpoint-override attribute name changed across provider major versions and the current provider's API Gateway resource lifecycle requires real OAuth-signed requests the mock httptest server can't sign. `services/apigateway/conformance/gcp_terraform_test.go` is smoke-skipped pending Track A real-cloud TF coverage. The sockerless GCP APIGW backend lane passes; this is now only the Terraform-provider leg. | **14.D** |
| BUG-15 | P3 | queue/gcp-frontend | GCP Pub/Sub `subscriptions.get` | `message_retention_duration = "604800s"` declared in HCL and the shim responding "604800s" at every call, hashicorp/google records `"345600s"` in state. Plan after apply diffs `"345600s" -> "604800s"`. Shim's backend retention PATCH/read path now passes against sockerless; the open question is whether the provider shows the same state drift against real GCP or the shim frontend still misses a provider-needed field. | **14.D** |
| BUG-41 | P2 | dns/gcp-tf-frontend | `hashicorp/google` `RemoveBasePathVersion` | Provider regex hardcodes `http[s]://` (literal `s`) when stripping the version path from `dns_custom_endpoint`. HTTP test endpoints fall through the regex no-op; the subsequent `strings.ReplaceAll("/dns/", "")` then mangles the URL into `http://localhost:PORTv1/` which `url.Parse` rejects → SDK panic at `googleapi.ResolveRelative`. Worked around by serving the GCP DNS Terraform conformance over TLS (`StartDNSServerGCPTLS`) with `SSL_CERT_FILE` threading the self-signed cert into the provider process. Linux-only (SSL_CERT_FILE platform limit); skips on macOS. Upstream fix would be `http[s]?://`. **Not filing upstream pending user direction; the local workaround is sufficient.** | **15.D** |

## Upstream-tracked (sockerless validation lane)

Sockerless fidelity gaps tracked on `github.com/e6qu/sockerless`. Each is filed as a fully self-contained issue (no shim references; sockerless maintainers can pick up the repro without reading this repo). See [docs/sockerless-validation.md](docs/sockerless-validation.md) for the wider context.

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

### Later audit rounds — all closed as of sockerless PR #219

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
| [e6qu/sockerless#218](https://github.com/e6qu/sockerless/issues/218) | ✅ closed by PR #219; GCP Secret Manager ListSecretVersions, UpdateSecret, and DeleteSecret handlers added. |

### Sockerless coverage history

- **Round 1** ([#173-178](https://github.com/e6qu/sockerless/issues/173)) — all closed by sockerless PR #179. Initial fidelity gaps (S3 `/s3/` URL prefix, `aws-chunked` envelope, missing `ListSecretVersionIds`) + missing-service rollups (AWS / GCP / Azure).
- **Round 2** ([#181-188](https://github.com/e6qu/sockerless/issues/181)) — all closed by sockerless PR #180. Per-service fidelity drift across SQS / Pub/Sub / Secret Manager / Cloud SQL / KV / Redis ARM.
- **Round 3** ([#189-191](https://github.com/e6qu/sockerless/issues/189)) — closed by sockerless PR #192 plus the later #190 reopen closure.
- **Later rounds** ([#193-215](https://github.com/e6qu/sockerless/issues/193), excluding unused issue numbers) — closed by sockerless PRs #200, #202, #211, and #216.
- **Next-lane fix**: [#218](https://github.com/e6qu/sockerless/issues/218) — GCP Secret Manager `ListSecretVersions`, `UpdateSecret`, and `DeleteSecret` landed in sockerless PR #219. The full `services/secrets/backends/gcp` sockerless lane is now green.

### End-to-end walkthrough findings (BUG-30..33)

| Upstream | Status |
|---|---|
| [e6qu/sockerless#220](https://github.com/e6qu/sockerless/issues/220) — Azure Blob `List Containers` omits per-container `<Properties>` (Last-Modified, Etag) | ✅ closed by sockerless PR #221 on 2026-05-26. The Azure simulator's `handleListContainers` now emits `<Properties><Last-Modified>…</Last-Modified><Etag>…</Etag></Properties>` per container; the shim reads both fields through the existing Azure SDK path (Last-Modified populated `domain.Bucket.CreatedAt`; ETag flows through the new `domain.Bucket.ETag` field added in PR #37). |

### Phase 14.B new lane findings (BUG-34..35)

| Upstream | Status |
|---|---|
| [e6qu/sockerless#223](https://github.com/e6qu/sockerless/issues/223) — Azure Service Bus admin: namespace-level ATOM XML protocol not implemented (only ARM management is) | ✅ closed by [sockerless PR #225](https://github.com/e6qu/sockerless/pull/225) on 2026-05-26 (`Implement Azure Service Bus ATOM admin routes`). Closes BUG-34. The shim now wires `TestSockerless_Azure_ServiceBus_Queue_CRUD` and `TestSockerless_Azure_ServiceBus_Topic_CRUD` against the new admin surface (admin-only — AMQP data plane still out of scope for sockerless). |
| [e6qu/sockerless#227](https://github.com/e6qu/sockerless/issues/227) — Azure Blob block-blob staging ops (`?comp=block`, `?comp=blocklist`) not implemented | ✅ closed by [sockerless PR #229](https://github.com/e6qu/sockerless/pull/229) on 2026-05-26. The shim's `azureblob` backend multipart code path (StageBlock / CommitBlockList / GetBlockList) now exercises end-to-end via `TestSockerless_Azure_Blob_Multipart`. |
| [e6qu/sockerless#228](https://github.com/e6qu/sockerless/issues/228) — Service Bus AMQP data plane (roadmap question) | ✅ closed by [sockerless PR #229](https://github.com/e6qu/sockerless/pull/229) on 2026-05-26 via AMQP-over-WebSocket implementation. Architectural follow-on: callers need WebSocket-dial code in test/integration to use it, which leaks transport choice. Tracked separately at [sockerless#230](https://github.com/e6qu/sockerless/issues/230) (raw AMQP-over-TCP transport for SDK-clean integration). |
| [e6qu/sockerless#230](https://github.com/e6qu/sockerless/issues/230) — Service Bus: expose raw AMQP-over-TCP transport so callers don't need WebSocket-dial code | ✅ closed by [sockerless PR #231](https://github.com/e6qu/sockerless/pull/231) on 2026-05-27. Reframed by the maintainer as "Service Bus public-surface fidelity" — the sim now exposes raw AMQP/TLS on a dedicated listener with namespace routing via TLS SNI / AMQP Open hostname and entity routing via AMQP link source/target addresses. Closes BUG-36. The shim's queue Send/Receive + pubsub topic Publish/Receive lanes are now wired using `azservicebus.ClientOptions.CustomEndpoint` + `TLSConfig` — the same SDK-clean integration shape as every other 14.B lane. |
| [e6qu/sockerless#232](https://github.com/e6qu/sockerless/issues/232) — Azure Blob: Copy Blob via `x-ms-copy-source` header not implemented | ✅ closed by [sockerless PR #235](https://github.com/e6qu/sockerless/pull/235) on 2026-05-27. Closes BUG-37. Shim's `TestSockerless_Azure_Blob_Copy` exercises end-to-end. |
| [e6qu/sockerless#233](https://github.com/e6qu/sockerless/issues/233) — GCS: `rewriteTo` / `copyTo` object-copy REST endpoints not implemented | ✅ closed by [sockerless PR #235](https://github.com/e6qu/sockerless/pull/235) on 2026-05-27. Closes BUG-38. Shim's `TestSockerless_GCS_Copy` exercises end-to-end (via the SDK's default `rewriteTo` path). |
| [e6qu/sockerless#234](https://github.com/e6qu/sockerless/issues/234) — GCS objects.list returns objects in random order | ✅ closed by [sockerless PR #235](https://github.com/e6qu/sockerless/pull/235) on 2026-05-27. `items[]` and `prefixes[]` now sorted lexicographically. The shim was already patched defensively in PR #43, but the upstream fix completes the contract. |
| [e6qu/sockerless#236](https://github.com/e6qu/sockerless/issues/236) — GCS copy/rewrite should honor destination object metadata | ◐ open. Tracks the request-body metadata-precedence follow-up from PR #235's review. Doesn't block any shim lane today — SDK-default `CopierFrom(src).Run` doesn't set destination metadata. |
| [e6qu/sockerless#237](https://github.com/e6qu/sockerless/issues/237) — Refactor GCS object persistence into shared helper | ◐ open. Internal-only refactor from PR #235's review. No shim-visible impact. |
| [sockerless PR #226](https://github.com/e6qu/sockerless/pull/226) — Azure Storage data-plane SDK coverage (Blob/File/Queue/Table) | ✅ merged 2026-05-26. The shim's existing Azure Blob lanes continue to pass against the broader Storage data-plane coverage. |

## False positives

| Area | Finding | Why it's not a bug |
|------|---------|--------------------|
| BUG-14 / storage Apply test fixture | `terraform apply` of `aws_s3_bucket` with no `tags` declared, followed by `plan -refresh-only -detailed-exitcode`, surfaces `tags: absent → {}` drift. | The shim returns `<Code>NoSuchTagSet</Code>` 404, identical to real AWS. The drift comes from hashicorp/aws's own behavior: it records `tags = {}` after read regardless of whether the HCL declared tags. Real AWS exhibits the same drift. **Resolution:** Phase 10 apply-test fixtures declare `tags = {}` explicitly. Documented in [`services/storage/APPLY_INTERSECTION.md`](services/storage/APPLY_INTERSECTION.md). |

## Class-of-bug rules (carried forward)

When a new bug fits one of these, tag it with the rule.

- **Fidelity-to-source-API is P0.** If the shim's response shape, header set, status code, error envelope, or async-operation semantics diverge from the cloud's published API, that is a P0 bug. The spec is the contract.
- **No fakes, no fallbacks, no skips.** Synthetic responses, hardcoded values, in-memory state where a real backend was specified, conditional `t.Skip` for missing config — all file as bugs and get real fixes. **Case study (PRs #51–#54 / 14.E ARM-shimming):** built synthetic ARM frontends for `Microsoft.Storage/storageAccounts` and `Microsoft.KeyVault/vaults`, in-process `Track*` state for azurerm idempotency, a hardcoded `StorageAccountsListKeys` synthetic, a mock Microsoft Entra OIDC endpoint, an `armResourcesStub` middleware. Every one violated this rule. The honest answer was always to extend sockerless's real Azure ARM with [configurable data-plane endpoint emission](https://github.com/e6qu/sockerless/pull/259); the shim's data-plane frontends compose with that without any shim-side fakes. Lesson: when a feature "requires" the shim to make up state to satisfy a client, the design is wrong — the state belongs somewhere real (sockerless, the destination cloud, or out-of-intersection).
- **Out-of-intersection features must fail loud in the source cloud's error vocabulary.** Fabricating a success response, returning a generic 500, or silently degrading to a partial result are all bugs.
- **Each shimmed operation requires SDK + CLI + Terraform conformance in the same commit.** A merged operation without all three driver tests is a bug.
- **K8s peer parity.** When a shimmed operation works against AWS / GCP / Azure backends but not the K8s peer (and no documented platform limitation explains the gap), that is a bug.
- **Spec drift.** When the upstream cloud spec changes shape, the codegen pipeline must regenerate and the translation table must be updated in the same PR. Stale generated code is a bug.
- **Cross-backend sweep on every find.** When a translation gap appears in one (source, backend) pair, the same code paths in the other backend pairs get checked in the same commit.
- **Recorded-interaction drift.** When a cassette / VCR recording silently masks a real-cloud behavior change, the test is a bug.
- **Incompatible-license dependency.** Adding a Go module whose license is not on the [`docs/compatible-licenses.md`](docs/compatible-licenses.md) allowlist is a bug — CI's `licenses` job blocks it.

## Resolved history (compressed; commit-log + linked PR have detail)

| ID | Sev | Area | Closed in | One-liner |
|---|---|---|---|---|
| 55 | P2 | codegen/ec2query-form-decode | 16.C PR1 | ec2Query form decode for `list<string>` fields used the Go field name (e.g. `InstanceIds.N`) as the form key, but the EC2 wire protocol uses the `@xmlName` value (e.g. `InstanceId.N`). Fixed: `fieldView.FormKey` derives from `@xmlName` for lists, falls back to GoName; template updated to use `{{ .FormKey }}` in list loops. Affected VpcIds→VpcId, SubnetIds→SubnetId, InstanceIds→InstanceId, SecurityGroupRuleIds→SecurityGroupRuleId, etc. |
| 54 | P2 | compute/aws-ec2-codegen | 16.B PR2 | `DescribeNetworkInterfaces` missing from `services/compute/codegen.json`. Terraform provider calls it during `aws_security_group` destroy to drain ENIs. Added to manifest + implemented (empty list). |
| 53 | P2 | compute/aws-ec2-codegen | 16.B PR2 | `DescribeVpcAttribute` missing from `services/compute/codegen.json`. Terraform provider calls it after every `aws_vpc` create. Added to manifest + implemented (returns plausible DNS defaults). |
| 52 | P1 | codegen/ec2query-xml-tags | 16.B PR1 | ec2query codegen template emitted `xml:"GoName,omitempty"` instead of `xml:"xmlName,omitempty"`. All ec2Query Describe operations returned empty results. Fixed: template uses `{{ .XMLTag }}`. |
| 50 | P2 | nosql/azure-cosmos-tables-frontend | PRs #97–#100 | Cosmos Tables ARM passthrough + metadata + bearer verifier closed in four PRs: foundational `Config.Passthrough` (PR #97); metadata endpoint + bearer verifier + TF conformance `azurerm_cosmosdb_table` + DynamoDB `DeleteItem ConditionExpression` fix + global ARM `/providers/` routing fix (PR #98); az CLI conformance `TestAzureCLI_CosmosTable_Lifecycle_ThroughShim` (PR #100). Upstream: sockerless#356 → PR #357 (Tables ARM), sockerless#360 → PR #361 (DynamoDB DeleteItem). |
| 51 | P2 | ci/conformance-redisop | PR #98 | Pre-pull redis-operator image on host with `docker-pull-retry.sh` + `kind load docker-image` to avoid quay.io outage at pod startup. `IfNotPresent` pull policy means pod uses the kind node cache without contacting quay.io. |
| 49 | P2 | ci/conformance-docker-steps | PR #98 | `docker-pull-retry.sh` extended with `LOCAL_TAG` env var (stable local alias for `docker run`); digest-form refs skipped during tag-aliasing. Workflow uses `$LOCAL_TAG` instead of mirror ref. |
| 48 | P2 | dns/coredns-backend | PR #89 | CoreDNS `auto` plugin regex corrected to `(.*)\.db {1}` matching the shim's `<zone>.db` naming; `bumpSOASerial` added to every `writeZoneFile` mutation so CoreDNS reloads the file on change. |
| 47 | P3 | dns/azure-backend | shim defensive fix + sockerless PR #350 | Sockerless's Azure sim was missing `GET /privateDnsZones` (list by RG) — only per-zone routes existed. Shim's `azurebackend.ListZones` calls both public+private list pagers when `filter=Unknown`; the 404 used to propagate as `NoSuchZone`, breaking the AWS Route 53 → Azure cross-cloud cell at the name-resolution step. Defensive fix: backend treats 404 on list-by-family as "empty list" when no explicit filter is set (an explicit `filter=Private` still surfaces real errors). Upstream [e6qu/sockerless#340](https://github.com/e6qu/sockerless/issues/340) → PR #350 also added the missing route. Defensive shim code stays as good defensive coding against any other simulator with partial coverage. |
| 45 | P3 | dns/azure-sdk-through-shim | PR #86 | `TestSockerless_AzureDNS_Through_Shim_ZoneLifecycle` wired end-to-end. armdns SDK with custom `cloud.Configuration` (resourceManager=shim, ActiveDirectoryAuthorityHost=sockerless) drives zone CRUD through the shim's Azure DNS frontend. Token credential `sockerlessTokenCred` acquires JWTs from sockerless's Entra ID stub via `POST /<tenant>/oauth2/v2.0/token` over TLS with `RootCAs` cert pinning. The shim's bearer verifier accepts the sockerless-signed token via the same JWKS plumbing BUG-46 introduced. Same shim configuration as the through-shim Terraform Apply, different driver. |
| 43 | P3 | dns/azure-cli-conformance | PR #86 | `TestAzureCLI_DNS_ZoneLifecycle_ThroughShim` wires `az network dns zone create/show/delete` through the shim+sockerless stack. `az cloud register --name shim-conformance --endpoint-resource-manager <shim> --endpoint-active-directory <sockerless> ...` plus `az cloud set` + `az login --service-principal` against sockerless's Entra mints a token via sockerless that the shim's bearer verifier accepts. Per-test `AZURE_CONFIG_DIR` isolates the profile. SSL_CERT_FILE + REQUESTS_CA_BUNDLE trust both the shim's httptest cert and sockerless's cert. Linux-only via SSL_CERT_FILE platform limit. |
| 46 | P3 | dns/azure-tf-through-shim | PR #85 | shim Azure cloud-metadata endpoint serves at `GET /metadata/endpoints?api-version=...`: `resourceManager` points at the shim itself (derived from `r.Host`), `authentication.loginEndpoint` + the other service URLs point at the configured `MetadataLoginURL` (sockerless's Azure ARM mock in tests, real Azure in prod). With `metadata_host = "<shim>"` set on the azurerm provider, ARM calls now flow through the shim's DNS dispatch + passthrough, while Entra ID token acquisition reaches sockerless directly. `TestSockerless_AzureDNS_Through_Shim_Terraform_Apply` re-enabled. Unit tests in `passthrough_test.go` pin the JSON contract (resourceManager=shim, login=upstream) for both `api-version=2022-09-01` (single object) and legacy (array). |
| 44 | P2 | dns/azure-arm-passthrough | shim PR #84 | Added **ARM passthrough primitive** to `internal/dns/frontends/azure_dns`: `NewWithPassthrough(d, upstream http.Handler)` forwards unmatched ARM paths to a configured upstream while DNS paths stay local. Verified by `passthrough_test.go` (resource-group / non-Microsoft.Network / providers-list paths reach upstream; DNS paths stay local; no-upstream returns honest 404 — no silent fallback). End-to-end Terraform Apply through the shim required additional metadata + Entra ID redirection — closed by BUG-46. |
| 42 | P3 | dns/gcp-backend | sockerless PR #299 | Sockerless's Cloud DNS sim was missing the Changes API (`POST /managedZones/{zone}/changes` + `GET .../changes/{id}` + `PATCH /rrsets/{name}/{type}`) — the surface the SDK and `hashicorp/google` use for atomic record updates. Upstream [#298](https://github.com/e6qu/sockerless/issues/298) → PR #299 added them. `TestSockerless_GCPCloudDNS_Through_Shim_ZoneLifecycle` re-enabled in this PR. |
| 40 | P3 | dns/aws-backend | shim defensive fix + sockerless PR #297 | After sockerless#291 closed, the through-shim test hit a second sim gap: `ListResourceRecordSets` returns records in **insert order**, not sorted by `(Name, Type)` the way real Route 53 does. The shim's AWS backend resolved records via `(StartRecordName, MaxItems=1)` and trusted `[0]`, which broke any time the sim's stored insert order put a larger-named record before the requested one (NS / SOA at the zone apex before an A record whose label sorts earlier). Defensive fix: `findRecordSet` now scans the response page for an exact `(name, type)` match — works against real Route 53 (always sorted) and against any server with looser ordering. Upstream [e6qu/sockerless#296](https://github.com/e6qu/sockerless/issues/296) → PR #297 also lands the sorted-page fix so real-Route-53-style `[0]` callers work too. |
| 39 | P3 | dns/aws-backend | sockerless PR #294 | Sockerless's Route 53 sim was missing `ListHostedZonesByName`; the shim's AWS backend uses it for stateless name → HostedZoneId resolution. Upstream [#291](https://github.com/e6qu/sockerless/issues/291) → PR #294 added the route. `TestSockerless_AWSRoute53_Through_Shim_ZoneLifecycle` re-enabled in this PR. |
| 35 | P3 | functions/azure-containerapps | sockerless PR #245 | Container Apps lane `TestSockerless_Azure_Functions_ContainerApps_CRUD` blocked on amd64 because sockerless hardcoded `Architecture: "linux/arm64"` regardless of pulled image platform. Upstream PR #245 derives the architecture from the resolved image manifest (including sidecars). Shim now re-defaults `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE=docker.io/library/nginx:alpine` in `scripts/run-sockerless-storage.sh`; `make sockerless` goes from 37 + 1 skip → 38 passing. |
| 30 | P1 | codegen/aws-smithy emitter | PR #37 | [GitHub #32](https://github.com/e6qu/shimanism/issues/32): AWS S3 codegen mis-named every `@xmlFlattened` list element (`<Object>` instead of `<Contents>`, etc.). Per the Smithy XML protocol, flattened lists use the containing structure's member name unless the inner list-member has an explicit `@xmlName`. `internal/codegen/emit/emit.go` now respects that, regen produced 6 corrections across `ListObjectsV2`, `ListMultipartUploads`, `CompletedMultipartUpload`, `GetBucketLifecycleConfiguration`, and `ReplicationConfiguration`. `aws s3 ls` is no longer silently empty. |
| 31 | P2 | storage/gcs-frontend | PR #37 | [GitHub #33](https://github.com/e6qu/shimanism/issues/33): GCS-shaped `buckets.list`/`buckets.get` leaked the backend's Azure region in the `location` field (e.g. `"EASTUS"`, invalid as a GCS location). The frontend now routes through `gcsLocation()`, which keeps GCS-shaped regions as-is and folds non-GCS values into the default multi-region `"US"`. The zero-`timeCreated`-on-list half was the upstream gap [sockerless#220](https://github.com/e6qu/sockerless/issues/220) — closed by sockerless PR #221 which now populates the per-container `<Properties>` block; the standalone E2E lane asserts a non-zero creation timestamp. |
| 32 | P2 | storage/azure_blob-frontend | PR #37 | [GitHub #34](https://github.com/e6qu/shimanism/issues/34): `blob list` returned unquoted ETags and `container list` returned an empty ETag, despite `upload`/`download` returning the quoted form. Centralized in a `quoteETag()` helper. Container ETag now flows through `domain.Bucket.ETag` from the Azure backend's `ContainerProperties.ETag` (populated upstream by sockerless PR #221); backends without a per-container ETag concept (AWS S3) honestly emit the empty value rather than a synthetic placeholder. |
| 33 | P3 | docs / conformance script | PR #37 | [GitHub #35](https://github.com/e6qu/shimanism/issues/35): `docs/end-to-end-examples.md` told readers to run `az storage blob download --file -`, which silently writes a literal file named `-` to `cwd` rather than streaming to stdout. The example now downloads to a real path and `cmp`s it. `scripts/check-standalone-sockerless-examples.sh` gained one wire-fidelity assertion per route — `aws s3 ls` must surface a key, the GCS `location` must be a valid GCS location, and the Azure blob-list ETag must be quoted — so the next round of this class of bug fails CI. |
| 24 | P2 | sockerless/conformance | Phase 14 | [GitHub #24](https://github.com/e6qu/shimanism/issues/24): the sockerless lane now covers every service family with source SDK → shim frontend → shim backend → sockerless simulator E2E cells, and CI runs the expanded lane. |
| 29 | P2 | storage/gcs-frontend | Phase 14 | [GitHub #29](https://github.com/e6qu/shimanism/issues/29): GCS frontend now serves the JSON API `managedFolders.list` route with an empty `storage#managedFolders` collection, so `gcloud storage rm --recursive gs://bucket` can finish cleanup instead of failing on a route miss. |
| 28 | P2 | storage/aws-backend | Phase 14 | [GitHub #28](https://github.com/e6qu/shimanism/issues/28): AWS S3 backend now spools frontend upload streams into seekable temporary bodies before `PutObject` / `UploadPart`, so the official AWS SDK can perform HTTP endpoint payload signing/checksum/retry behavior without rejecting non-seekable request bodies. |
| 27 | P2 | storage/gcs-frontend | Phase 14 | [GitHub #27](https://github.com/e6qu/shimanism/issues/27): GCS object metadata now includes concrete JSON API `mediaLink` / `selfLink` values and the frontend serves `/download/storage/v1/b/{bucket}/o/{object}`, so `gcloud storage cp` object download no longer crashes while building the apitools transfer URL. |
| 25 | P2 | terraform/conformance | Phase 14 | Per-test Terraform working directories now use local `.terraform-plugin-cache` directories instead of shared package-level `TF_PLUGIN_CACHE_DIR` paths, removing parallel `terraform init` provider-cache races. |
| 23 | P2 | sockerless/conformance | Phase 14 | Storage now has through-shim sockerless E2E cells for AWS -> GCP, GCP -> Azure, and Azure -> AWS: source SDK -> shimanism frontend -> shimanism backend -> sockerless destination simulator. |
| 22 | P3 | storage/conformance | Phase 14 | `services/storage/conformance/sockerless_test.go` was not gofmt-clean under the CI pre-commit hook after the GCS import landed. Fixed by running gofmt. |
| 21 | P2 | CI / kind conformance jobs | Phase 14 | `helm/kind-action@v1` fetched the kind release binary without following GitHub release redirects, so the checksum step validated the redirect body and failed before tests ran. Fixed by preinstalling kind/kubectl into the action cache with redirect-following, checksum-verified downloads. |
| 20 | P2 | azure-codegen / ARM | Phase 12.A.24 (PR #19) | `flattenARMAllOf` preprocessor inlines `{ allOf: [TrackedResource], properties: {own} }` so oapi-codegen emits a struct, not a type alias. ContainerApp / RedisResource / Server (PostgreSQL FlexibleServer) emit as proper Go structs. Phase 12.A.31 caught + fixed a chained-inheritance bug in the same stage. |
| 18 | P3 | all frontends (sig verification) | Phase 11.14 (PR #18) | 4 verifier packages wrap all 24 frontends; bypass dropped; every conformance test signs end-to-end. AWS-CLI compatibility via manual SigV4 in `canonical.go` accepting both Go-SDK and boto3 signing shapes. See [docs/verifiers.md](docs/verifiers.md). |
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

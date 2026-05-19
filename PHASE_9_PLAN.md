# Phase 9 — Cross-cloud Terraform import through shimanism

> **Goal:** When a resource that exists in cloud A is imported via `terraform import` through shimanism's frontend for cloud B (with a configured backend that is cloud A), Terraform sees it as the corresponding **cloud-B-shaped** resource. The user's Terraform state file ends up populated with a `google_storage_bucket` (or `azurerm_storage_container`, or whatever cloud B's resource is) whose attributes reflect the **actual data living in cloud A**, surfaced through the cloud-B wire shape that shimanism implements.
>
> Beyond the standard 3 frontends × 5 backends matrix (now with `terraform import` driver instead of create/destroy), the phase delivers (a) **mock cloud servers** accurate enough to fool the official provider into believing the resource exists upstream, (b) **real-cloud Terraform / CLI / SDK e2e** that exercises the same flow against actual AWS / GCP / Azure accounts, and (c) a **base-URL override mechanism** so the end-user's invocation of `terraform import` (or `aws ec2 ...`, or `gcloud storage ...`) routes through shimanism instead of the native cloud endpoint.

> This is a *test, override, and proof* phase. No new shimmed service. The work proves that the existing 8 services already provide enough fidelity to be transparent to the official tooling under `terraform import` — and where they don't, exposes the gaps as BUGs.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · roadmap [PLAN.md](PLAN.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Where Phase 9 stands (live status on PR #13)

A substantial chunk of Phase 9 landed on the Phase 8 PR per user instruction "keep phase 9 to the same open PR." Concretely:

- **9.0 ✅** This plan + codex review revisions applied.
- **9.1 ✅** `cmd/shimctl` + `internal/clientconfig/overrides.yaml` (per-cloud × per-service endpoint-override registry).
- **9.2 ✅** Importer-read contract traces captured per service in `services/<svc>/conformance/importer_contract.md` (storage, secrets, queue, pubsub, apigateway, functions, rdbms, cache).
- **9.2-A ✅** No-fakes audit + `services/<svc>/INTERSECTION.md` per service. Surfaced and fixed 6 real fidelity bugs (BUG-9/10/11 closed; 6 inline fixes: SNS XML double-nesting + Policy JSON + ListTagsForResource + DisplayName; APIGW selection-expression defaults; 7 Lambda Read-path subresources; RDS DBInstanceArn + DbiResourceId; queue ListQueueTags handler).
- **9.2-B ✅** `services/<svc>/MIGRATION.md` per service — runnable walkthroughs proving the intersection is migration-useful.
- **9.5 ✅** `terraform_import_test.go` per service — 8 single-cloud import tests, all green.
- **9.11 ✅** `cmd/shim mock` subcommand — bundle inmem-backed cloud-shaped APIs on localhost ports.
- **9.13 ✅** Cross-cloud exit criterion — **six services validated**: AWS → GCS (storage), AWS → GCP Pub/Sub (queue), AWS → GCP API Gateway (apigateway), AWS → GCP Memorystore (cache), AWS → GCP Cloud Run (functions), AWS → GCP Cloud SQL (rdbms). Reverse direction (GCS → AWS) skeleton landed, skipped pending hashicorp/google credentials Track A work.

**Filed in BUGS.md (not yet fixed):** BUG-12 (queue domain tag storage), BUG-13 (Lambda memory_size/role/publish defaults).

### Remaining Phase 9 work (next PR after PR #13 merge)

- **9.3** Mock cloud servers as a packaging story (the existing `cmd/shim mock` is the seed).
- **9.7** Full matrix import driver (every frontend × backend cell, all services).
- **9.8 / 9.9** CLI + SDK import smoke (separate from full TF import).
- **9.12** CI lane `conformance-import-matrix`.
- **9.14** Phase 9 closer.

### Phase 9-A (real-cloud, Track A) carry

- Reverse-direction cross-cloud tests once provider credentials handling clears (especially hashicorp/google).
- Real-cloud lanes for storage / secrets / queue / pubsub / rdbms / cache / functions / apigateway behind Track A credentials.
- The pattern proven on PR #13 (mock cloud B = shim frontend over inmem; shim backend points at mock; real provider drives the user-facing shim) extends 1:1 to real cloud B by swapping the mock for the real backend's URL.



## What "intersection" means here — useful for migration, not lowest common denominator

The shim exists to **help users migrate, service by service, between clouds and to/from K8s or on-prem.** A migration user's question is *"can I keep my AWS-shaped Terraform module, point it at a GCS backend through the shim, and have my workloads continue to function?"* The intersection that matters is whatever serves that question — not the trivially overlapping subset of every cloud's API.

Concretely, an operation is **in the intersection** iff:

1. **It moves migration forward.** A user migrating from AWS to GCP needs to read tags, recreate access policies, list versions, configure encryption — these are migration-critical even when the per-cloud shape differs. An operation purely useful for vendor-lock-in features (e.g. AWS-specific S3 Object Lock retention modes) is not.
2. **It has a faithful counterpart on each target backend.** The counterpart can be a different shape — AWS bucket policy ↔ GCS uniform bucket-level access ↔ Azure container public-access level — but it must capture the same *user intent*. If the user-intent capture requires lying about behavior, the operation is **not** in the intersection.
3. **The translation is honest under load and at scale.** A "common" operation that only works for resources smaller than 100MB, or that silently degrades for high concurrency, is not in the intersection. Migration is end-to-end or it isn't.

What this rules in / out:

| Op | Rationale |
|---|---|
| **List buckets, list secrets, list queues, list functions, list gateways.** | Migration core: a user has to enumerate before they migrate. **In.** |
| **Read + write tags / labels / metadata.** | Migration core: ownership + cost-attribution + lifecycle hooks all key off these. **In** for every service. |
| **Read + write resource-level access policies (intent, not bytes).** | Migration core: rebuilding security posture. Per-cloud shape differs; the *intent* maps. **In** at intent level — verbatim policy-byte round-trip is a non-goal. |
| **Set per-object server-side encryption.** | Migration core: data-at-rest compliance. **In.** Encryption-key-source quirks (AWS KMS keys vs GCS CMEK vs Azure CSE) are translated at intent level. |
| **AWS S3 Object Lock with compliance mode.** | Vendor-specific compliance feature, no honest cross-cloud counterpart. **Out of intersection** — returns `NotImplemented` honestly when requested. |
| **GCP requester-pays buckets.** | Vendor-specific billing model. **Out** — returns GCP's `requesterPaysNotEnabled` envelope honestly. |
| **APIM per-policy XML transform pipelines.** | Vendor-specific request-mangling DSL. **Out**, returns Azure's `BadRequest:NotSupported`. |
| **Per-route auth at gateway level.** | Migration-critical *but* Phase 8 deferred it to Phase 8.5+. **Currently out**; tracked. |

**The 9.2-A intersection inventory therefore captures three things per op**: (a) what the source cloud calls it; (b) what the cross-cloud user-intent is, if any; (c) which backend counterparts realize that intent (and how). When (b) doesn't exist, the op is out of intersection by definition — the shim then returns the source cloud's "not supported" envelope, not a placeholder.

### Migration utility audit (Phase 9 sub-phase 9.2-B)

After the no-fakes audit (9.2-A) classifies every op, sub-phase 9.2-B re-evaluates the *intersection itself* through the migration lens:

- **Per-service migration scenario walkthrough.** For each shimmed service, write a 1-page "migration walkthrough" at `services/<svc>/MIGRATION.md`: AWS → GCP, AWS → Azure, AWS → K8s peer (and the reverse cells). What ops does the migration use? Are they all green? Where it uses an op currently marked "out of intersection," is the migration *actually impossible without it*, or just less convenient?
- **Findings either expand the intersection or stay out.** If a walk-through reveals an op that's migration-critical and *does* have honest cross-cloud counterparts (we just didn't ship it), file a BUG + reclassify it as in-intersection + schedule the implementation work. If the op's "counterpart" turns out to require lying, leave it out and document why.
- **Existing Phase 1–8 ops re-evaluated under the same lens.** A few cells today are "in intersection because the SDK calls them" rather than "in intersection because migration needs them." The audit re-asks.

This is what separates shimanism from "yet another cloud API gateway." The goal is migration, not API mimicry.

## Hard invariant: every intersection endpoint does real work

A reinforcement of [AGENTS.md § No fakes. No stubs. No mocks. No silent fallbacks. Ever.](AGENTS.md#no-fakes-no-stubs-no-mocks-no-silent-fallbacks-ever) — directly applicable to Phase 9:

**Every URL path / RPC method / wire endpoint that the official CLI, SDK, or Terraform provider calls — when that endpoint is part of the cross-cloud intersection of functionality — must be backed by a real shim translation to a real backend.** No `return &T{}, nil` placeholders. No `204 No Content` shortcuts. No quietly-empty list responses. No "we'll wire it next phase."

The three classes from the importer-read contract (above) are the *only* legitimate response shapes:

1. **Backed by real backend state.** Tags, region, metadata, ARN-equivalents, etc. The shim's domain must actually persist + return them. If the inmem backend doesn't store the field today, sub-phase 9.2 surfaces it and 9.3 makes inmem store it. Inmem is a *real* fixture — the bytes / metadata it holds are the truth for the test, just as a real S3 bucket's state is the truth for production.
2. **Feature genuinely not configured.** A brand-new bucket has no `BucketPolicy`; AWS returns `NoSuchBucketPolicy`. That is not a fake — it is the **real, true answer** ("this feature is unset on this resource"). The shim must produce the source cloud's *actual* "unset" envelope, byte-for-byte. A 200 with empty body is a fake; a 404 with the wrong code is a fake; only the correct error envelope counts.
3. **Out of intersection.** AWS-only object-lock, GCP-only resource-tagging, Azure-only customer-managed soft-delete. The shim returns the source cloud's `NotImplemented` / `OperationNotSupported` / `BadRequest:NotConfigured` error verbatim. Out-of-intersection ≠ "we didn't bother" — it means *this feature has no honest cross-cloud counterpart*, and the user gets told so in the cloud's own error vocabulary.

There is no fourth category. **A handler that returns "something that looks plausible" without driving real backend logic is a fake** and must be removed or BUGed.

### Audit + remediation pass (Phase 9 sub-phase 9.2-A)

Inserted between the importer-read contract trace and the mock implementation: a directed **audit** of every existing frontend's handlers (Phases 1–8 services × all 3 frontends) for any handler that:

- Returns a hard-coded payload without consulting the backend.
- Swallows a backend error and returns a synthesized success.
- Discards request fields silently (e.g. tags supplied on `CreateBucket` not stored on the inmem `Bucket`).
- Returns the wrong "unset" envelope (200 instead of 404, generic 500 instead of `NoSuchBucketPolicy`, etc.).

Each finding files a BUG in [BUGS.md § Open](BUGS.md#open) **before** the fix lands. The audit's exit gate: every operation the CLI / SDK / TF provider importer hits resolves to category 1, 2, or 3 above — with no exceptions. The audit also runs against the intersection inventory below.

### Intersection inventory (per-service deliverable)

A new file per service: `services/<svc>/INTERSECTION.md`. Three columns:

| Cloud-A wire op | Cross-cloud meaning | Status |
|---|---|---|
| `GET /v1/projects/{p}/locations/{l}/gateways/{g}` (GCP) | Describe a Gateway | **In intersection** — frontend dispatches to `domain.DescribeGateway`. |
| `PUT /…/Microsoft.ApiManagement/service/{s}/apis/{a}/policies/{policyId}` (Azure) | Apply per-API policy (auth/transform/rate-limit) | **Out of intersection** — Phase 8's intersection is method+path+backend only. Returns `BadRequest:NotSupported`. |
| `GET /v1/projects/{p}/locations/{l}/gateways/{g}/iamPolicy` (GCP) | Read IAM bindings | **Out of intersection** today; in scope only when an IAM intersection lands. Returns `NOT_IMPLEMENTED` per GCP error envelope. |

Every operation the importer-read contract surfaces must appear in the inventory with one of the three statuses. The inventory + the audit are what Phase 9 hands to a future Phase that wants to add a new feature to the intersection — the gap is already inventoried.

## Why import (and why now)

`terraform import` is the canonical "the resource already exists; tell me about it" operation. Unlike `terraform apply` (which the shim has been driving since Phase 1) **import does not create or modify anything** — the provider issues a `GetResource` against the cloud's wire endpoint and adopts the returned attributes verbatim. This is the single most demanding fidelity test:

- A wrong field name, a missing optional, a date in the wrong format → import fails *or* worse, the next `apply` proposes a no-op diff with corruption.
- Async-operation polling shortcuts don't apply (import is read-only).
- Auth flows must complete (the provider signs its requests; the shim must accept the signature).

Phases 1–8 prove create/list/get/delete work. Phase 9 closes the loop: *can the shim be the entire data source for an existing-state adoption?*

## Scope

| | |
|---|---|
| Services in scope | All 8 (storage, secrets, queue, pubsub, rdbms, cache, functions, apigateway). |
| Operation in scope | `terraform import` per shimmed primitive resource type, per frontend. **Read paths only** — `get`, `describe`, `list`. |
| Out of scope | Provider versions older than the ones used in Phases 1–8. Multi-region import. Cross-account import. Drift reconciliation (`terraform plan -refresh-only`). |
| Driver matrix | 3 frontends (AWS / GCP / Azure) × 5 backends (inmem, K8s peer, AWS, GCP, Azure) × 3 driver types (SDK Get/Describe, CLI describe, Terraform import). Across 8 services that's **3 × 5 × 3 × 8 = 360 cells**, but many already share scaffolding with Phases 1–8's create/list cells. |
| Real-cloud lanes | New Track A lanes per cloud: `import-real-aws`, `import-real-gcp`, `import-real-azure`. Each runs only when its corresponding secret is set in the repo. |

## Hard problems & how each is approached

### 1. Base-URL override mechanism

End-user tools all support endpoint overrides but the syntax differs widely. Phase 9 provides a single repo-wide pattern: a `shimctl env` helper that emits a shell snippet exporting the right env vars + provider config snippet for the chosen frontend.

```
$ eval "$(shimctl env --frontend=aws_s3 --endpoint=http://shim:9000)"
# now `aws s3 ...`, `terraform plan` with hashicorp/aws, and any
# aws-sdk-go-v2 client all hit the shim.
```

| Frontend | Mechanism |
|---|---|
| AWS (any service) | `AWS_ENDPOINT_URL_<SERVICE>=<shim>` env var (SDK v2 native since 2024) **plus** Terraform `provider "aws" { endpoints { <service> = "<shim>" } }`. |
| GCP | `CLOUDSDK_API_ENDPOINT_OVERRIDES_<SERVICE>` env var for `gcloud`; `option.WithEndpoint` for `cloud.google.com/go`; `<service>_custom_endpoint` for `hashicorp/google`. |
| Azure | `cloud.Configuration` with `ResourceManager` endpoint override for ARM-based services; `azblob.NewClient(endpoint, ...)` for data-plane services; Terraform `azurerm` uses `metadata_host` + `environment = "stack"` for full ARM redirect. |

`shimctl env` reads a small declarative YAML (`internal/clientconfig/overrides.yaml`) that already documents the per-cloud knobs for every shimmed service. Each Phase-1–8 service contributed one row when it landed; Phase 9 promotes the table to a CLI tool. No new translation logic — only packaging.

### 2. Mock cloud servers — accuracy bar for `terraform import`

The mock cloud servers are not new shims — they are **fixtures** that simulate enough of AWS/GCP/Azure's *backend* so that:

1. The official provider believes the resource exists upstream.
2. The shim's translation logic — pointed at the mock as its backend — surfaces it through the *opposite* cloud's wire shape.

Layout per cloud:

```
mocks/
  aws/        # an httptest-style mock of S3, Secrets Manager, SQS, etc.
  gcp/        # mock of GCS JSON API, Secret Manager, Pub/Sub, etc.
  azure/      # mock of Blob, Key Vault, Service Bus, etc.
```

Each mock is implemented as a thin wrapper around the corresponding **inmem backend** that Phases 1–8 already ship — exposing it at the wire shape of cloud A. That way the mock can never drift from real shim behavior: it is *literally the same code* the shim itself uses as its inmem backend, dressed in the source cloud's wire envelope.

#### Importer-read contract (the foundation, drafted *before* code)

**Lesson from Codex review:** the inmem domain knows about a `Bucket`; the Terraform provider's `aws_s3_bucket` `Importer.Read` calls `HeadBucket` → `GetBucketLocation` → `GetBucketTagging` → `GetBucketPolicy` → `GetBucketVersioning` → `GetBucketEncryption` → `GetPublicAccessBlock` → … Each is a discrete API call the mock + frontend must answer, even if the answer is *"unset"* in the source cloud's vocabulary. Phase 9's first move per service is to **trace the provider's Read path** (via `TF_LOG=DEBUG terraform import …` against an `httptest` recorder) and capture the precise op list in `services/<svc>/conformance/importer_contract.md`. The mock and frontend then implement that contract explicitly; "add ops as we discover them" is *not* the workflow.

Three classes of read-path operation:

1. **Backed by inmem state.** Tags, metadata, region. These extend inmem's schema; the shim should have shipped them anyway.
2. **Sensibly unset.** Versioning, encryption-at-rest, public-access-block on a brand-new resource. The frontend returns the source cloud's "feature not configured" response (e.g. `NoSuchBucketPolicy`, `404 with code NotFound`, ARM `404`). Recorded once per service in the contract doc.
3. **Genuinely out of intersection.** AWS-only object-lock, GCP-only customer-managed retention policies, Azure-only soft-delete-blob-versioning. The frontend returns the source cloud's "not configured" answer; the provider records the attribute as default; `plan` is no-diff because the attribute *is* its default.

Phase 9 *does not* try to make `aws_s3_bucket` represent a GCS bucket's GCS-specific quirks — it represents *the intersection the shim defines*, presented through the AWS wire shape.

#### What we do NOT mock

- Authentication of signed requests against the mock. The mock accepts any well-formed SigV4 / OAuth2 / SharedKey signature — same posture as Phase-1–8 frontends. Real-auth coverage lives in the real-cloud lane.
- Pagination boundaries that don't matter for single-resource import.
- Replication, lifecycle policies that import doesn't read.

### 3. The actual cross-cloud import flow

The flow for a single cell — *user wrote AWS-shaped Terraform; the actual data lives in GCS* — is:

```
                            ┌─────────────────────────┐
                            │ user's terraform        │
                            │ import aws_s3_bucket.x  │
                            │ -endpoint=$SHIM         │
                            └────────────┬────────────┘
                                         │  SigV4 GET
                                         ▼
                              ┌──────────────────────┐
                              │ shim AWS S3 frontend │
                              └──────────┬───────────┘
                                         │ domain.Storage.Head/Get
                                         ▼
                              ┌──────────────────────┐
                              │ GCS backend          │
                              └──────────┬───────────┘
                                         │ GCS JSON GET
                                         ▼
                              ┌──────────────────────┐
                              │ real GCS  OR  GCS    │
                              │             mock     │
                              └──────────────────────┘
```

The shim is already correct; Phase 9 only proves the *import* code path works against this stack. Per-frontend `terraform_import_test.go` in each service's `conformance/` adds:

1. Seed a resource directly in the backend (real GCS for real-cloud lane; mock-GCS for the matrix lane).
2. `terraform import aws_s3_bucket.imported <name>` against the shim's AWS-shaped frontend.
3. Assert the resulting state JSON's `id`, `bucket`, `region`, plus any tags/labels round-trip.
4. **`terraform plan -generate-config-out`** writes provider-emitted HCL from the imported state. The test then runs `terraform plan` against that generated config — it must report no diff. The contract is *generate-then-plan-is-no-diff*, not *import-then-original-config-is-no-diff*. The latter is unachievable because the source-cloud provider's state always contains computed canonical fields (ARNs, self-links, ARM IDs) that the user's original config wouldn't repeat verbatim.

### 4. Resource-shape mapping table

A persistent worry from the user's framing: "what does it *mean* for an `aws_s3_bucket` to map to a GCS bucket?" The answer is a per-frontend lookup baked into shimanism's existing frontends. We expose this as a static table for clarity:

| Source cloud's resource | Shim domain | Maps to (per backend) |
|---|---|---|
| `aws_s3_bucket` | `storage.Bucket` | AWS bucket / GCS bucket / Azure storage container / MinIO bucket |
| `aws_secretsmanager_secret` | `secrets.Secret` | AWS secret / GCP secret / Azure KV secret / Vault KV entry |
| `aws_sqs_queue` | `queue.Queue` | AWS queue / GCP subscription / Azure SB queue / NATS stream |
| `aws_sns_topic` | `pubsub.Topic` | AWS topic / GCP topic / Azure SB topic / NATS subject |
| `aws_db_instance` | `rdbms.Instance` | AWS RDS instance / GCP Cloud SQL / Azure DB / CloudNativePG cluster |
| `aws_elasticache_cluster` | `cache.Cluster` | AWS EC / GCP Memorystore / Azure Cache / Redis Operator RedisFailover |
| `aws_lambda_function` | `functions.Function` | AWS Lambda / GCP Cloud Run / Azure Container App / Knative Service |
| `aws_apigatewayv2_api` | `apigateway.Gateway` | AWS API / GCP Gateway / Azure APIM Api / Envoy Gateway CR |

Same table flipped for `google_*` and `azurerm_*` source clouds. Every cell in the table is one `terraform import` conformance cell in Phase 9.

## Sub-phase plan

| Sub | Status | Headline |
|---|---|---|
| **9.0** | ◻ | Phase-9 scope baseline (this doc + `services/_import/OPERATIONS.md`). |
| **9.1** | ◻ | `shimctl env` CLI: emits per-cloud + per-frontend endpoint-override env vars / TF snippets. Single source of truth at `internal/clientconfig/overrides.yaml`. |
| **9.2** | ◻ | **Importer-read contract trace** per service. Run `TF_LOG=DEBUG terraform import` against an httptest recorder for each in-scope resource type. Capture op list + expected response shapes in `services/<svc>/conformance/importer_contract.md`. **No code yet** — this is the design input for 9.3+. |
| **9.2-A** | ◻ | **No-fakes audit + intersection inventory.** Per service, file `services/<svc>/INTERSECTION.md` classifying every operation the 9.2 trace surfaces as (1) in-intersection-real-work, (2) feature-genuinely-unset, or (3) out-of-intersection. Audit existing Phases 1–8 frontend handlers for any synthesized success / discarded fields / wrong-envelope unset response; file BUGs before fixes. Exit gate: no operation an importer hits returns a fake. |
| **9.2-B** | ◻ | **Migration utility audit.** Per service, file `services/<svc>/MIGRATION.md` covering AWS→GCP, AWS→Azure, AWS→K8s peer (and reverses). Re-evaluate every op's intersection status through the migration lens. Reclassify ops that are migration-critical and have honest counterparts; document why genuinely-vendor-locked ops stay out. |
| **9.3** | ◻ | Mock cloud servers under `mocks/{aws,gcp,azure}/`. Each is a thin frontend around the inmem backend so the wire shape is identical to the corresponding shim frontend. Backs **exactly** the ops the 9.2 contracts enumerate, with the source cloud's true "not configured" envelope for unset features and the source cloud's "not supported" envelope for out-of-intersection. Verified by the 9.2-A audit. |
| **9.4** | ◻ | Per-service import conformance for storage — `terraform import` against the AWS / GCP / Azure frontends, mock-backed. Uses `terraform plan -generate-config-out` then plan-is-no-diff. |
| **9.5** | ◻ | Same for secrets, queue, pubsub. |
| **9.6** | ◻ | Same for rdbms, cache. |
| **9.7** | ◻ | Same for functions, apigateway. |
| **9.8** | ◻ | Full matrix import driver: every (frontend, backend, service) cell in scope. Uses inmem-as-mock-cloud for the non-Track-A backends. |
| **9.9** | ◻ | CLI-level import smoke: one per (frontend, service) — the `describe`/`get` op the importer calls under the hood, run as a raw CLI command. |
| **9.10** | ◻ | SDK-level import smoke: one per (frontend, service) — the same op via the cloud's official Go SDK. |
| **9.11** | ◻ | `cmd/shim mock` subcommand: stands up the inmem-as-cloud mocks as standalone HTTP servers for end-user dev loops. |
| **9.12** | ◻ | CI integration: new lane `conformance-import-matrix` (mock-only). |
| **9.13** | ◻ | **Exit criterion (mock-tier): `TestCrossCloudImport_Roundtrip`** — for each (source cloud A, backend cloud B) where A ≠ B, write A-shaped Terraform with `endpoints { <svc> = shim }`, import a B-resident resource via the shim, run `terraform plan -generate-config-out` then `terraform plan` against the generated config. Assert zero diffs. This is the Phase 9 exit gate. |
| **9.14** | ◻ | Phase 9 closer — continuity docs, PR. |

### Track A bring-up (separate phase, parallel)

Per Codex review (Q3): real-cloud Terraform / CLI / SDK e2e tests **do not gate Phase 9 closure**. They are real work — account setup, IAM, quota, cleanup, naming uniqueness, billing — that has its own failure modes orthogonal to import fidelity, and that work was never carried into Phases 1–8. Phase 9 ships **mock-tier confidence**; **Phase 9-A** (a follow-on, opened once Track A credentials are available) ships real-cloud confidence. The 9-A sub-phases (would-be 9.10–9.12 in the original draft) are deferred to that phase, *not* dropped:

- **9-A.1** — Real-cloud Terraform e2e import lanes for storage / secrets / queue / pubsub (data-plane services, cheapest to provision).
- **9-A.2** — Real-cloud Terraform e2e import lanes for rdbms / cache / functions / apigateway (control-plane services, slower / costlier).
- **9-A.3** — Real-cloud CLI + SDK import smoke per frontend × per service.
- **9-A.4** — Exit criterion: `TestCrossCloudImport_Roundtrip_Real` mirrors the mock-tier exit criterion but against real backends.

## Anti-patterns explicitly rejected

- **No recorded-cassette tests.** Recorded cassettes can pass while the underlying shim is broken — they're a worse version of mocks. The mock cloud servers are *runnable code* that the conformance suite shares with inmem.
- **No "happy path only" import tests.** Every cell must also assert `terraform plan` post-import is no-diff. A test that "imports successfully" but proposes a diff is a fidelity bug, not a pass.
- **No per-resource bespoke shape mapping.** The mapping table above is enforced — adding a new importable resource type means updating the table and the corresponding shim frontend, not writing one-off translation code in the test.
- **No selective skipping by resource type.** If a resource's import doesn't work, file a BUG. The skip list is `services/<svc>/conformance/import-exempt.txt` and must cite a BUG ID.
- **No "passes import, then `plan` proposes a diff" cell.** The exit criterion mandates plan-is-no-diff *after generating config from the imported state*. A cell that fails this is a BUG, not an exemption.
- **No "the inmem domain knows enough" leap.** The provider's `Importer.Read` is the authority on what operations the importer needs; the importer-read contract trace (sub-phase 9.2) is the design input, not an afterthought.
- **No endpoint without real work.** Every URL the CLI / SDK / TF provider calls (for an in-intersection operation) must do real translation against a real backend. No `return &T{}, nil` placeholders. The 9.2-A audit pass enforces this against existing Phases 1–8 frontends too — not just new Phase 9 code.

## Open design questions for review

1. **Provider TF mock injection.** Should we contribute upstream `terraform-provider-aws` patches that respect `AWS_ENDPOINT_URL_S3` everywhere, or stay with the existing `endpoints { ... }` block? Provider v5.x already honors `AWS_ENDPOINT_URL_*`. **Recommendation:** rely on it.
2. **Mock fidelity vs inmem drift.** Inmem omits some real-cloud quirks (eventual-consistency windows; case-insensitive bucket lookups on AWS; …). For import these are usually irrelevant — import is read-once. Plan: **catch quirks via Track A real-cloud lanes**, not by trying to simulate them in mocks.
3. **State manipulation.** `terraform import` writes to `terraform.tfstate` directly. We use per-test `TF_DATA_DIR` to isolate state so parallel tests don't collide.
4. **GCS / Azure resumable-upload imports.** Not in scope — import is read-only.
5. **Cross-cloud naming.** AWS bucket names are unique; GCS bucket names are also globally unique; Azure container names are scoped per storage account. The mocks accept the source cloud's naming rules; the underlying inmem stores the bytes unambiguously.

## Why this phase is honest

The shim has been shimming since Phase 1; what Phase 9 proves is that the *receiving end* of the shim — the user's `terraform import` command, the `gcloud describe`, the `aws get` — gets attributes back that are good enough that the user could then run `terraform apply` and the provider would propose no changes. **If the next `apply` after `import` proposes any diff, the shim is lying somewhere.** Phase 9 is the test that catches every such lie.

## Codex review (and what we changed in response)

This document was drafted then submitted to the `codex` non-interactive CLI for an independent review. Four critiques landed; each is addressed in the plan above.

1. **"Reusing inmem behind a cloud wire shape is not enough; the provider's Read path calls many ops the inmem domain doesn't naturally expose."**
   *Accepted.* Sub-phase **9.2 — Importer-read contract trace** was inserted as the explicit first deliverable, before any mock or test code. Inmem is extended only against the traced contract; the "add ops as discovered" workflow Codex flagged as inadequate is rejected.

2. **"A universal no-diff exit criterion is unachievable; cloud-native canonical shapes (ARNs, self_links, ARM IDs) will diff."**
   *Partially accepted.* The exit criterion was reworded to **`terraform plan -generate-config-out` then plan-against-generated-config is no-diff**. That's the achievable formulation — the provider's own emitted HCL is what we compare against. Computed-only fields then live inside the state, not the config, and don't drive a diff. The plan also makes the three classes of read-path operation explicit (backed by inmem state / sensibly unset / out of intersection) so the team has a contract for what "unset" looks like per cloud.

3. **"Real-cloud lanes 9.10–9.12 are not realistic as part of the same exit gate; Track A bring-up has its own failure modes."**
   *Accepted.* Real-cloud lanes are removed from Phase 9's exit gate and re-homed under **Phase 9-A**, opened once Track A credentials are available. Phase 9 ships mock-tier confidence. Phase 9-A ships real-cloud confidence. The work is sequenced, not dropped.

4. **"Most likely failure mode: conflating 'the shim's domain model can represent the resource' with 'the official Terraform provider's full Read-after-import contract is satisfied.'"**
   *Accepted.* The plan now opens 9.2 with exactly this distinction and uses the trace as the *only* source of truth for which ops the mock + frontend must implement. The "Anti-patterns explicitly rejected" section also flags the conflation directly.

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–20 complete. Active work is Phase 21 (L7 Load Balancers).

## Where we are

**Phase 19 (Key Management) is complete** — PRs #127 (19.A) / #128 (19.B) / #129 (19.C) / #130 (19.D CLI/TF) / #131 (19.D sockerless), all merged. KMS across AWS/GCP/Azure + K8s: domain + inmem (real AES-256-GCM) + all three frontends + real backends + full SDK/CLI/TF conformance + all sockerless lanes green with **zero skips**. All four KMS sockerless gaps closed upstream (#407/#413/#419/#423).

**Phase 18 — Container Registry is complete.** OCI Distribution `/v2/` data plane (shared hand-written router) + ECR/AR/ACR control planes + connected backends. See [docs/phase-18-scoping.md](docs/phase-18-scoping.md), [services/registry/INTERSECTION.md](services/registry/INTERSECTION.md), and [services/registry/APPLY_INTERSECTION.md](services/registry/APPLY_INTERSECTION.md). 18.A–18.D landed across PRs #132–#141: GCP AR, AWS ECR, and Azure ACR frontends all have SDK/CLI/Terraform control-plane coverage plus go-containerregistry OCI data-plane coverage; connected backends now include CNCF Distribution, AWS ECR, GCP Artifact Registry, Azure ACR, and inmem.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials) · BUG-67 (sockerless AWS ECR manifest `HEAD`, filed upstream as sockerless#465) · BUG-68/69 (local sockerless-runner/KMS-lane findings).

**PR #153 merged.** Phase 20.C complete: AWS MSK restJson1 control-plane frontend, cluster-scoped `domain.Streams`, BUG-73/74/75 fixed, AWS SDK + kgo produce/fetch conformance all green. Next: Phase 20.D Azure Event Hubs frontend on a new branch.

## Session-start checklist

1. Create branch `phase-20-azure-eventhubs-frontend` from `main`.
2. Work Phase 20.D from [docs/phase-20-scoping.md](docs/phase-20-scoping.md): Azure Event Hubs ARM spec + codegen, Azure frontend over `domain.Streams`, Azure SDK + CLI + Terraform conformance, real Kafka client via Event Hubs Kafka endpoint.
3. Open one PR and monitor CI until green. Do not merge.

## Phase 20 — Event Streaming ◐

Phase 20 is planned around ordered, partitioned event streams with a Kafka-compatible data plane. PR #147 merged the first 20.A foundation slice: scoping is done, the domain is defined, Strimzi is the honest K8s peer choice, and the inmem backend is a real append-only partition log. PR #148 added the real Kafka frame/runtime boundary without faking a broker. PR #150 added the first real Kafka dispatcher. PR #151 added the first cloud-shaped control-plane frontend: GCP Managed Kafka topic lifecycle over `domain.Streams`. PR #152 closed the first GCP/Kafka surface with real `kgo` produce/fetch conformance after GCP REST topic creation.

Merged 20.A foundation checklist:

- [x] Draft `docs/phase-20-scoping.md`: Kafka data-plane boundary, control-plane/resource intersection, out-of-intersection features, and normalization rules.
- [x] Inspect official specs/source APIs for AWS MSK, GCP Managed Service for Apache Kafka, Azure Event Hubs/ARM, Kafka protocol, and Strimzi.
- [x] Decide K8s peer: Strimzi Kafka as a connected backend.
- [x] Define `internal/eventstream/domain`.
- [x] Add `services/eventstream/backends/inmem` as a real append-only partition log, with tests for topic lifecycle, produce/fetch, offsets, retention, and validation.
- [x] Publish `services/eventstream/INTERSECTION.md` and `docs/services/eventstream.md`.
- [x] Open and merge the Phase 20.A foundation PR (#147).

Merged Kafka runtime checklist:

- [x] Verify `github.com/twmb/franz-go/pkg/kmsg` release age and BSD-3-Clause license against repo policy.
- [x] Add the direct `kmsg` dependency.
- [x] Add `internal/eventstream/kafkawire` for Kafka size-prefixed frame reads, request-header decode, generated request-body decode, and response framing.
- [x] Cover flexible headers, unsupported APIs/versions, malformed frames, and response framing with tests.
- [x] Run full local verification (`go test`, `make lint`, `make license-check`, code-health audits as practical).
- [x] Open and merge the Kafka runtime PR (#148).

Current Phase 20.B data-plane checklist:

- [x] Validate sockerless PR #475 and remove stale registry BUG-65/66 skip gates.
- [x] Implement the minimal Kafka TCP handler/dispatcher for honest metadata/produce/fetch behavior.
- [x] Add SDK/client conformance around the Kafka handler and inmem backend.
- [x] Run local verification (`make test`, `make lint`, `make license-check`).
- [x] Open and merge the Kafka handler PR (#150).
- [x] Add GCP Managed Kafka topic lifecycle frontend over `domain.Streams`.
- [x] Add official GCP Managed Kafka SDK conformance for topic create/get/list/delete and out-of-intersection rejection.
- [x] Run local verification (`make test`, `make lint`, `make license-check`).
- [x] Open and merge the Phase 20.B GCP frontend PR (#151).
- [x] Add real Kafka client conformance with `franz-go/pkg/kgo` over the TCP data plane after GCP REST topic creation.
- [x] Fix BUG-70/71 surfaced by live Kafka client negotiation.
- [x] Run full local verification (`make test`, `make lint`, `make license-check`).
- [x] Open and merge the Phase 20.B GCP/Kafka client PR (#152).

Phase 20.C AWS MSK checklist: ✅ COMPLETE (PR #153)

- [x] Vendor AWS MSK Smithy spec from `aws/aws-sdk-go-v2` and add deterministic `codegen.json`.
- [x] Add generated AWS MSK restJson1 routes for cluster lifecycle, bootstrap discovery, node/topic list, and topic lifecycle.
- [x] File and fix BUG-73: explicit cluster scope in domain, inmem backend, GCP frontend, Kafka data-plane server.
- [x] Add AWS MSK frontend over `domain.Streams` with SigV4 and source-shaped errors; reject out-of-intersection replication/config options loudly.
- [x] File and fix BUG-74: escaped ARN path labels preserved through SigV4 canonicalization and generated REST routing.
- [x] Add official AWS MSK SDK conformance plus real `kgo` produce/fetch against `GetBootstrapBrokers`.
- [x] File and fix BUG-75: `TopicArn` as `arn:aws:kafka:{region}:{account}:topic/{cluster-name}/{cluster-uuid}/{topic-name}`.
- [x] Run full local verification (`make test`, `make lint`, `make license-check`, `make codegen-check`) — all green.
- [x] Open PR #153 and merge after CI green.

## Phase 20.D — Azure Event Hubs frontend ✅ COMPLETE

- [x] Create branch `phase-20-azure-eventhubs-frontend`.
- [x] Add Azure Event Hubs frontend (`internal/eventstream/frontends/azure_eventhubs/`) over `domain.Streams` with Azure Bearer auth and ARM-shaped errors; map namespace → cluster, event hub → topic. (ARM SDK types used directly; no azure-codegen route emission for eventstream.)
- [x] Wire the frontend into `internal/harness/server.go` with `httptest.NewTLSServer`.
- [x] Add official Azure SDK conformance (`armeventhub`) for namespace + event hub lifecycle, and real `kgo` produce/fetch against the Event Hubs Kafka endpoint.
- [x] Reject out-of-intersection options (Basic SKU, CaptureDescription, BYOK, dedicated clusters, auto-inflate, partitionCount > 32) with 400 OperationNotSupported.
- [x] Run full local verification (`make test`, `make lint`, `make license-check`, `make codegen-check`) — all green.
- [x] Open Phase 20.D PR (on `phase-20-azure-eventhubs-frontend`). Awaiting CI + user merge.

## Phase 20.E — Strimzi backend + full CLI/TF conformance matrix ✅ COMPLETE (PR #155)

- [x] File BUG-76/77, fix both, add Strimzi backend, full CLI/TF conformance matrix, GCP SDK cluster lifecycle test.
- [x] All local verification green; PR #155 merged.

## Phase 21 — L7 Load Balancers ◐

Phase 21 extends Phase 16.D's layer-4 TCP NLB to layer-7 Application LB. The intersection (N35) covers: HTTPS termination, host/path routing rules, HTTP target groups with health checks, and opaque certificate pass-through. SSL cert management is out-of-intersection (callers supply pre-provisioned cert IDs). See [docs/phase-21-scoping.md](docs/phase-21-scoping.md) and N35 in [docs/normalizations.md](docs/normalizations.md).

### Phase 21.A — Domain + inmem + AWS ELBv2 adapter ◐

Active branch: `phase-21-l7-loadbalancers`.

- [x] Write `docs/phase-21-scoping.md`.
- [x] Add N35 to `docs/normalizations.md`; update N27 to reference N35.
- [x] Extend `internal/loadbalancer/domain/domain.go`: `Rule`, `HealthCheck`, `ProtocolHTTP/HTTPS`, `CertificateIDs`, update/modify op types, extend `LoadBalancers` interface.
- [x] Extend `services/loadbalancer/backends/inmem/inmem.go`: Rule CRUD + UpdateTargetGroup/UpdateListener/UpdateRule/SetRulePriorities.
- [x] Add `ErrNotSupported` stubs for new interface methods in `backends/aws/aws.go` and `backends/k8slb/k8s.go`.
- [x] Update `services/loadbalancer/codegen.json`: add CreateRule/DeleteRule/DescribeRules/ModifyRule/ModifyListener/ModifyTargetGroup/SetRulePriorities.
- [x] Run `make codegen` to regenerate `services/loadbalancer/gen/aws_elbv2.gen.go`.
- [x] File BUG-78: awsQuery codegen doesn't decode `*ProtocolEnum` / `*LoadBalancerTypeEnum` pointer-to-enum fields.
- [x] Fix BUG-78: add `IsPointerEnum` flag to `fieldView` in `emit.go`, add `hasPrefix`/`trimPrefix` to FuncMap, update `template_awsquery.tmpl` to emit pointer-to-enum form decode.
- [x] Run `make codegen` again to pick up the template fix.
- [x] Extend `internal/loadbalancer/frontends/aws_elbv2/adapter.go`: accept ALB type, HTTP/HTTPS protocols, HTTPS+cert listener, HTTP TG health check, CreateRule/DeleteRule/DescribeRules/ModifyRule/ModifyListener/ModifyTargetGroup/SetRulePriorities.
- [x] Add `TestAWSSDK_ELBv2_ALB_RuleLifecycle` to `services/loadbalancer/conformance/aws_sdk_test.go`.
- [x] Run full local verification (`go test ./...`, `make lint`, `make license-check`) — all green.
- [ ] Run `make codegen inject-provenance` and commit.
- [ ] Check `gh pr list --state open` and ask user before opening PR.
- [ ] Open Phase 21.A PR.

### Phase 21.B — GCP HTTP(S) LB extension ✅ COMPLETE

- [x] Extend `internal/loadbalancer/frontends/gcp_lb/server.go`: global backendServices + urlMaps + targetHttpsProxies + globalForwardingRules + sslCertificates.
- [x] Add BlobEntry + PutBlob/GetBlob/ListBlobs/DeleteBlob to domain interface; implement in inmem; ErrNotSupported stubs in AWS + K8s.
- [x] Add `TestGCPSDK_LB_L7Lifecycle` conformance test (insert/get/list/delete all resources).
- [x] All tests green, lint clean, codegen-check clean.
- [ ] Open Phase 21.B PR (on `phase-21-l7-loadbalancers`).

### Phase 21.C — Azure Application Gateway + K8s Ingress + full CLI/TF matrix

- [ ] Extend `azure_lb/server.go` or new `azure_appgateway` sub-package for compound ARM `applicationGateways` resource.
- [ ] Extend `k8slb` backend and frontend for K8s Ingress.
- [ ] Add CLI (aws, gcloud, az) + Terraform conformance for all three frontends.
- [ ] Full 3 × 3 driver-type matrix green.

## Phase 18 — Container Registry ✅ COMPLETE

Closed by PRs #132–#141. Registry sockerless tests are wired and fail loud on simulator gaps only. BUG-65 (GCP AR chunk `PATCH`) and BUG-66 (Azure ACR upload/auth) are closed after sockerless #451/#456 and #469/#475; their local skip gates were removed. BUG-67 (AWS ECR manifest `HEAD` 400, [sockerless#465](https://github.com/e6qu/sockerless/issues/465)) remains as the only registry simulator skip. BUG-64 / sockerless#450 is closed on current sockerless main; PR #146 replaced the old missing-`/v2/` probe with a real AWS ECR through-shim push/pull attempt.

## Code Health Audit Baseline ◐

- [x] Research current Go dead-code and duplicate-code tooling.
- [x] File registry sockerless simulator issues after user approval.
- [x] Add advisory `make duplication-audit`, `make deadcode-audit`, and `make code-health` targets.
- [x] Publish [docs/code-health.md](docs/code-health.md) with triage rules and cleanup order.
- [x] Open and merge the baseline PR (#143).
- [x] Refactor `cmd/shim/cache.go` / `cmd/shim/rdbms.go` runner duplication without hiding backend/frontend errors.
- [x] Open and merge the `cmd/shim` cleanup PR (#144).
- [x] Extract focused helpers for repeated Terraform apply bodies in `services/secrets/conformance/sockerless_test.go`.
- [x] Review and deduplicate compute inmem and K8s duplicate helpers while preserving domain-specific behavior.
- [x] Open and merge the remaining duplicate-code cleanup PR (#145).
- [x] Make duplicate-code detection strict in `make lint` / CI.
- [x] Triage small dead-code findings and delete confirmed unused helpers.
- [x] Open and merge the code-health closeout + Phase 20 scoping PR (#146).
- [ ] Next code-health candidate: continue hand-written `deadcode` triage one package at a time after Phase 20.A PR is green.

## Phase 19 — Key Management ✅ COMPLETE

Closed by PRs #127–#131. KMS has all three frontends, real AWS/GCP/Azure backends, K8s NotImplemented, full SDK/CLI/Terraform conformance, and all sockerless lanes green with zero skips.

## Phase 17 ✅ (PRs #122–#125)

Block storage — AWS/GCP/Azure SDK+CLI+Terraform, K8s PVC volume CRUD, sockerless EBS. Provider wire-quirks absorbed in-shim: EBS `CreateTime` nil-deref, Azure disk/snapshot 200-vs-201 poller, GCP `sizeGb` unquoted-vs-`,string`.

## Upstream watch

All Firecracker blockers resolved. Sockerless PR #475 merged; registry BUG-65/66 are closed. Watch sockerless#465 for BUG-67.

## Standing rules

- **One PR open at a time.** Before opening any PR, check `gh pr list --state open`; if one exists, ask the user first. Close stale/superseded PRs.
- **Fix shim problems in the shim.** Only ever file with `github.com/e6qu/sockerless` (real sockerless-side gaps, after asking) — never Hashicorp or any other upstream.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in BUGS.md before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file; mirrors CI.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane.

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #12 (Phase 7) at `9d02af0` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-8-apigateway` — PR #13. Phase 8 + a substantial chunk of Phase 9 on one PR per user instruction.
- **Phase 8** complete (16/16 sub-phases). Exit criterion `TestRouteServes_Envoy` green in `conformance-envoy` CI lane.
- **Phase 9** advanced significantly on the same PR:
  - `PHASE_9_PLAN.md` drafted + codex-reviewed + revised to encode the "no fakes" + "useful for migration" instructions.
  - **9.1** `shimctl env` CLI + `internal/clientconfig/overrides.yaml` registry of (cloud × service) endpoint-override knobs.
  - **9.2-A** per-service `INTERSECTION.md` audits — every wire op classified real-work / feature-unset / out-of-intersection.
  - **9.2-B** per-service `MIGRATION.md` walkthroughs — runnable migration recipes per (source cloud × target cloud × K8s peer).
  - **9.5** `terraform_import_test.go` for all 8 services — every import driver passes through the shim.
  - **Six real fidelity fixes** surfaced by the import tests (XML double-nesting, missing Policy JSON, missing tag-list handlers, missing selection-expression defaults, missing Lambda subresources, missing RDS ARN). No fakes survived.
  - **9.13** cross-cloud exit criterion: `TestCrossCloudImport_Roundtrip_StorageAWStoGCS` proves the headline promise — AWS-shape TF imports a bucket that lives in mock-GCS through the shim, with no fidelity diffs.
- **Remaining Phase 9 work** (next PR after merge): mock cloud servers as standalone binaries (9.3), `cmd/shim mock` subcommand (9.11), per-frontend full-matrix import driver (9.7), CI lane `conformance-import-matrix` (9.12), Phase 9-A real-cloud lanes behind Track A.

## Phase 8 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **8.0** | ✅ | Scope + design baseline. `services/apigateway/OPERATIONS.md` captures the 5-op intersection. Declarative-replace via `DeployGateway`. Route shape: method + path + backend URL only — per-route auth/throttling/transforms deferred. |
| **8.1** | ✅ | Vendor AWS API Gateway v2 Smithy. GCP via `google.golang.org/api/apigateway/v1`; Azure via `armapimanagement`. |
| **8.2** | ✅ | Domain interface `internal/apigateway/domain/`. |
| **8.3** | ✅ | inmem + AWS API Gateway v2 frontend (restJson1) + SDK conformance. |
| **8.4** | ✅ | **Envoy Gateway backend** (K8s peer) via dynamic client + unstructured `Gateway` / `HTTPRoute` CRs. |
| **8.5** | ✅ | AWS API Gateway v2 passthrough. |
| **8.6** | ✅ | GCP API Gateway backend. |
| **8.7** | ✅ | Azure API Management backend. (DeleteGateway returns InvalidArgument until Track A; armapimanagement/v3 delete signature requires version-specific etag handling — see BUGS.md.) |
| **8.8** | ✅ | GCP API Gateway frontend. |
| **8.9** | ✅ | Azure API Management REST frontend. |
| **8.10** | ✅ | Matrix conformance — 3 frontends × 5 backends, SDK driver. |
| **8.11** | ✅ | CLI conformance: aws apigatewayv2 + gcloud api-gateway; az smoke (per-resource override gap tracked in BUGS.md). |
| **8.12** | ✅ | Terraform conformance: hashicorp/aws apigatewayv2 init+apply+destroy; hashicorp/google plan; azurerm smoke. |
| **8.13** | ✅ | `cmd/shim apigateway` subcommand. Default `:9700`. |
| **8.14** | ✅ | CI lane `conformance-envoy`: kind + Envoy Gateway v1.2.4. |
| **8.15** | ✅ | **HTTP-route exit criterion test** `TestRouteServes_Envoy` — register Gateway+Route via AWS frontend → echo upstream behind Envoy → port-forward + HTTP GET succeeds. |
| **8.16** | ◐ | Phase 8 closer — push, CI green, PR merged. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 8 design notes

- **Declarative-replace.** `DeployGateway(spec)` atomically swaps the full routing table. Partial route mutations on a live gateway are out of intersection (cross-cloud semantics differ too much).
- **Route shape is minimal.** Method + path + backend URL only. Per-route auth, throttling, transforms, custom domain mapping all deferred — the exit criterion is "routes dispatch HTTP to backends correctly."
- **HTTP data plane.** Same as Phase 7 — the shim provisions the gateway and returns its URL; clients HTTP-request the URL; the gateway dispatches.

## Invariants snapshot

- Never auto-merge; user merges every PR.
- **One PR at a time.**
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API.
- Real backends only.
- Tests from official client surfaces.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention.**

## Resumable tracks

- **Track A — Cloud test accounts.**
- **Track B — Coding-agent automation.**
- **BUG-2 (queue / SetQueueAttributes).** Ripples through Phases 4–7 TF cells.
- **BUG-5 (rdbms / GCP Operations polling endpoint).** Blocks Phases 5–7 GCP CLI + TF cells.

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open`.
3. `git checkout <pr-branch>`.
4. Read STATUS snapshot + this file's "Where we are".
5. Read STATUS invariants + AGENTS.md.
6. Skim BUGS open.
7. Pick the next ◻ sub-task.

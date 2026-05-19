# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #11 (Phase 6 — cache, full 3 × 5 × 3 matrix + Redis Operator as K8s peer + redis PING exit criterion) at `cca8bc0` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-7-functions` — fresh off main, 7.0 scope baseline drafted.
- **Project phase:** **Phase 7 — Functions.** Same control-plane shape as Phases 5+6 with HTTP as the data plane. Container-image deployments only. Three frontends (AWS Lambda, GCP Cloud Run, Azure Container Apps) × five backends (inmem + Knative Serving as K8s peer + the three clouds) × three driver types. 5-op intersection. Events/triggers deferred — HTTP-trigger functions only.

## Phase 7 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **7.0** | ✅ | Scope + design baseline. `services/functions/OPERATIONS.md` captures the 5-op intersection (Create/Delete/Describe/List/Update Function). Container image only; events + auth-on-invoke deferred. |
| **7.1** | ✅ | AWS Lambda Smithy vendored (restJson1). |
| **7.2** | ✅ | `internal/functions/domain/` — 5-method `Functions` interface. |
| **7.3** | ✅ | inmem + AWS Lambda restJson1 frontend + SDK conformance. |
| **7.4** | ✅ | Knative Serving backend (K8s peer). |
| **7.5** | ✅ | AWS Lambda passthrough backend. |
| **7.6** | ✅ | GCP Cloud Run backend (`run/v2`). |
| **7.7** | ✅ | Azure Container Apps backend (`armappcontainers/v3`). |
| **7.8** | ✅ | GCP Cloud Run REST frontend. |
| **7.9** | ✅ | Azure Container Apps REST frontend. |
| **7.10** | ✅ | Matrix conformance `TestFunctionsMatrix_AWSFrontend`. |
| **7.11** | ✅ | CLI: `aws lambda` green; `gcloud run` + `az containerapp` ◇ skipped. |
| **7.12** | ✅ | Terraform: all three ◇ skipped. |
| **7.13** | ✅ | `cmd/shim functions` subcommand. Default `:9600`. Version 0.8.0-phase-7. |
| **7.14** | ✅ | CI lane `conformance-knative`: kind + Knative Serving v1.15.7 + Kourier. |
| **7.15** | ✅ | **HTTP-invoke connectivity test** — deploys `gcr.io/knative-samples/helloworld-go` via the shim, port-forwards, opens real HTTP, asserts "Hello" response. Phase-7 exit criterion. |
| **7.16** | ✅ | Phase 7 closer. PR pending push. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 7 design notes

- **Control plane only.** Same posture as Phases 5+6. The shim provisions deployments and returns endpoint URLs; HTTP invocation goes straight to the function URL.
- **Container image only.** ZIP-package Lambda deployment is out of intersection. All four backends natively support container images.
- **Events deferred.** Cross-cloud event payload normalization is the hard part per PLAN.md. HTTP-trigger functions only at this phase; event-source mappings deferred.
- **Auth-on-invoke deferred.** Public-HTTP functions only.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.**
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API.
- Real backends only; no emulators.
- Tests from official client surfaces.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention.**

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.**
- **Track B — Coding-agent automation.**
- **BUG-2 (queue / SetQueueAttributes).** Ripple still affects Phase 4 + 5 TF cells.
- **BUG-5 (rdbms / GCP Operations polling endpoint).** Blocks Phase 5 + 6 GCP CLI + TF cells.

## Session-resume checklist

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md).
6. Skim [BUGS.md § Open](BUGS.md#open).
7. Pick the next ◻ sub-task; mark ◐ when starting.

# Agent Guidelines

> Rules for any agent (human or AI) working in this repo. `CLAUDE.md` is a symlink to this file. Read [PHILOSOPHY.md](PHILOSOPHY.md) first for the *why*; this file is the *how*.

## Continuity is the contract

Coding-agent sessions die. Context windows compact. New sessions start cold. The continuity files exist so that none of that costs us progress.

**Five files carry the project across sessions.** A fresh agent reading them must be productive without re-deriving context from older conversations:

| File | Purpose |
|---|---|
| [STATUS.md](STATUS.md) | Current state — snapshot, invariants, active phase. |
| [DO_NEXT.md](DO_NEXT.md) | Resume checklist + in-flight sub-tasks. The cold-start entry point. |
| [PLAN.md](PLAN.md) | Roadmap, phase definitions, exit criteria, closed-phase index. |
| [WHAT_WE_DID.md](WHAT_WE_DID.md) | Reverse-chronological narrative. *Why* + surprises + root causes; not per-PR. |
| [BUGS.md](BUGS.md) | Every bug, filed *before* fixing. |

Plus this file ([AGENTS.md](AGENTS.md) = the rules) and [PHILOSOPHY.md](PHILOSOPHY.md) (the premise).

### The continuity-update rule

Every significant chunk of work updates:
- **STATUS.md** — Snapshot + active-phase section.
- **DO_NEXT.md** — Mark the sub-task ◐ or ✅; add follow-ups discovered.
- **WHAT_WE_DID.md** — Append per-phase narrative when a phase closes; mid-phase, fold surprising findings into the active-phase section there.

"Significant chunk" = anything that, if the next session started cold without it being recorded, would require re-deriving information from chat logs or git archaeology. Err on the side of writing it down.

### The BUG-first rule

When a bug surfaces — CI failure, conformance gap, fidelity defect, a fake/stub/placeholder spotted in the codebase — **file the BUG in [BUGS.md § Open](BUGS.md#open) before any fix commit.** One-liner is sufficient: ID + Sev + Area + source-API + one-line description. Detail belongs in the eventual fix commit, not in the bug entry.

Filing first is what creates the audit trail. Without it, "I fixed an issue" is unfalsifiable; with it, the project knows what it owed and paid.

## No fakes. No stubs. No mocks. No silent fallbacks. Ever.

Every line of code does real work or it does not exist. There is no middle ground.

**Specifically:**
- **Server handlers** must invoke the configured backend or return the cloud's own "not implemented / not supported" error in the cloud's error envelope. Never `return &SomeStruct{}, nil` as a placeholder.
- **Backend adapters** must call the real backend's API. No in-memory stand-ins for real cloud state, no canned-response paths.
- **Tests** run against the real shim with a real backend (passthrough, K8s peer, or cross-cloud). No mock objects, no fake HTTP servers in the SDK-conformance lane.
- **CI conformance** exercises the actual client SDK / CLI / Terraform provider against the shim endpoint. If a test can't work without a feature, implement the feature — don't mock around it.

If real implementation isn't feasible today, **file a BUG and surface it**. Do not silently degrade.

## Fidelity to the source cloud's API is P0

The shim's front door speaks the cloud's published API. The contract is:
- Response shapes match.
- Error envelopes match (XML for S3 errors, JSON for most others, ARM problem-details for Azure).
- HTTP status codes match.
- Async-operation semantics match (operation polling endpoints, ETag headers, long-poll behavior).
- Path templates, query-parameter names, header names — match.

Where the chosen backend can't honor a call honestly, the shim returns the source cloud's own error vocabulary for that situation: `NotImplementedException`, `OperationNotSupported`, `InvalidParameterValue`, etc. **Never fabricate success. Never substitute a generic 500.** This is the [PHILOSOPHY.md](PHILOSOPHY.md) "never lie" rule, made testable.

## The conformance contract

Every shimmed operation must be exercisable, in the same commit that registers the handler, via all three official client surfaces against every backend in scope:

1. **Cloud SDK** — `aws-sdk-go-v2/*`, `cloud.google.com/go/*`, `github.com/Azure/azure-sdk-for-go/*` (Go is canonical; Python + Node added per-service when relevant).
2. **Cloud CLI** — `aws`, `gcloud`, `az` shelled out via the test harness.
3. **Terraform provider** — the official `hashicorp/aws`, `hashicorp/google`, `hashicorp/azurerm` resource that wraps the operation, with `endpoints { ... }` override.

Tests live in `services/<svc>/conformance/<driver>-tests/`. A pre-commit / CI hook will (eventually) block any commit that registers a new operation without touching at least one test file for each driver. Operations that genuinely aren't exposed via SDK / CLI / Terraform (rare; e.g. internal control-plane probes) go on `services/<svc>/conformance/exempt.txt` with one operation per line.

There is no "land it and add tests later." If you edit a service, the conformance tests ship with it.

## Spec is the source of truth

Each shimmed service has a canonical published spec (AWS Smithy JSON, GCP protobuf, Azure OpenAPI). The codegen pipeline generates Go server stubs (handlers, types, error envelopes) from that spec. **Hand-written code is restricted to per-operation `translate.go` files** that map the source-API request to the backend's domain operation.

When the upstream cloud changes its spec, regenerate. The translation-table delta is the only thing to review by hand. Stale generated code is a bug (see [BUGS.md § Class-of-bug rules](BUGS.md#class-of-bug-rules-carried-forward)).

## Intersection-only scope

We shim only the operations and feature flags that exist across AWS + GCP + Azure + the chosen K8s peer for that service. A feature in one cloud only is not portable and is not eligible.

When a call lands on an out-of-intersection feature, return the source cloud's own "not supported" error. Don't fabricate, don't degrade silently, don't return an empty success.

This is the philosophical core. See [PHILOSOPHY.md § The Intersection](PHILOSOPHY.md#the-circle).

## Kubernetes is the fourth backend, always

Every shimmed service has a K8s peer on equal footing with the three clouds. The K8s peer ships in Phase 1 alongside the AWS/GCP/Azure backends — it is not an afterthought.

If no suitable open-source K8s-native peer exists for a service, we build it. See [PHILOSOPHY.md § The Fourth Wall](PHILOSOPHY.md#the-fourth-wall).

## No bug IDs in code comments

Once a bug is fixed, the fix speaks for itself. Code comments document *what* and *why*, not *which bug prompted it*. Bug lineage belongs in `BUGS.md`, the commit message, and the PR description — never in source.

> Good: `// S3 requires the bucket name in the path, not the host, for non-virtual-hosted requests.`
> Bad: `// BUG-42: S3 requires the bucket name in the path, not the host.`

## Branch hygiene

- One branch per phase / sub-phase. Many commits, one PR.
- Before pushing, rebase on `origin/main`:
  ```
  git fetch origin main
  git rebase origin/main
  ```
- After your PR is merged, sync local `main`:
  ```
  git checkout main && git pull origin main
  ```
- Never force-push to `main`. The ruleset blocks this anyway; admin bypass is for emergencies only.

## Never merge PRs

Create PRs with `gh pr create`. Never run `gh pr merge`. The user merges every PR.

## Always fix CI failures

If CI fails on your branch, fix it in the same PR — even if the failure is "pre-existing." Broken CI on any branch is not tolerated. If adding lint or expanding test coverage reveals old issues, fix them in the same PR.

## No silent deferrals

If something seems too hard, ambiguous, or out of scope, **ask the user** rather than silently dropping it. Returning `NotImplemented` or leaving a `TODO` without an open BUG + user notification is not acceptable.

"Best effort" does not mean "skip if inconvenient." It means handle errors gracefully while still performing the operation.

## How to add a new shimmed operation (the recipe)

1. Identify the operation in the source cloud's published spec.
2. Regenerate server stubs from the upstream spec if needed.
3. Add the `translate.go` mapping for each backend in scope (AWS / GCP / Azure / K8s — exclude only the source-cloud's passthrough if there's no translation to perform there).
4. Add **SDK + CLI + Terraform** conformance tests under `services/<svc>/conformance/`.
5. Update the per-service docs to reflect the new operation.
6. Run the full conformance lane locally; verify green against every backend in scope.
7. Open the PR with continuity-doc updates (STATUS / DO_NEXT / WHAT_WE_DID if a phase or sub-phase advanced).

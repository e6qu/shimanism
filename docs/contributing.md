# Contributing

Read [PHILOSOPHY.md](../PHILOSOPHY.md) first — the *why* every contributor should internalize before changing code. Then [AGENTS.md](../AGENTS.md) — the rules. This document is the contributor-facing summary.

## Continuity is the contract

Coding sessions die. Context windows compact. New sessions start cold. Five files at the repo root carry the project across sessions and **every significant chunk of work updates them**:

| File | Purpose |
|---|---|
| [STATUS.md](../STATUS.md) | Current state snapshot, invariants, active phase. |
| [DO_NEXT.md](../DO_NEXT.md) | Resume-from-cold checklist + in-flight sub-tasks. |
| [PLAN.md](../PLAN.md) | Roadmap, phase definitions, exit criteria, closed-phase index. |
| [WHAT_WE_DID.md](../WHAT_WE_DID.md) | Reverse-chronological narrative. *Why* + surprises + root causes; not per-PR. |
| [BUGS.md](../BUGS.md) | Every bug, filed *before* fixing. |

Plus [AGENTS.md](../AGENTS.md) (rules) and [PHILOSOPHY.md](../PHILOSOPHY.md) (premise).

"Significant chunk" = anything that, if the next session started cold without it being recorded, would require re-deriving information from chat logs or git archaeology. Err on the side of writing it down.

## The bug-first rule

When a bug surfaces — CI failure, conformance gap, fidelity defect, a fake/stub/placeholder spotted in the codebase — **file the BUG in [BUGS.md § Open](../BUGS.md#open) before any fix commit.** A one-liner is sufficient: ID + Sev + Area + source-API + one-line description. Detail belongs in the eventual fix commit, not in the bug entry.

Filing first is what creates the audit trail. Without it, "I fixed an issue" is unfalsifiable; with it, the project knows what it owed and paid.

## No fakes. No stubs. No mocks. No silent fallbacks.

Every line of code does real work or it does not exist. There is no middle ground. See [AGENTS.md § No fakes](../AGENTS.md#no-fakes-no-stubs-no-mocks-no-silent-fallbacks-ever). Concretely:

- **Server handlers** must invoke the configured backend or return the cloud's own "not implemented / not supported" error in the cloud's error envelope. Never `return &SomeStruct{}, nil` as a placeholder.
- **Backend adapters** must call the real backend's API. No in-memory stand-ins for real cloud state, no canned-response paths.
- **Tests** run against the real shim with a real backend (passthrough, K8s peer, or cross-cloud). No mock objects, no fake HTTP servers in the SDK-conformance lane.
- **CI conformance** exercises the actual client SDK / CLI / Terraform provider against the shim endpoint. If a test can't work without a feature, implement the feature — don't mock around it.

If real implementation isn't feasible today, **file a BUG and surface it**. Do not silently degrade.

## Branch + PR shape

- One branch per phase / sub-phase. Many commits, one PR.
- Before pushing, rebase on `origin/main`:
  ```sh
  git fetch origin main
  git rebase origin/main
  ```
- After your PR is merged, sync local `main`:
  ```sh
  git checkout main && git pull origin main
  ```
- Never force-push to `main`. The ruleset blocks this anyway.
- **Never merge PRs.** The repo owner merges every PR. Push, wait for CI green, ping them.
- **Always fix CI failures** in the same PR — even if the failure is "pre-existing." Broken CI on any branch is not tolerated.
- **No silent deferrals.** If something is too hard, ambiguous, or out of scope, **ask the user** rather than silently dropping it. Returning `NotImplemented` or leaving a `TODO` without an open BUG + user notification is not acceptable.

## Commit messages

Follow the repo's existing style: imperative, descriptive, no trailing periods on subject lines. Body explains *why*, not just *what*. For BUG closures, reference the BUG ID and what changed:

```
Phase 10.3 (second chunk): close BUG-2 — AWS SQS SetQueueAttributes

Carried since Phase 3 (5 phases). Closing it unblocks AWS-shape
Apply through hashicorp/aws's CreateQueue → SetQueueAttributes
reconciliation flow ...

domain.Queues gains SetQueueAttributes(name, QueueAttributes) with
honest per-backend implementations:
  inmem  patches in place
  aws    SQS SetQueueAttributes
  gcp    subscriptions.patch with updateMask
  ...

BUGS.md: BUG-2 moved to resolved table.
```

**No bug IDs in code comments.** Once a bug is fixed, the fix speaks for itself. Bug lineage belongs in [BUGS.md](../BUGS.md), the commit message, and the PR description — never in source. See [AGENTS.md § no bug IDs in code](../AGENTS.md#no-bug-ids-in-code-comments).

## Code style

- Default to writing no comments. Only add one when the *why* is non-obvious: a hidden constraint, a subtle invariant, a workaround for a specific bug, behavior that would surprise a reader.
- Don't explain *what* the code does — well-named identifiers already do that.
- Don't reference the current task, fix, or callers — those belong in the PR description and rot as the codebase evolves.
- Don't add features, refactor, or introduce abstractions beyond what the task requires.
- Don't add error handling, fallbacks, or validation for scenarios that can't happen. Trust internal code and framework guarantees. Only validate at system boundaries (user input, external APIs).

## Adding a new shimmed operation

See [docs/development.md § adding a new operation](development.md#adding-a-new-operation) for the full recipe. The short form:

1. Identify the operation in the source cloud's published spec.
2. Regenerate server stubs if needed.
3. Add the `translate.go` mapping for each backend in scope.
4. Add SDK + CLI + Terraform conformance tests under `services/<svc>/conformance/`.
5. Update the per-service docs.
6. Run the full conformance lane locally; verify green.
7. Open the PR with continuity-doc updates.

## Asking for help

The repo follows an explicit "ask the user" preference over silent deferral. Open a draft PR + `git push`, then describe the question + your current hypothesis. The continuity docs are how reviewers ramp up; reference them.

## License

shimanism is AGPL-3.0-only. Every dependency we link must carry a license on the allowlist in [`docs/compatible-licenses.md`](../docs/compatible-licenses.md). When in doubt, **check first, add second.** See [`docs/dependency-policy.md`](../docs/dependency-policy.md) for the policy beyond legal compatibility.

## Cross-link

- [AGENTS.md](../AGENTS.md) — full rules for human and LLM contributors.
- [PHILOSOPHY.md](../PHILOSOPHY.md) — the *why*.
- [docs/development.md](development.md) — local setup, build, run, debug.
- [docs/testing.md](testing.md) — the conformance contract.
- [docs/codegen.md](codegen.md) — spec-to-server-stub pipeline.
- [docs/releasing.md](releasing.md) — release flow.

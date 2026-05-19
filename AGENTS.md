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

## The shim is stateless

shimanism is a pure translation layer. **The shim binary holds no state of record.** All persistent state lives in the backend — the destination cloud's own storage, the K8s peer's data plane, or (for tests) the in-memory fixture that *is* the backend.

What this means in practice:

- **No sidecar storage.** No SQLite, no Redis, no shim-managed bucket / table / namespace for "auxiliary metadata", no in-process cache that the shim treats as a source of truth.
- **No shim-owned key/value mappings**, even on disk or in the destination cloud's tag set, that the shim *itself* needs in order to answer a future request. If the backend can be re-read to derive the same answer, the shim re-reads — it does not cache.
- **Per-request scratch is fine.** A handler can read upstream state to compute its response (e.g. listing versions to derive a monotonic version index). What's forbidden is *persisting* anything across requests in the shim.
- **Multipart-style coordination state goes in the backend.** GCS multipart writes its part objects + marker into GCS itself (under a `.uploads/<id>/` prefix); Azure block-blob staged blocks live in Azure; AWS S3 multipart lives in AWS. The shim doesn't hold the upload-ID-to-part-list mapping — the backend does.
- **Cross-cloud shape translations that need a stable mapping** (e.g. Azure's GUID version handles ↔ a monotonic integer the AWS frontend wants to expose) **derive the mapping at request time** from data the backend already keeps, like creation timestamps. The shim doesn't maintain a translation table.

Why: a stateless shim scales horizontally (any replica answers any request), restarts cleanly (no warmup, no recovery), and never holds the last-writer-wins position that would cause split-brain in a multi-replica deployment. Most importantly, it can't *lie* — every answer comes from the backend that actually owns the data.

If a feature can't be implemented statelessly, **it's out of intersection** — return the source cloud's "not supported" error. Don't add state to make it work.

## In-tree K8s peer: one package, common denominator

When a shimmed service's K8s-peer slot doesn't have a clean third-party OSS fit, the in-tree [`peers/shimanism/`](peers/shimanism/) package fills it. **One** package, **one** binary, **one** Store interface — not a fleet of per-service shim peers.

The common denominator every shimmed service reduces to:

- Named, versioned binary objects.
- Per-object structured metadata (`map[string]string`).
- Soft-delete + force-delete lifecycle.
- List with prefix + pagination.
- Multi-namespace addressing so one deployment serves many shim services.

That's the whole `peers/shimanism/peer.go` interface. The shim service's frontend handles the per-cloud shape (S3 / Vault / SQS / Lambda / …); the peer just stores the bytes. Don't add service-specific knowledge to the peer.

The peer lives in its own Go module so it can be deployed and upgraded on its own cadence; importing it from `services/<svc>/backends/` is optional and only happens when a phase actually surfaces the gap. Phase 1 (storage) and Phase 2 (secrets) used MinIO and Vault — the peer module didn't ship code, only the interface contract.

## Fidelity to the source cloud's API is P0

The shim's front door speaks the cloud's published API. The contract is:
- Response shapes match.
- Error envelopes match (XML for S3 errors, JSON for most others, ARM problem-details for Azure).
- HTTP status codes match.
- Async-operation semantics match (operation polling endpoints, ETag headers, long-poll behavior).
- Path templates, query-parameter names, header names — match.

Where the chosen backend can't honor a call honestly, the shim returns the source cloud's own error vocabulary for that situation: `NotImplementedException`, `OperationNotSupported`, `InvalidParameterValue`, etc. **Never fabricate success. Never substitute a generic 500.** This is the [PHILOSOPHY.md](PHILOSOPHY.md) "never lie" rule, made testable.

## The conformance contract

Every shimmed operation must be exercisable, in the same commit that registers the handler, via the matching cloud's official client surfaces — **for every frontend × every backend in scope.** Per [PLAN.md principle 11](PLAN.md#guiding-principles) each phase carries the full 3 frontends × 3 driver types × 4 backends = 36 driver-backend matrix.

A frontend's drivers are always the matching cloud's tooling — not a cross-cloud substitute. The AWS-shaped frontend is tested by AWS tools, the GCS-shaped frontend by GCP tools, the Azure-shaped frontend by Azure tools. Driving the AWS frontend with `gcloud` (or vice versa) isn't a meaningful conformance test — the wire protocols don't match.

| Frontend | SDK | CLI | Terraform provider |
|---|---|---|---|
| AWS | `aws-sdk-go-v2/*` | `aws` | `hashicorp/aws` with `endpoints { ... }` |
| GCP | `cloud.google.com/go/*` | `gcloud` with `--api-endpoint-overrides` | `hashicorp/google` with endpoint overrides |
| Azure | `github.com/Azure/azure-sdk-for-go/*` | `az` | `hashicorp/azurerm` |

Go is canonical for the SDK row; Python + Node added per-service when relevant.

Tests live in `services/<svc>/conformance/<frontend>/<driver>-tests/` — one driver-test directory per (frontend, driver) pair. A pre-commit / CI hook will (eventually) block any commit that registers a new operation without touching at least one test file for each frontend × each driver. Operations that genuinely aren't exposed via SDK / CLI / Terraform (rare; e.g. internal control-plane probes) go on `services/<svc>/conformance/exempt.txt` with one operation per line.

There is no "land it and add tests later." If you edit a service, the conformance tests ship with it.

## Spec is the source of truth

Each shimmed service has a canonical published spec (AWS Smithy JSON, GCP protobuf / Discovery doc, Azure OpenAPI). The codegen pipeline generates Go server stubs (handlers, types, error envelopes) from that spec. **Hand-written code is restricted to per-operation `translate.go` files** that map the source-API request to the backend's domain operation.

When the upstream cloud changes its spec, regenerate. The translation-table delta is the only thing to review by hand. Stale generated code is a bug (see [BUGS.md § Class-of-bug rules](BUGS.md#class-of-bug-rules-carried-forward)).

## Reuse over reinvention

Where the cloud's official tooling fits, use it. The shim's job is to **match** the cloud's published API, not to maintain a parallel implementation that drifts. This is locked-in decision #11 in [PLAN.md](PLAN.md#locked-in-decisions); the rules below make it operational.

**Spec inputs.** Always vendor from upstream-canonical; never fork. AWS = Smithy JSON from `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models`. GCP = protobuf from `googleapis/googleapis` and/or the Discovery doc at the documented URL. Azure = OpenAPI v3 from `Azure/azure-rest-api-specs`.

**Wire types.** Prefer reusing the official Go SDK's wire-type structs over re-emitting equivalents:

| Cloud | Reusable wire-type package |
|---|---|
| AWS | `github.com/aws/aws-sdk-go-v2/service/<svc>/types` |
| GCP REST | `google.golang.org/api/<svc>/v1` (generated from Discovery) |
| GCP gRPC | the proto-generated structs in `cloud.google.com/go/<svc>` |
| Azure | the SDK's internal `generated/` package (the types `azblob` etc. use under the hood) |

Re-emit server-side types only when SDK types fight server-side handling — for example, client-only fields, pointer-heavy shapes that don't round-trip, or struct tags that target the SDK's middleware rather than direct (un)marshalling. When re-emitting, generate from the same spec the SDK is generated from, not from a copy.

**Server-side codegen.** For each spec format, prefer the most authoritative existing generator:

| Spec format | First choice | When to fall back to a custom emitter |
|---|---|---|
| Smithy (AWS) | Custom emitter (no official Smithy → Go *server* generator exists; `smithy-go` is client-side). | n/a — custom is the only option. |
| OpenAPI v3 (Azure) | `oapi-codegen` server-stubs. | When generated stubs can't match the shim's handler shape after reasonable adapter glue. |
| Discovery / protobuf (GCP) | Reuse `google.golang.org/api` wire types directly; emit only the routing + dispatch layer. | When the generated types are too SDK-coupled to import cleanly. |

The codegen pipeline owns the diff between spec and emitted code. The translation table (in `translate.go`) is the only file an agent should write by hand.

**Auth verification.** Use the cloud's official signer/verifier libraries — never roll a SigV4 / OAuth2 / SharedKey implementation. AWS = `aws-sdk-go-v2/aws/signer/v4`. GCP = `golang.org/x/oauth2`. Azure = the signer in `azure-sdk-for-go/sdk/azcore/auth` and the SharedKey verifier exposed by the storage SDK.

**Validation.** The cloud's spec carries field-level constraints (string lengths, enum sets, pattern regexes). Honor them at the wire-decode boundary so an invalid request fails with the **source cloud's own error vocabulary**, not a generic 500. When the spec generator emits validation (it does for Smithy and OpenAPI), wire it in.

**When to *not* reuse.** Reuse is a tool, not a contract. If reusing a piece of SDK or generator output forces the shim to lie (synthetic responses, swallowed errors, fabricated success), drop the reuse and emit our own honest implementation. The fidelity rule beats the reuse rule.

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

## Dependency updates are handled by Renovate

Renovate (config in `.github/renovate.json5`) opens weekly batched PRs for Go module and GitHub Actions updates. Major-version bumps get their own PR; security advisories surface immediately regardless of schedule. The Renovate GitHub App must be installed on the repo for this to take effect.

Renovate **never auto-merges** — same rule as everything else (user merges every PR). Review Renovate PRs like any other: run conformance locally, check the changelogs of bumped deps, then merge.

When Renovate proposes a new transitive dep, the `licenses` CI job verifies it's on the allowlist *before* merge. A Renovate PR that fails license-check is a signal that an upstream relicensed; file a BUG in [BUGS.md](BUGS.md) and decide whether to pin to the last compatible version or replace the dep.

## Dependency licenses must be AGPL-compatible

shimanism is AGPL-3.0-only. Every Go module we *link* (anything in `go.mod` / `go.sum`) must carry a license on the allowlist in [`doc/COMPATIBLE_LICENSES.md`](doc/COMPATIBLE_LICENSES.md). CI enforces this; `make license-check` runs the same check locally.

Services we *connect to over the wire* (Vault, MinIO, Postgres, Terraform CLI, etc.) carry no copyleft obligation and may use any license, including BUSL or proprietary. See the doc for the linked-vs-connected distinction.

When in doubt about a new dependency, **check first, add second.** A failed `license-check` is a bug to file in [BUGS.md](BUGS.md), not a CI hurdle to work around.

## Dependency policy (beyond licenses)

[`doc/DEPENDENCY_POLICY.md`](doc/DEPENDENCY_POLICY.md) covers what we accept beyond legal compatibility:

- **Minimum release age: 48 hours.** Renovate enforces; manual additions should too. Mitigates supply-chain attacks where malicious releases get yanked within ~1 day.
- **Pin GitHub Actions to immutable SHAs.** Tags are mutable; SHAs are not. Renovate keeps both fresh.
- **Go: prefer pure-Go deps over cgo.** Cross-compilation, smaller binaries, no system-library dependency. cgo only when there's no equivalent pure-Go alternative; justification in the PR.
- **npm (when we eventually need it): pnpm only, lifecycle scripts disabled.** Deps that require pre-install/post-install scripts get patched, replaced, or rejected.

When a needed dep doesn't fit, file a BUG with the rationale before proposing an exception.

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

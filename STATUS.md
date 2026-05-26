# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #37 merged 2026-05-26 closed the end-to-end-walkthrough fidelity-bug cluster (BUG-30..33 / GitHub #32-#35). |
| In-flight | **Phase 14 — Sockerless-verified validation lane + deferred follow-ons.** **14.A done** (2026-05-24): sockerless PR #179 closed all 6 round-1 issues (#173-178); shim assertions re-enabled. **14.D audit done through PR #216** (2026-05-25): all previously filed sockerless audit issues closed. **14.B current lane is green** against sockerless `f858d66` / PR #221: `make sockerless` passes the backend-adapter sockerless lanes across storage AWS/GCS/Azure Blob, secrets AWS/GCP/Azure KV, queue AWS/GCP, pubsub GCP, and apigateway GCP. **The four end-to-end-walkthrough fidelity bugs surfaced after PR #36 (#32 xmlFlattened codegen, #33 GCS region/timeCreated, #34 Azure ETag, #35 docs+conformance) closed in PR #37.** [sockerless#220](https://github.com/e6qu/sockerless/issues/220) closed by sockerless PR #221 on 2026-05-26 — `List Containers` now emits the per-container `<Properties>` block. The shim wires Azure container `ETag` through `domain.Bucket.ETag` so real values flow through; the synthetic `"shim"` mitigation is gone. BUG-24 remains open to expand the same through-shim sockerless pattern to the remaining service families. 14.C (full handler migrations for 9 blank-import frontends) still independent + pending. |
| Last merged | PR #37 — fix the end-to-end-walkthrough fidelity bugs (BUG-30..33) on `main`, 2026-05-26. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | **33 filed · 30 fixed · 2 open · 1 false positive.** BUG-8 remains the GCP Terraform-provider endpoint/OAuth leg; BUG-15 remains the hashicorp/google Terraform state-drift question; BUG-24 tracks expanding through-shim sockerless E2E beyond storage. BUG-30..33 (this PR) are all marked fixed end-to-end — sockerless PR #221 closed the upstream `List Containers` `<Properties>` gap and the shim's domain layer now propagates real Azure container ETags, eliminating the last synthetic placeholder. No upstream sockerless blocker is open at this checkpoint. |
| CI | 18 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config + custom manager for vendored-spec SHAs (12.0.15). **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. |
| Live infra | None. |

## Toolchain (locked-in across phases)

- **Codegen** flows through `make codegen` across three lanes: `cmd/codegen` (AWS Smithy, 5 protocols) · `cmd/azure-codegen` (Azure OpenAPI v2; 8-stage preprocessor) · `cmd/gcp-codegen` (GCP Discovery routing-only). `make codegen-check` (also runs `inject-provenance`) is the deterministic guard; CI's `codegen deterministic` job runs the same. Pipeline architecture in [docs/codegen-pipelines.md](docs/codegen-pipelines.md).
- **Vendored-spec provenance.** Every spec under `services/*/spec/` + `services/common-types/` carries a `_provenance` top-level key derived from SOURCES.md. `cmd/inject-provenance` writes it; three fetch scripts (`scripts/fetch-{aws,azure,gcp}-*.sh`) run the injector after download. CI guards: `TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance`.
- **Spec freshness.** `make spec-freshness` + weekly workflow (Mondays 14:00 UTC) surface upstream drift. Renovate custom manager tracks vendored-spec SHAs and opens issues on drift.
- **Signature verification.** Per-cloud verifiers under `internal/{sigv4verifier,gcpbearer,azurebearer,azuresharedkey}/` wrap every frontend at the harness layer. Test mode HS256 with project-owned key. Production RS256 JWKS is Phase 13.C. Architecture in [docs/verifiers.md](docs/verifiers.md).

## Invariants

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user.
- **Single-branch rule.** All work for one phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then commit.
- **Update continuity docs every significant chunk** (not just at phase end).
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.
- **One plan file.** PLAN.md is the only roadmap doc.

### Architecture
- **Fidelity to the source cloud's API.** Error shapes, headers, status codes, async semantics — match. Out-of-intersection calls fail loud in the source cloud's own error vocabulary.
- **Real backends, never emulators.**
- **Stateless shim.** No sidecar storage, no shim-managed namespace, no in-process cache treated as authoritative.
- **Intersection-only scope.**
- **Kubernetes is a first-class fourth backend.**
- **No fakes, no fallbacks, no degraded modes.**
- **Test from the official client surfaces.** SDK + CLI + Terraform per frontend per backend.

Full locked-in decisions table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions).

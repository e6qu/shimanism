# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #37 merged 2026-05-26 closed the end-to-end-walkthrough fidelity-bug cluster (BUG-30..33 / GitHub #32-#35). |
| In-flight | **Phase 14 — Sockerless-verified validation lane + deferred follow-ons.** **14.A done** (2026-05-24). **14.D audit done through PR #216** (2026-05-25). **14.B in-progress on branch `phase-14-bundled-bce-bug24`**: added 3 new Azure ARM backend-adapter sockerless lanes (Cache for Redis, PostgreSQL FlexibleServer, APIM); Container Apps lane scaffolded + default-skipped (BUG-35) pending pre-pulled-image opt-in. Service Bus queue + pubsub blocked on [sockerless#223](https://github.com/e6qu/sockerless/issues/223) (BUG-34) — sim lacks the namespace-level ATOM XML admin protocol the `azservicebus/admin` SDK uses. `make sockerless` now reports 23 passing + 1 skipped. 14.C (full handler migrations) deferred — azure_blob is 69 ops; 7 GCP frontends are cosmetic per existing TestGCPRoutes_*_FrontendDispatchCoverage pinning. 14.E (cross-cloud Apply matrix expansion) deferred to a follow-on PR. |
| Last merged | PR #37 — fix the end-to-end-walkthrough fidelity bugs (BUG-30..33) on `main`, 2026-05-26. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | **35 filed · 30 fixed · 4 open · 1 false positive.** BUG-8 / BUG-15 remain Track A residuals (real-cloud TF provider legs). BUG-24 stays open for reverse-direction through-shim expansion. **BUG-34** (new this PR) — Azure SB admin ATOM XML not in sockerless, blocks SB queue+pubsub lanes; tracked at [sockerless#223](https://github.com/e6qu/sockerless/issues/223). **BUG-35** (new this PR) — Container Apps lane needs pre-pulled-image plumbing; lane wired but default-skipped. |
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

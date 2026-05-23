# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-14` (created from `main` 2026-05-24 after PR #20 merged at `3cf9e13`). |
| In-flight | **Phase 14 — Sockerless-verified validation lane + deferred follow-ons.** **14.A done** (2026-05-24): sockerless PR #179 closed all 6 round-1 issues (#173-178); shim assertions re-enabled — AWS S3 PutObject/GetObject round-trip + AWS Secrets Manager HeadSecret/GetSecretValue now exercise end-to-end through `make sockerless-storage`. **14.D fidelity audit done** (2026-05-24): 8 round-2 sockerless issues filed ([#181](https://github.com/e6qu/sockerless/issues/181) Azure Redis case sensitivity, [#182](https://github.com/e6qu/sockerless/issues/182) GCP Pub/Sub field drops — likely closes BUG-15, [#183](https://github.com/e6qu/sockerless/issues/183) GCP Secret Manager + Operations routing leak, [#184](https://github.com/e6qu/sockerless/issues/184) Azure Key Vault malformed kid URLs, [#185](https://github.com/e6qu/sockerless/issues/185) Azure Key Vault placeholder modulus, [#186](https://github.com/e6qu/sockerless/issues/186) AWS SQS attribute drops, [#187](https://github.com/e6qu/sockerless/issues/187) GCP Cloud SQL relative selfLink, [#188](https://github.com/e6qu/sockerless/issues/188) GCP Secret Manager unresolved `latest` alias). **14.B + 14.C pending**: shim-side new-service lanes (gated on round-2 fixes) + full handler migrations for the 9 Phase-13 blank-import frontends (independent of sockerless). |
| Last merged | PR #20 — Phase 13 at `3cf9e13` on `main`, 2026-05-24. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | 20 filed · 18 fixed · 2 open · 1 false positive. Both open bugs (BUG-8, BUG-15) absorbed into Phase 14 — likely closed via sockerless#182 fix + new GCP APIGW lane once round-2 fixes land. Plus 14 upstream-sockerless issues: 6 round-1 (✅ closed by sockerless PR #179) + 8 round-2 (open). |
| CI | 18 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config + custom manager for vendored-spec SHAs (12.0.15). **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. |
| Live infra | None. |

## Toolchain (locked-in across phases)

- **Codegen** flows through `make codegen` across three lanes: `cmd/codegen` (AWS Smithy, 5 protocols) · `cmd/azure-codegen` (Azure OpenAPI v2; 8-stage preprocessor) · `cmd/gcp-codegen` (GCP Discovery routing-only). `make codegen-check` (also runs `inject-provenance`) is the deterministic guard; CI's `codegen deterministic` job runs the same. Pipeline architecture in [doc/CODEGEN.md](doc/CODEGEN.md).
- **Vendored-spec provenance.** Every spec under `services/*/spec/` + `services/common-types/` carries a `_provenance` top-level key derived from SOURCES.md. `cmd/inject-provenance` writes it; three fetch scripts (`scripts/fetch-{aws,azure,gcp}-*.sh`) run the injector after download. CI guards: `TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance`.
- **Spec freshness.** `make spec-freshness` + weekly workflow (Mondays 14:00 UTC) surface upstream drift. Renovate custom manager tracks vendored-spec SHAs and opens issues on drift.
- **Signature verification.** Per-cloud verifiers under `internal/{sigv4verifier,gcpbearer,azurebearer,azuresharedkey}/` wrap every frontend at the harness layer. Test mode HS256 with project-owned key. Production RS256 JWKS is Phase 13.C. Architecture in [doc/VERIFIERS.md](doc/VERIFIERS.md).

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

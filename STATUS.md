# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #47 merged 2026-05-27 (Phase 13.A.6: `azure_blob` full `gen.ServerInterface` impl — 12 in-intersection bridges + 57 out-of-intersection stubs). |
| In-flight | **3-PR plan now 2/3 shipped.** PR #46 closed Phase 14.B + 14.C. PR #47 closed Phase 13.A.6 (the last ◐ Azure-adapter migration). PR 3 (Track A: BUG-8 + BUG-15 + real-signed verifier conformance) stays blocked on real-cloud credentials. 37 sockerless lanes passing + 1 documented-skipped (Container Apps on amd64, awaiting sockerless#244). 14.E cross-cloud Apply deferred — shim needs ARM-shimming on its own side before sockerless#243 is on its critical path. **Practical next chunks** while Track A is blocked: BUG-24 reverse-direction expansion (5 remaining service families), or starting the shim-side ARM-shimming workstream that unblocks 14.E. |
| Last merged | PR #47 — Phase 13.A.6: `azure_blob` full handler migration, 2026-05-27. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | **38 filed · 35 fixed · 3 open · 1 false positive.** Open: **BUG-8** (apigateway TF-provider Track A leg, real GCP), **BUG-15** (queue TF state-drift Track A leg, real GCP), **BUG-35** (Container Apps lane skipped by default on amd64; the shim-side script plumbing landed in PR #46 — the lane unblocks once sockerless#244 lands). |
| Upstream | Two sockerless tracking issues: [#243](https://github.com/e6qu/sockerless/issues/243) (Azure ARM endpoint emission across non-Storage services — maintainer reframed scope; not on shimanism's critical path because shimanism's 14.E needs shim-side ARM-shimming, with sockerless on the destination/AWS side), [#244](https://github.com/e6qu/sockerless/issues/244) (Container Apps `linux/arm64` hardcode — blocks BUG-35 closure on amd64 runners). Earlier follow-ons #239/#240/#241 closed via sockerless PR #242. |
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

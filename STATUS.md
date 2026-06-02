# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-16-plan` — Phase 16 planning docs (PLAN.md + STATUS.md + DO_NEXT.md). |
| In-flight | Phase 16 plan committed; 16.A is next (scoping doc + N20–N27 + ec2Query codegen lane). |
| Last merged | PR #101 — Phase 15 closure narrative. Phase 15 fully closed (15.A ✅ 15.B ✅ 15.C ✅ 15.D ✅). |
| Upstream watch | Sockerless PRs #368–#372 merged. PR #372 added Firecracker VM lifecycle; three follow-up issues open: [#373](https://github.com/e6qu/sockerless/issues/373) (kvm check), [#374](https://github.com/e6qu/sockerless/issues/374) (disk), [#375](https://github.com/e6qu/sockerless/issues/375) (caching). These block 16.C instance sockerless lane + 16.D RegisterTargets lane. |
| Phases 1–13 | All closed (Phase 13 closed by PR #20). PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 14 | 14.A ✅ · 14.B ✅ · 14.C ✅ · 14.D ✅ (simulator audit) / Track A blocked · 14.E ✅. Remaining residuals (terraform-aws `_wo` drift, SB cross-cloud scoping) carried into Phase 15.B. |
| Phase 15 | ✅ All sub-phases closed: 15.A ✅ · 15.B ✅ · 15.C ✅ (PRs #90–#100) · 15.D ✅ (PR #89). |
| Phase 16 | **Planned.** 16.A (scoping + ec2Query codegen) → 16.B (VPC networking) → 16.C (instances; sockerless gated on #373–375) → 16.D (load balancers, parallels 16.B). Services: `services/compute/` + `services/loadbalancer/`. |
| Bugs | **51 filed · 47 fixed · 3 open · 1 false positive.** Open: **BUG-8** + **BUG-15** + **BUG-41** (all Track A / third-party — blocked on real-cloud credentials). BUG-48/49/50/51 ✅ closed. |
| Upstream | All prior sockerless gaps closed. Current watch in DO_NEXT.md § Upstream watch. |
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

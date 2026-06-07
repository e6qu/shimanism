# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` — Phase 21 complete. No in-flight branch. |
| In-flight | Nothing. |
| Last merged | Phase 21.C Azure App Gateway + K8s Ingress + full CLI/TF conformance matrix — PR #158. |
| Upstream watch | Registry sockerless: BUG-67/#465 remains open. BUG-65/#451 and BUG-66/#452/#469 are closed on current sockerless main. |
| Phases 1–19 | 1–19 closed. See [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 19 | ✅ complete — 19.A (#127) · 19.B (#128) · 19.C (#129) · 19.D (#130 CLI/TF, #131 sockerless). All 4 backends, full SDK/CLI/TF, all sockerless lanes green, zero skips. |
| Phase 18 | ✅ complete — PRs #132–#141. OCI Distribution data plane + ECR/AR/ACR frontends + connected backends + registry docs. Simulator-only gap BUG-67 remains; BUG-64/#450, BUG-65/#451, and BUG-66/#452/#469 are closed on current sockerless main. |
| Bugs | **80 filed · 71 fixed · 8 open · 1 false positive.** Open: **BUG-8** + **BUG-15** + **BUG-41** (Track A) + **BUG-67** (sockerless AWS ECR) + **BUG-68/69** (local sockerless-runner/KMS-lane findings). BUG-76+77 fixed in Phase 20.E. BUG-78 fixed in Phase 21.A. BUG-79+80 filed and fixed in Phase 21.C. |
| CI | 20 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config + custom manager for vendored-spec SHAs. **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. PR #146 was a one-time user-authorized exception and has already been merged. **One PR open at a time** — ask before opening if one's active. |

## Toolchain (locked-in across phases)

- **Codegen** flows through `make codegen` across three lanes: `cmd/codegen` (AWS Smithy, 5 protocols) · `cmd/azure-codegen` (Azure OpenAPI v2; 8-stage preprocessor) · `cmd/gcp-codegen` (GCP Discovery routing-only). `make codegen-check` (also runs `inject-provenance`) is the deterministic guard; CI's `codegen deterministic` job runs the same.
- **Vendored-spec provenance.** Every spec under `services/*/spec/` carries a `_provenance` key. `cmd/inject-provenance` writes it; three fetch scripts run the injector after download. CI guards: `TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance`.
- **Spec freshness.** `make spec-freshness` + weekly workflow surface upstream drift. Renovate custom manager tracks vendored-spec SHAs.
- **Signature verification.** Per-cloud verifiers under `internal/{sigv4verifier,gcpbearer,azurebearer,azuresharedkey}/` wrap every frontend at the harness layer.

## Invariants

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user.
- **Single-branch rule.** All work for one phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then commit.
- **Update continuity docs every significant chunk** (not just at phase end).
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.

### Architecture
- **Fidelity to the source cloud's API.** Error shapes, headers, status codes — match. Out-of-intersection calls fail loud.
- **Real backends, never emulators.**
- **Stateless shim.** No sidecar storage, no shim-managed namespace, no in-process cache as source of truth.
- **Intersection-only scope.**
- **Kubernetes is a first-class fourth backend.**
- **No fakes, no fallbacks, no degraded modes.**
- **Test from the official client surfaces.** SDK + CLI + Terraform per frontend per backend.

Full locked-in decisions table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions).

# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-18d-registry-cloud-backends` — Phase 18.D (Container Registry connected cloud backends). |
| In-flight | 18.D PR2: real AWS ECR backend using AWS SDK control APIs + ECR's own `/v2/` data-plane credentials; BUG-61 fixed in the ECR frontend. GCP AR + Azure ACR connected backends remain next. |
| Last merged | PR #139 — 18.D PR1: CNCF `distribution` connected backend + live Distribution push/pull conformance gate. |
| Upstream watch | All KMS sockerless gaps closed: #407 (PR #412), #413 (PR #415), #419 GCP Cloud KMS sim (PR #422), #423 Azure no-version crypto (PR #425). |
| Phases 1–19 | 1–17 + 19 closed; 18 (Container Registry) starting. See [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 19 | ✅ complete — 19.A (#127) · 19.B (#128) · 19.C (#129) · 19.D (#130 CLI/TF, #131 sockerless). All 4 backends, full SDK/CLI/TF, all sockerless lanes green, zero skips. |
| Phase 18 | 18.A complete; 18.B GCP Artifact Registry complete; 18.C AWS ECR + Azure ACR complete; 18.D in progress. OCI Distribution data plane + ECR/AR/ACR frontends + connected backends. |
| Bugs | **59 filed · 55 fixed · 3 open · 1 false positive.** Open: **BUG-8** + **BUG-15** + **BUG-41** (Track A). |
| CI | 20 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config + custom manager for vendored-spec SHAs. **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. **One PR open at a time** — ask before opening if one's active. |

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

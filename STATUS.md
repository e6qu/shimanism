# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #54 merged 2026-05-28, with PR #55 closed + revert PR pending: PRs #51–#54 introduced ARM-shimming fakes that violate the "no fakes / stateless shim" rules. PR-in-flight reverts that work; sockerless#259 unblocks the honest architecture. |
| In-flight | **Revert PR #51–#54's ARM-shimming fakes.** Removes `internal/storage/frontends/azure_arm_storageaccounts/`, `internal/secrets/frontends/azure_arm_keyvault/`, `internal/mockaad/`, the `armResourcesStub` middleware, the synthetic ARM responses, the `Track*` in-process state, the `StorageAccountsListKeys` hardcoded synthetic key, the two through-shim ARM sockerless cells. The Makefile glob change for `azure*codegen.json` + `scripts/fetch-azure-spec.sh` SOURCES.md auto-append + `cmd/azure-codegen` bare-sibling `$ref` recognition stay (those are general improvements, not fake-specific). `make sockerless` 45 → **43 passing** (the 2 ARM cells removed). The honest path for 14.E cross-cloud Apply: sockerless#259 (merged 2026-05-28) added configurable Azure ARM data-plane endpoint emission via `SIM_AZURE_ARM_EXTERNAL_DATA_PLANE_URLS_JSON`; a future PR will wire shimanism's harness to set this env var pointing at the shim's data-plane URLs, letting `azurerm → sockerless real ARM → shim data plane → backend` compose without any shim-side fakes. Track A blocked on real-cloud credentials. |
| Last merged | PR #54 — 14.E.4: mock Microsoft Entra + first through-shim azurerm Terraform Apply (storage), 2026-05-28. **Note:** the in-flight revert PR removes this. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | **38 filed · 36 fixed · 2 open · 1 false positive.** Open: **BUG-8** (apigateway TF-provider Track A leg, real GCP), **BUG-15** (queue TF state-drift Track A leg, real GCP). BUG-35 closed by sockerless PR #245. |
| Upstream | sockerless [#257](https://github.com/e6qu/sockerless/issues/257) closed via [PR #259](https://github.com/e6qu/sockerless/pull/259) — configurable Azure ARM data-plane endpoint emission landed. Two follow-on gaps still open before through-shim azurerm Apply composes with honest verification: [#260](https://github.com/e6qu/sockerless/issues/260) (storage listKeys per-account 32-byte deterministic keys), [#261](https://github.com/e6qu/sockerless/issues/261) (RS256-signed Azure AD tokens with real JWKS). Both block shim-side verifier alignment. |
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

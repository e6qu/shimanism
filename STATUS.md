# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #44 merged 2026-05-27 (storage CopyObject lanes; closed BUG-37 + BUG-38). |
| In-flight | **Phase 14.B nearly complete.** 33 sockerless lanes passing + 1 documented-skipped. Storage matrix complete 3×3 (single-shot + multipart + copy across AWS S3 + GCS + Azure Blob). SB admin + raw-AMQP/TLS lanes both green. ARM-based Azure lanes (Redis / PG / APIM) green. **3 PRs planned to close out remaining Phase-14.B/C/E work** — see [DO_NEXT.md § The 3-PR closure plan](DO_NEXT.md#the-3-pr-closure-plan) for the concrete task list. 14.D (Track A real-cloud) stays blocked on infra. |
| Last merged | PR #44 — Phase 14.B storage CopyObject sockerless lanes (closes BUG-37 + BUG-38), 2026-05-27. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | **38 filed · 35 fixed · 3 open · 1 false positive.** Open: **BUG-8** (apigateway TF-provider Track A leg, real GCP), **BUG-15** (queue TF state-drift Track A leg, real GCP), **BUG-35** (Container Apps pre-pull — shim-side script change, planned PR 1). |
| Upstream | 3 sockerless follow-ons open after PR #235/#238 reviews ([#239](https://github.com/e6qu/sockerless/issues/239) validation, [#240](https://github.com/e6qu/sockerless/issues/240) redundant clone, [#241](https://github.com/e6qu/sockerless/issues/241) write-guard) — all internal-only; none block any shim lane. |
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

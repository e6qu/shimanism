# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-13` (created from `main` 2026-05-22 after PR #19 merged). |
| In-flight | **Phase 13 — Full adapter migration + production auth + real-cloud Track A.** All 7 Azure + 8 GCP frontends now sit on the gen-inventory spec-drift contract on PR #20. **Full handler migrations** (Server implements gen.ServerInterface; handlers dispatch through the gen path): `azure_redis` (13.A.1), `azure_containerapps` (13.A.2), `azure_dbadmin` (13.A.3), `azure_servicebus` queue (13.A.4), `azure_servicebus_topics` pubsub (13.A.5), `gcp_secretmanager` (13.B.1). 13.A.4/5 use a hand-written-dispatch hybrid because Go 1.22's ServeMux refuses the Service Bus admin spec's overlapping patterns. **Spec-drift contract via blank import** (build-time gate; full handler migration follow-on): `azure_blob` (13.A.6), `azure_apim` (13.A.7), `gcp_apigateway` / `gcp_memorystore` / `gcp_cloudrun` / `gcp_pubsub` × 2 / `gcp_cloudsql` / `gcs` (13.B.2-8). 13.C production RS256 JWKS landed in `internal/{gcpbearer,azurebearer}/jwks.go` — both verifiers now branch on JWT header `alg`: HS256 (test mode, existing) vs RS256 (JWKS lookup → RSA pubkey reconstruction → `crypto/rsa.VerifyPKCS1v15`). Config via `Options.JWKSURL` (URL-fetched + cached + kid-rotation re-fetch) or `Options.JWKS` (in-process). Unit tests cover both paths + signature tampering + claims rejection. Remaining for next Phase 13 PR: 13.D (Track A — needs real cloud accounts; closes BUG-8 + reclassifies BUG-15). |
| Last merged | PR #19 — Phase 12 at `778e8e9` on `main`, 2026-05-22. |
| Phases 1–12 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | 20 filed · 18 fixed · 2 open · 1 false positive. Both open bugs (BUG-8, BUG-15) absorbed into Phase 13.D (real-cloud Track A). |
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

# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `15c-azure-cosmos-tables-backend-frontend` — 15.C NoSQL Azure Cosmos DB Table API backend + frontend + SDK conformance. |
| In-flight | **15.C Azure Cosmos Tables — backend + frontend + conformance.** Backend at `services/nosql/backends/azure/azure.go` uses `aztables` SDK against real Cosmos Tables; metadata in reserved `shimtables` Cosmos table per N18. Frontend at `internal/nosql/frontends/azure_cosmos_tables/` translates OData v3 Tables protocol → `domain.NoSQL`. `internal/azuresharedkey/` extended to accept the `SharedKeyLite` scheme (Tables variant with `x-ms-date + canonicalizedResource` string-to-sign and only `?comp=` query-signed) in addition to the existing `SharedKey` for Blob/Queue/File. Cosmos PartitionKey/RowKey are strings; non-string domain.Values encode with typed prefixes (`s:` / `n:` / `b:` / `x:` / `_:`) so cross-cloud Apply preserves type fidelity. SDK conformance via `aztables` (TableLifecycle / EntityCRUD / QueryByPartition) green. CLI + Terraform deferred to cross-cloud Apply PR (azurerm uses ARM not direct Tables protocol). |
| Last merged | PR #92 — 15.C GCP Firestore Native backend + frontend + SDK + raw-HTTP conformance. |
| Upstream watch | sockerless #288 merged (AWS API Gateway client-surface coverage). Zero open sockerless issues for shimanism's current work. |
| Phases 1–13 | All closed (Phase 13 closed by PR #20). PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 14 | 14.A ✅ · 14.B ✅ · 14.C ✅ · 14.D ✅ (simulator audit) / Track A blocked · 14.E ✅. Remaining residuals (terraform-aws `_wo` drift, SB cross-cloud scoping) carried into Phase 15.B. |
| Phase 15 | 15.A ✅ · 15.B ✅ · 15.D ✅ (closed PR #89). 15.C in flight (this branch — foundational landing). |
| Bugs | **48 filed · 45 fixed · 3 open · 1 false positive.** Open: **BUG-8** (apigateway TF-provider Track A leg, real GCP), **BUG-15** (queue TF state-drift Track A leg, real GCP), **BUG-41** (hashicorp/google DNS endpoint regex; documented workaround in place — third-party project, can't file upstream). BUG-48 closed by PR #89. |
| Upstream | sockerless #257 / #260 / #261 / #269 / #272 / #276 all closed (via #259 / #262 / #271 / #274 / #277). Zero open sockerless issues for the shimanism roadmap. |
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

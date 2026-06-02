# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `15c-cosmos-tables-az-cli-conformance` — 15.C az CLI conformance for `az cosmosdb table`. |
| In-flight | **PR pending — `az cosmosdb table` CLI conformance (BUG-50 final leg).** `TestAzureCLI_CosmosTable_Lifecycle_ThroughShim` in `services/nosql/conformance/azure_cli_test.go`. Mirrors DNS BUG-43 pattern: `az cloud register` → `az login --service-principal` against sockerless Entra → create RG + account + table → show + list → delete. Linux-only (SSL_CERT_FILE). |
| Last merged | PR #98 — BUG-50 Cosmos Tables metadata + bearer verifier + Terraform conformance. TF conformance row complete. DynamoDB DeleteItem ConditionExpression fix. Global ARM path routing fix. |
| Upstream watch | [sockerless PR #357](https://github.com/e6qu/sockerless/pull/357) — Cosmos + Storage Tables ARM (merged). [PR #358](https://github.com/e6qu/sockerless/pull/358) — Linux netns + NAT/IPAM (merged, no Phase 15 dep). [#360](https://github.com/e6qu/sockerless/issues/360) filed + closed via [PR #361](https://github.com/e6qu/sockerless/pull/361) — DynamoDB `DeleteItem ReturnValues=ALL_OLD`. |
| Phases 1–13 | All closed (Phase 13 closed by PR #20). PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 14 | 14.A ✅ · 14.B ✅ · 14.C ✅ · 14.D ✅ (simulator audit) / Track A blocked · 14.E ✅. Remaining residuals (terraform-aws `_wo` drift, SB cross-cloud scoping) carried into Phase 15.B. |
| Phase 15 | 15.A ✅ · 15.B ✅ · 15.D ✅ (closed PR #89). 15.C in flight (this branch — foundational landing). |
| Bugs | **51 filed · 46 fixed · 5 open · 1 false positive.** Open: **BUG-8** + **BUG-15** + **BUG-41** (third-party / Track A); **BUG-50** (Cosmos Tables — foundational + metadata + TF landed; CLI conformance in flight). |
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

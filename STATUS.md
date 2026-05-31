# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `main` after PR #87 merged (15.D cross-cloud Apply cells). CoreDNS K8s peer PR in flight. |
| In-flight | **15.D CoreDNS K8s peer — file-based.** `services/dns/backends/coredns/coredns.go` implements `domain.DNS` by mutating RFC 1035 master files (one `.db` per zone) in a configured directory. In a K8s deployment the directory is a ConfigMap or PVC mounted into the CoreDNS pod; CoreDNS's `auto` plugin reloads via inotify. Uses `github.com/miekg/dns` (the same parser CoreDNS itself uses) for read/write round-trips. Per-zone in-memory mutex serialises writes; no shim-side cache. Unit tests cover zone + record CRUD, force-delete, TXT round-trip, and master-file parse-back validation. Live conformance against a real `coredns` binary deferred (requires CoreDNS in CI's PATH). Closes the 4-backend slot per AGENTS.md's K8s-as-fourth-backend mandate. Track A blocked on real-cloud credentials. |
| Last merged | PR #87 — 15.D cross-cloud Apply cells. |
| Upstream watch | sockerless #288 merged (AWS API Gateway client-surface coverage). Zero open sockerless issues for shimanism's current work. |
| Phases 1–13 | All closed (Phase 13 closed by PR #20). PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Phase 14 | 14.A ✅ · 14.B ✅ · 14.C ✅ · 14.D ✅ (simulator audit) / Track A blocked · 14.E ✅. Remaining residuals (terraform-aws `_wo` drift, SB cross-cloud scoping) carried into Phase 15.B. |
| Phase 15 | Just opened. Sub-phases: 15.A normalisations contract doc · 15.B 14.E cleanups · 15.C NoSQL key-value · 15.D DNS public+private. 15.A ships first. See [PLAN.md § Phase 15](PLAN.md#phase-15--cross-cloud-normalization-standard--new-service-expansion). |
| Bugs | **38 filed · 36 fixed · 2 open · 1 false positive.** Open: **BUG-8** (apigateway TF-provider Track A leg, real GCP), **BUG-15** (queue TF state-drift Track A leg, real GCP). BUG-35 closed by sockerless PR #245. |
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

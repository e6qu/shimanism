# shimanism — Status

Roadmap [PLAN.md](PLAN.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **A fresh session or post-compaction agent should be productive after reading this file + DO_NEXT.md.**

## Snapshot

| | |
|---|---|
| Active branch | `phase-12` — PR #19 at exit. **All three Phase 12 exit criteria met.** 82+ granular commits; ready for user review + merge. |
| In-flight | **Phase 12 at exit on PR #19.** Spec-driven toolchain for both clouds is fully built + validated. (1) `TestCrossCloudApply_Roundtrip_<svc>_<cell>` exists + passes for all 8 services. (2) 8/8 Azure specs + 8/8 GCP services have gen files compiling + imported by per-service conformance smoke tests; `azure_keyvault` ships fully migrated through `gen.HandlerWithOptions` as the reference impl; the other 7 + all 8 GCP frontends keep hand-written dispatch with the gen inventory acting as the spec-drift contract. (3) Every `MIGRATION.md` carries a copy-pasteable Terraform + endpoint-override walkthrough (HCL pulled from each service's `cross_cloud_import_test.go` fixture). **Phase 13 stub** in PLAN.md frames the follow-on: full adapter migration, production RS256 JWKS, real-cloud Track A. |
| Toolchain | **8-stage Azure preprocessor** (inline external refs / examples-skip / x-ms-enum with parameter+header gating / parameter+definition name dedup / ARM allOf flatten BUG-20 fix / x-ms-paths flatten / empty-AllOf normalize / deterministic walk) — every stage has direct unit tests in `cmd/azure-codegen/main_test.go`. **GCP routing emitter** ships `Routes` + `BasePath` + compiled `Pattern` + `Match` + `MatchAll`. **Provenance lane**: `_provenance` top-level key in every vendored spec; `cmd/inject-provenance` + `make inject-provenance` + CI guards (`TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance`); three fetch scripts (`scripts/fetch-{aws,azure,gcp}-*.sh`) all run the injector after download. **Spec freshness**: `make spec-freshness` + weekly workflow surface drift. Renovate custom manager tracks vendored-spec SHAs. |
| Last merged | PR #18 — Phase 11 (BUG-18 closed; 8/8 AWS spec-driven; Azure oapi-codegen pilot). `bcd72e5`, 2026-05-22. |
| Phases 1-11 | All closed. PR index in [PLAN.md § Closed phases](PLAN.md#closed-phases-pr-index). |
| Bugs | 20 filed · 18 fixed · 2 open · 1 false positive. Open: BUG-8 (apigateway/gcp-tf — Track A only), BUG-15 (queue/gcp retention plan/apply asymmetry — partial fix, Track-A walk pending). BUG-20 closed in 12.A.24 (ARM allOf-flatten unblocks Container Apps / Redis / PostgreSQL adapter migration). |
| CI | 16 required checks. Real-cloud lanes wait on Track A. |
| Renovate | Config committed (48h min release age, weekly batches, pinned action SHAs). **User must install the Renovate GitHub App.** |
| Standing merge auth | **None.** User merges every PR. |
| Live infra | None. |

## Invariants (carry across compactions / fresh sessions)

### Process
- **Never auto-merge PRs.** Push, wait for CI green, ping user. User merges.
- **Single-branch rule.** All work for one phase / sub-phase on one branch; many commits, one PR.
- **File BUGs *before* fixing.** Survey first, write `BUGS.md § Open` entry, then start the fix commit.
- **Update continuity docs every significant chunk** (not just at phase end): STATUS.md + WHAT_WE_DID.md + DO_NEXT.md.
- **Branch hygiene.** Rebase phase branch on `origin/main` before pushing; sync local `main` after merge.
- **No bug IDs in code comments.** Bug lineage lives in BUGS.md, commits, and PRs.
- **One plan file.** PLAN.md is the only roadmap doc. Per-phase planning lives inline as a section in PLAN.md; no `PHASE_X_PLAN.md` files.

### Architecture (load-bearing across all services)
- **The shim speaks the cloud's published API exactly.** Error shapes, response headers, status codes, async semantics — match. Server stubs are generated from the upstream spec; hand-written code is translation logic only.
- **Real backends, never emulators.** A shimmed call drives a real comparable service. The shim holds no state of record.
- **Stateless shim.** No sidecar storage, no shim-managed key/value namespace, no in-process cache treated as authoritative. State lives in the backend; cross-cloud mappings are derived at request time. See [AGENTS.md § The shim is stateless](AGENTS.md#the-shim-is-stateless).
- **Intersection-only scope.** Out-of-intersection feature calls fail loud with the source cloud's own error vocabulary. **Never fabricate success.**
- **Kubernetes is a first-class fourth backend** for every shimmed service.
- **No fakes, no fallbacks, no degraded modes.** Translation can't be honest → call fails loud.
- **Test from the official client surfaces.** SDK + CLI + Terraform provider in the same commit, against every backend in scope.

### Locked-in decisions
Full table in [PLAN.md § Locked-in decisions](PLAN.md#locked-in-decisions). Highlights: Go; specs pulled upstream (never forked); codegen owns wire stubs (`translate.go` is the only hand-written code); AGPL-3.0; reuse-over-reinvention; stateless shim; `shima<service>` naming for in-tree K8s peers built on `peers/shimakit/`.

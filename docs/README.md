# shimanism documentation

The repository [README.md](../README.md) is intentionally light — what the project is, the service catalog, examples, the philosophy in two paragraphs. Everything else lives here.

## For users

- **[Getting Started](getting-started.md)** — install the shim, point an SDK at it, watch a real AWS-shape CLI command land on a non-AWS backend. Five-minute walkthrough.
- **[End-to-end examples](end-to-end-examples.md)** — start from real backends or optional local simulator testing, then drive shimanism through CLI, SDK, and Terraform provider endpoint overrides.
- **[Standalone sockerless examples](end-to-end-examples.md#optional-local-simulator-testing-with-sockerless)** — run AWS -> GCP, GCP -> Azure, and Azure -> AWS storage examples locally without using the Go test harness.
- **[Architecture](architecture.md)** — what a shim is, how the front / domain / back layers compose, why the shim is stateless, and how cross-cloud translation works without emulation.
- **[Migration story](migration.md)** — rerouting cloud services one at a time, end-to-end walkthrough.
- **[Comparison with other projects](comparison.md)** — LocalStack, MinIO, Crossplane, Dapr, gocloud.dev, Pulumi/Terraform, OpenStack/Ceph S3 compat — where shimanism fits relative to each.
- **[Service catalog](services.md)** — each shimmed service: its per-cloud frontends, its backends, and pointers to the per-service `OPERATIONS.md` + `INTERSECTION.md` + `APPLY_INTERSECTION.md`. Detailed per-service docs live under [`docs/services/`](services/).
- **[Cross-cloud routing](../docs/cross-cloud-routing.md)** — the wire-level walkthrough of how user A's AWS Terraform points at the shim and the bytes land on cloud B.
- **[FAQ](faq.md)** — common questions: "Is this an emulator? Does it lie? What happens when a feature has no peer?"

## For contributors

- **[Contributing](contributing.md)** — branch flow, PR shape, the continuity-file contract (the six load-bearing markdown files at the repo root), commit-message conventions, and how to ask for help.
- **[Development setup](development.md)** — required tools (Go, terraform, docker, optional kind for K8s peers), `make` targets, how to add a new shimmed operation end-to-end (recipe), how to add a new service (longer recipe).
- **[Testing](testing.md)** — the conformance contract (SDK + CLI + Terraform per frontend per backend), how to run the test matrix locally, how to add a new conformance test, how the CI lanes are organized, the bug-first rule.
- **[Codegen](codegen.md)** — spec-driven server generation: where the upstream specs live, how to regenerate, why hand-written code is restricted to `translate.go`.
- **[Code health audits](code-health.md)** — dead-code and duplicate-code audit tools, current baseline, and cleanup policy.
- **[Releasing](releasing.md)** — the release flow, semver semantics, the no-auto-merge rule, who can cut a release.

## For agents (LLM-driven contributors)

- **[Agent guidelines](../AGENTS.md)** — the rules every coding agent must follow. `CLAUDE.md` is a symlink to this file.
- **[Philosophy](../PHILOSOPHY.md)** — the *why* every agent should read before changing code.

## Operational reference

- **[Plan](../PLAN.md)** — phase roadmap, exit criteria, closed-phase index.
- **[Status](../STATUS.md)** — current state snapshot, invariants, active phase.
- **[Do next](../DO_NEXT.md)** — the resume-from-cold file for the next session.
- **[What we did](../WHAT_WE_DID.md)** — reverse-chronological narrative (why + surprises + root causes).
- **[Bugs](../BUGS.md)** — every bug filed, fixed, or reclassified.
- **[Dependency policy](../docs/dependency-policy.md)** — minimum release age, pinning, pure-Go preference, npm/pnpm posture.
- **[License compatibility](../docs/compatible-licenses.md)** — the allowlist for linked dependencies.

## Reading order

If you have ten minutes and want to know whether shimanism is for you, read [README.md](../README.md) and [Getting Started](getting-started.md).

If you have an hour and want to *understand* the project, read [PHILOSOPHY.md](../PHILOSOPHY.md), [Architecture](architecture.md), and one service's `OPERATIONS.md` (start with [`services/storage/OPERATIONS.md`](../services/storage/OPERATIONS.md)).

If you're about to contribute code, read [Contributing](contributing.md) and [AGENTS.md](../AGENTS.md). They overlap intentionally — the rules apply to humans and LLMs alike.

# Dependency Policy

Rules that govern *which* dependencies we accept and *how* we adopt them, beyond the AGPL-compatibility allowlist in [`compatible-licenses.md`](compatible-licenses.md).

> Read this alongside [`compatible-licenses.md`](compatible-licenses.md). The license doc decides whether a dep is *legally* compatible. This doc decides whether it is *safe and operationally appropriate*.

## Supply-chain hardening

### Minimum release age: 48 hours

A dependency version must be **at least 48 hours old** before we adopt it. Renovate is configured with `minimumReleaseAge: "48 hours"` (see [`.github/renovate.json5`](../.github/renovate.json5)) so automated bumps respect this; manual additions should too.

Why: real-world supply-chain attacks (`event-stream`, `ua-parser-js`, `colors` / `faker`, `coa`, `node-ipc`, several Go modules in 2024–25) were caught and yanked from the registry within 48 hours of publish. The 48-hour wait gives the ecosystem time to spot and respond to a malicious release before we land it. The cost is one batched-PR cycle of latency; the benefit is order-of-magnitude.

Exceptions: a published **security advisory** for a vulnerability we actually use is a justification to override the 48-hour wait. The exception is logged in [BUGS.md](../BUGS.md) with the CVE / advisory reference.

### Pin GitHub Actions to immutable SHAs

`pinDigests: true` in Renovate config pins every `uses: actions/foo@v6` to `uses: actions/foo@<sha> # v6.0.2`. The tag is a movable reference; the SHA is immutable. A compromised maintainer can re-tag; they can't re-cement a SHA.

When manually adding an Action, look up the SHA at the release page and pin it. Renovate keeps it updated.

## Language-specific rules

### Go

- **Prefer pure-Go dependencies.** A pure-Go dep cross-compiles to every target trivially, ships smaller binaries, and avoids the dependency on a C toolchain at build time and a system libc / library set at run time. *When evaluating two equivalent libraries, the pure-Go one wins.*
- **`cgo` is allowed only when there is no pure-Go alternative and the cgo dependency is justified.** Examples where it's reasonable: SQLite via `mattn/go-sqlite3` if we ever need an embedded SQL store (alternative: `modernc.org/sqlite` which is pure-Go via translation); cryptography requiring boringcrypto for FIPS, etc. The justification is recorded in the PR adding the dep.
- **Generated code from upstream specs is preferred over hand-translation libraries.** If AWS publishes a Smithy model and we have a codegen pipeline, the generated code is more faithful than a third-party SDK wrapper.

### npm / Node (when we eventually need a JavaScript SDK conformance lane)

We do not yet have any JavaScript / TypeScript code; these rules apply when we add it (Phase 5+ likely).

- **Package manager: `pnpm` only.** Not npm, not yarn. pnpm's content-addressable store is meaningfully harder to poison than npm's per-project `node_modules`, and pnpm's strict-isolation model surfaces transitive-dependency creep that npm hides.
- **`pnpm install --ignore-scripts` is the default.** Pre-install / post-install / install lifecycle scripts are a well-documented attack surface (a malicious release ships code in a script that runs before any review is possible). We disable them globally.
- **Dependencies that require lifecycle scripts to install** must be triaged on a case-by-case basis:
  - If we can patch them to not require the script (often a build-step quirk we can replace), do that.
  - If the script is genuinely necessary (native bindings, etc.), evaluate whether a different dep gets us the same capability without the script.
  - If neither works, the dep is rejected and we document the rationale in a BUG.
- **`pnpm audit`** runs in CI alongside the license check (added when JavaScript code lands).

### Python (when we eventually need the boto3 conformance lane)

We do not yet have Python code; expected to land in Phase 1.4 (conformance harness). Rules will mirror the npm/Node rules adapted for Python's `pip` / `uv`:

- Lock files committed.
- `--no-build-isolation` discouraged; we'd rather use prebuilt wheels.
- Build-step scripts in `setup.py` reviewed.

Will be expanded when the first Python dep lands.

### Container images (when we publish them)

- Pin all base images to a digest (`FROM alpine@sha256:…`), not a tag.
- Use Renovate's docker manager to keep the digest fresh.
- Build with [SLSA Level 3](https://slsa.dev/spec/v1.0/levels) provenance when we publish.

## What to do when a dep doesn't fit the policy

1. **File a BUG in [BUGS.md](../BUGS.md) describing the gap** — what capability does the dep provide that we need, why does the existing-dep landscape not cover it under our rules.
2. **Look for an alternative dep that does fit.** Often the constraint pushes us to a healthier choice.
3. **If no alternative exists, propose an exception in the PR.** Document the justification, the additional risk mitigation, and the review trail. The user approves on merge.

The policy exists to apply consistent friction to dependency growth, not to block work. Exceptions are first-class — they just need to be visible.

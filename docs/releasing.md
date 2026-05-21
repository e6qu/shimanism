# Releasing

shimanism follows the same posture as everything else in the repo: the user merges every PR; CI is the gate; no auto-merge.

## Release cadence

There is no fixed release cadence yet. Phases close when their exit criteria are met (see [PLAN.md](../PLAN.md)); a release is cut after a phase closes when it adds user-visible value worth tagging.

## Versioning

Semver for the binary:

- **Major:** breaking changes to the CLI flags, the `shimctl env` output format, or the on-the-wire behavior in a way that would break clients pointing at the shim.
- **Minor:** new shimmed services or new operations added to existing services.
- **Patch:** bug fixes, fidelity improvements, internal refactors.

The Go module is `github.com/e6qu/shimanism`. Library consumers (mostly: anyone embedding the shim in their own binary) treat `internal/` as private; the public surface is `cmd/shim` and `cmd/shimctl`.

## Release flow

1. **Ensure CI is green on `main`.** Check the latest workflow run; all required checks must pass.
2. **Update [STATUS.md](../STATUS.md)** to reflect the release: bump the phase status, note the version, link to the relevant PRs.
3. **Update [WHAT_WE_DID.md](../WHAT_WE_DID.md)** if a phase closes with this release. Reverse-chronological narrative entry covering what shipped and why.
4. **Tag.** Annotated tag, semver format:
   ```sh
   git tag -a v0.x.y -m "Phase X: <one-line summary>"
   git push origin v0.x.y
   ```
5. **Build release artifacts.** Cross-compile for `linux/amd64` + `darwin/arm64`:
   ```sh
   GOOS=linux GOARCH=amd64 go build -o build/shim-linux-amd64 ./cmd/shim
   GOOS=darwin GOARCH=arm64 go build -o build/shim-darwin-arm64 ./cmd/shim
   GOOS=linux GOARCH=amd64 go build -o build/shimctl-linux-amd64 ./cmd/shimctl
   GOOS=darwin GOARCH=arm64 go build -o build/shimctl-darwin-arm64 ./cmd/shimctl
   ```
6. **Create the GitHub release.** Use the tag from step 4. Body: link to the phase exit criterion + the WHAT_WE_DID.md narrative. Attach the binaries from step 5.

## What can be in a release

Per [PHILOSOPHY.md](../PHILOSOPHY.md) and [AGENTS.md](../AGENTS.md):

- Anything that does real work against the cross-cloud intersection.
- Per-service code that emits the source cloud's honest error envelopes for out-of-intersection features.
- Documentation updates.

## What cannot be in a release

- **No fakes.** Stub backends, in-memory state where a real backend was specified, mock responses, hardcoded values — none of it. If real implementation isn't feasible today, the operation isn't in the release; it's filed as a BUG and surfaced.
- **No silent fallbacks.** If a feature can't be honored, the shim must return the source cloud's "not supported" error envelope. Never a fabricated success.
- **No skipped CI lanes.** If CI fails on the release branch, the release waits until CI is green. Skipping a flaky test is a BUG, not a release strategy.
- **No Renovate auto-merges.** Renovate opens PRs; humans review + merge.

## Pre-release checks

Before tagging:

1. `make test` — all green locally.
2. `make lint` — 0 issues.
3. `make license-check` — every linked dependency is on the allowlist in [`doc/COMPATIBLE_LICENSES.md`](../doc/COMPATIBLE_LICENSES.md).
4. `make conformance-matrix` — the canonical cross-backend matrix.
5. **Skim [BUGS.md](../BUGS.md) § Open.** Any P0 or P1 bug should be either fixed for this release or explicitly deferred with a note in WHAT_WE_DID.md.

## Hotfix flow

For a P0 fidelity defect that needs to ship before the next planned phase release:

1. Branch from the tagged release.
2. Cherry-pick the fix (must come with its own BUG entry, file/fix per the [bug-first rule](../AGENTS.md#the-bug-first-rule)).
3. Run the full conformance lane.
4. Open a PR for review.
5. After merge, tag the patch version off the release branch.

## Cross-link

- [PLAN.md](../PLAN.md) — phase definitions and exit criteria.
- [STATUS.md](../STATUS.md) — current state.
- [WHAT_WE_DID.md](../WHAT_WE_DID.md) — narrative.
- [BUGS.md](../BUGS.md) — open + resolved bug ledger.
- [docs/contributing.md](contributing.md) — no-auto-merge rule.

#!/usr/bin/env bash
# Checks that the current branch is rebased on top of origin/main with a
# linear history (no merge commits since origin/main).
#
# Adapted from e6qu/sockerless. Runs in CI (GitHub Actions) and is also
# safe to wire into a local pre-push hook.
#
# Exits 0 on pass; non-zero with a human-readable error on fail.

set -euo pipefail

# Pre-commit framework exposes the target remote via $PRE_COMMIT_REMOTE_NAME.
# Mirror remotes (any remote whose name is not exactly `origin`) are expected
# to track origin/main verbatim — fast-forward pushes of main to them are
# intentional and exempt from these checks.
remote_name="${PRE_COMMIT_REMOTE_NAME:-origin}"
if [ "$remote_name" != "origin" ]; then
  exit 0
fi

# In GitHub Actions on a pull_request event, HEAD is detached at the PR merge
# commit. Use $GITHUB_HEAD_REF when present; otherwise the current symbolic
# ref. Both branches should already be fetched by `actions/checkout` with
# `fetch-depth: 0`.
branch="${GITHUB_HEAD_REF:-$(git rev-parse --abbrev-ref HEAD)}"

# Never push directly to main on origin.
if [ "$branch" = "main" ]; then
  echo "ERROR: Do not push directly to main on origin. Create a branch first."
  exit 1
fi

# Make sure origin/main is fresh.
git fetch origin main --quiet 2>/dev/null || true

# When running locally, also verify the local 'main' is in sync with
# origin/main — drift between them is a frequent source of confusing rebases.
local_main=$(git rev-parse main 2>/dev/null || echo "")
origin_main=$(git rev-parse origin/main 2>/dev/null || echo "")

if [ -n "$local_main" ] && [ -n "$origin_main" ] && [ "$local_main" != "$origin_main" ]; then
  # In CI there is no local 'main' checkout, so this only fires locally.
  if [ -z "${CI:-}" ]; then
    echo "ERROR: Local main ($local_main) differs from origin/main ($origin_main)."
    echo "Sync first: git checkout main && git pull origin main"
    exit 1
  fi
fi

# In CI on pull_request, $GITHUB_SHA is a merge-commit ref between the PR
# branch and main. We want to compare the PR HEAD against origin/main, not
# that merge commit. github.event.pull_request.head.sha would be ideal, but
# we approximate by using $GITHUB_HEAD_REF (the PR branch).
head_ref="HEAD"
if [ -n "${GITHUB_HEAD_REF:-}" ]; then
  head_ref="origin/${GITHUB_HEAD_REF}"
  # The checkout action with fetch-depth: 0 fetches all refs, so origin/<head>
  # is available as a fully-resolved branch ref.
  git fetch origin "${GITHUB_HEAD_REF}" --quiet 2>/dev/null || true
fi

# Check branch is not behind origin/main.
behind=$(git rev-list --count "${head_ref}..origin/main" 2>/dev/null || echo "0")

if [ "$behind" -gt 0 ]; then
  echo "ERROR: Branch '$branch' is $behind commit(s) behind origin/main."
  echo "Rebase before pushing: git fetch origin main && git rebase origin/main"
  exit 1
fi

# Check linear history (no merge commits since origin/main).
merges=$(git rev-list --merges "origin/main..${head_ref}" 2>/dev/null | wc -l | tr -d ' ')

if [ "$merges" -gt 0 ]; then
  echo "ERROR: Branch '$branch' has $merges merge commit(s). History must be linear."
  echo "Rebase instead of merging: git rebase origin/main"
  exit 1
fi

echo "OK: '$branch' is rebased on origin/main with linear history."

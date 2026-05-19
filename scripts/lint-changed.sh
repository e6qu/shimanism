#!/usr/bin/env bash
# Run golangci-lint on the packages that contain the changed Go files.
# Wired in as a pre-commit hook so every commit gets linted without
# paying the full repo-wide lint cost.

set -euo pipefail

if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint not found. Install via:"
  echo "  curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b \$(go env GOPATH)/bin v2.10.1"
  exit 1
fi

# Pre-commit passes the changed files via $@. If none, lint the full tree.
if [ $# -eq 0 ]; then
  exec golangci-lint run ./...
fi

# Walk up from <dir> until we find a directory with a go.mod. Returns
# the module root or an empty string when none is found.
module_root_for() {
  local d="$1"
  while [ "$d" != "." ] && [ "$d" != "/" ]; do
    if [ -f "$d/go.mod" ]; then
      echo "$d"
      return
    fi
    d=$(dirname "$d")
  done
  if [ -f "./go.mod" ]; then
    echo "."
  fi
}

# Find the repo's root go.mod path so we can skip changes that
# belong to a nested module — those need a separate lint invocation
# from their own module root, not from here.
repo_root="$(pwd)"

# Map each changed file to its containing package (directory), dedup
# without bash 4's associative arrays so this runs on macOS' bash 3.
# Skip files that belong to a nested module (different go.mod) —
# we only lint the root module from this script.
pkgs=$(for f in "$@"; do
  [ -f "$f" ] || continue
  dir="$(dirname "$f")"
  mod="$(module_root_for "$dir")"
  if [ -n "$mod" ] && [ "$(cd "$mod" && pwd)" != "$repo_root" ]; then
    continue
  fi
  echo "./$dir"
done | sort -u)

if [ -z "$pkgs" ]; then
  exit 0
fi

# golangci-lint accepts package paths as positional args (one per line
# becomes a quoted list when expanded by xargs).
echo "$pkgs" | xargs golangci-lint run

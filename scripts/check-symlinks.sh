#!/usr/bin/env bash
# Verifies the repo's tracked symlinks resolve to existing files with the
# expected content. Currently: CLAUDE.md → AGENTS.md (by design — see
# AGENTS.md).
#
# Exits 0 on pass; non-zero with a human-readable error on fail.

set -euo pipefail

fail=0

check_symlink() {
  local link="$1"
  local expected_target="$2"

  if [ ! -L "$link" ]; then
    echo "ERROR: '$link' should be a symlink, but is a regular file or missing."
    echo "Recreate with: ln -s '$expected_target' '$link'"
    fail=1
    return
  fi

  local actual_target
  actual_target=$(readlink "$link")
  if [ "$actual_target" != "$expected_target" ]; then
    echo "ERROR: '$link' points to '$actual_target', expected '$expected_target'."
    fail=1
    return
  fi

  if [ ! -e "$link" ]; then
    echo "ERROR: '$link' points to '$expected_target', which does not exist."
    fail=1
    return
  fi

  echo "OK: $link -> $expected_target"
}

check_symlink "CLAUDE.md" "AGENTS.md"

exit "$fail"

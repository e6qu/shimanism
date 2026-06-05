#!/usr/bin/env bash
# Audit unreachable Go functions using the official x/tools deadcode command.
#
# This is an audit, not deletion authority. deadcode is call-graph based and
# can report functions that are valid public/library entry points for this repo.

set -euo pipefail

DEADCODE_VERSION=${DEADCODE_VERSION:-v0.45.0}
DEADCODE_PACKAGES=${DEADCODE_PACKAGES:-./...}
DEADCODE_EXCLUDE_RE=${DEADCODE_EXCLUDE_RE:-/gen/}

raw=$(mktemp)
filtered=$(mktemp)
cleanup() {
  rm -f "$raw" "$filtered"
}
trap cleanup EXIT

go run "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}" -test "$DEADCODE_PACKAGES" >"$raw"

if [[ -n $DEADCODE_EXCLUDE_RE ]]; then
  grep -Ev "$DEADCODE_EXCLUDE_RE" "$raw" >"$filtered" || true
else
  cp "$raw" "$filtered"
fi

cat "$filtered"

if [[ -s $filtered ]]; then
  echo "deadcode audit: unreachable functions reported"
  if [[ ${DEADCODE_AUDIT_STRICT:-0} == "1" ]]; then
    exit 1
  fi
else
  echo "deadcode audit: no unreachable functions reported after filters"
fi

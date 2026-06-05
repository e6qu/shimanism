#!/usr/bin/env bash
# Audit copy-pasted Go code using golangci-lint's dupl linter.
#
# This is a strict audit. The duplicate baseline is zero, and dupl is also
# enabled in the normal golangci-lint gate.

set -euo pipefail

if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint not found. Install via:" >&2
  echo "  curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b \$(go env GOPATH)/bin v2.10.1" >&2
  exit 2
fi

tmp=$(mktemp)
cleanup() {
  rm -f "$tmp"
}
trap cleanup EXIT

set +e
golangci-lint run --enable-only=dupl ./... >"$tmp" 2>&1
status=$?
set -e

cat "$tmp"

case "$status" in
  0)
    echo "duplication audit: no duplicate fragments reported"
    ;;
  1)
    echo "duplication audit: duplicate fragments reported"
    exit 1
    ;;
  *)
    exit "$status"
    ;;
esac

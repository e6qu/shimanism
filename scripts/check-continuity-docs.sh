#!/usr/bin/env bash
# Verifies that the load-bearing continuity files exist. CI guards
# against accidental deletion; this is the local mirror of that
# check, wired in via .pre-commit-config.yaml.

set -euo pipefail

for f in PLAN.md STATUS.md WHAT_WE_DID.md DO_NEXT.md BUGS.md AGENTS.md PHILOSOPHY.md README.md; do
  if [ ! -f "$f" ]; then
    echo "ERROR: required continuity doc missing: $f" >&2
    exit 1
  fi
done

echo "OK: all continuity docs present."

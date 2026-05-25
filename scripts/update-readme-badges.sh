#!/usr/bin/env bash
# Updates README.md badge values from current codebase stats.
# Adapted from sockerless's scripts/update-readme-badges.sh.
#
# Used as a pre-push hook (see .pre-commit-config.yaml) and runnable
# manually:  `bash scripts/update-readme-badges.sh`.
#
# Mirror remote pushes (any pre-commit PRE_COMMIT_REMOTE_NAME other
# than "origin") are intentional fast-forwards of origin/main; they
# carry whatever badges origin/main already has, so this hook is a
# no-op for them.

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

remote_name="${PRE_COMMIT_REMOTE_NAME:-origin}"
if [ "$remote_name" != "origin" ]; then
  exit 0
fi

readme="README.md"

# Portable sed -i (macOS vs Linux).
sedi() { if [[ "$OSTYPE" == darwin* ]]; then sed -i '' "$@"; else sed -i "$@"; fi; }

# Sum line counts of Go source files (non-test) under a directory, with
# vendored and node_modules paths stripped.
count_go_src() {
  find "$1" -name '*.go' -not -name '*_test.go' \
       -not -path '*/vendor/*' -not -path '*/node_modules/*' -print0 2>/dev/null \
    | xargs -0 wc -l 2>/dev/null | tail -1 | awk '{print $1+0}'
}

count_go_test() {
  find "$1" -name '*_test.go' \
       -not -path '*/vendor/*' -not -path '*/node_modules/*' -print0 2>/dev/null \
    | xargs -0 wc -l 2>/dev/null | tail -1 | awk '{print $1+0}'
}

# Generated Go (cmd/codegen, cmd/azure-codegen, cmd/gcp-codegen output).
count_go_gen() {
  find "$1" -name '*.gen.go' \
       -not -path '*/vendor/*' -not -path '*/node_modules/*' -print0 2>/dev/null \
    | xargs -0 wc -l 2>/dev/null | tail -1 | awk '{print $1+0}'
}

# Format a line count as "12.3k" / "456" for badge text.
fmt_k() {
  local n=${1:-0}
  if [ "$n" -ge 1000 ]; then
    local k=$((n / 1000))
    local r=$(( (n % 1000) / 100 ))
    if [ "$r" -gt 0 ]; then echo "${k}.${r}k"; else echo "${k}k"; fi
  else
    echo "$n"
  fi
}

# ---- Top-level numbers ----
go_total=$(count_go_src .)
go_test=$(count_go_test .)
go_gen=$(count_go_gen .)
go_src_handwritten=$((go_total - go_gen))
go_modules=$(find . -name 'go.mod' -not -path './.git/*' -not -path '*/vendor/*' -not -path '*/node_modules/*' | wc -l | tr -d ' ')

# Top-level badge substitutions. The label "Go" tracks hand-written
# Go (everything in *.go minus *.gen.go and *_test.go) — this is the
# count that reflects what humans wrote. *.gen.go is its own badge.
sedi "s|Go-[0-9.]*k*_lines|Go-$(fmt_k "$go_src_handwritten")_lines|g" "$readme"
sedi "s|Generated-[0-9.]*k*_lines|Generated-$(fmt_k "$go_gen")_lines|g" "$readme"
sedi "s|Tests-[0-9.]*k*_lines|Tests-$(fmt_k "$go_test")_lines|g" "$readme"
sedi "s|Go_Modules-[0-9]*-|Go_Modules-${go_modules}-|g" "$readme"

# ---- Per-major-component Go badges ----
# (slug:directory). The slug is what appears between "badge/<slug>-"
# and the numeric value in the shields.io URL.
for pair in \
  "storage:services/storage" \
  "secrets:services/secrets" \
  "queue:services/queue" \
  "pubsub:services/pubsub" \
  "rdbms:services/rdbms" \
  "cache:services/cache" \
  "functions:services/functions" \
  "apigateway:services/apigateway" \
  "internal:internal" \
  "cmd:cmd" \
  "peers:peers" \
; do
  slug="${pair%%:*}"
  dir="${pair#*:}"
  if [ -d "$dir" ]; then
    val=$(fmt_k "$(count_go_src "$dir")")
    sedi "s|badge/${slug}-[0-9.k]*-|badge/${slug}-${val}-|g" "$readme"
  fi
done

if ! git diff --quiet "$readme" 2>/dev/null; then
  echo "badges: updated"
  git add "$readme"
fi

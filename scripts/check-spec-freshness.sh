#!/usr/bin/env bash
# Reports drift between vendored specs and their upstream HEADs.
#
# For each services/*/spec/SOURCES.md row, parses the "Upstream repo",
# "Upstream path" and "Pinned at" columns, then asks GitHub for the
# most-recent commit that touches that path on the upstream default
# branch. A mismatch is printed as a one-line warning. Discovery
# revisions (e.g. `revision 20260424`) skip the comparison — those
# refresh as part of fetch.
#
# Output is informational; exit code is 0 unless the script itself
# fails (missing `gh`, malformed table). CI can wrap this with a
# threshold check (e.g. ">14 days behind upstream"); see PLAN.md
# for the spec-freshness CI lane.
#
# Usage:
#   scripts/check-spec-freshness.sh
#
# Requires: gh (authenticated), jq.

set -euo pipefail

if ! command -v gh >/dev/null 2>&1; then
  echo "check-spec-freshness: gh CLI is required (brew install gh)" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "check-spec-freshness: jq is required" >&2
  exit 2
fi

drift_count=0

for sources_md in services/*/spec/SOURCES.md; do
  # Each table row: | `local-file` | `upstream-repo` | `upstream-path` | License | `pinned-sha-or-revision` | timestamp |
  # Skip the header + separator + empty/non-row lines.
  while IFS= read -r line; do
    # Match table rows whose first column is a backticked local file
    # (i.e. starts with `| \``). Header/separator rows don't.
    if [[ ! "$line" =~ ^\|[[:space:]]*\` ]]; then
      continue
    fi
    # Extract backticked values: positions 1, 2, 3, 5 of the row.
    # shellcheck disable=SC2016 # backticks are literal table-cell delimiters in this regex, not command substitution.
    backticked=$(grep -oE '`[^`]+`' <<<"$line" || true)
    local_file=$(sed -n '1p' <<<"$backticked" | tr -d '`')
    upstream_repo=$(sed -n '2p' <<<"$backticked" | tr -d '`')
    upstream_path=$(sed -n '3p' <<<"$backticked" | tr -d '`')
    pinned=$(sed -n '4p' <<<"$backticked" | tr -d '`')

    # Discovery rows have `revision 20260424` (no SHA) — skip.
    if [[ ! "$pinned" =~ ^[0-9a-f]{40}$ ]]; then
      echo "skip  $local_file (no commit SHA pinned; got '$pinned')"
      continue
    fi

    # Query upstream HEAD for the most recent commit touching that path.
    upstream_sha=$(gh api "repos/${upstream_repo}/commits?path=${upstream_path}&per_page=1" --jq '.[0].sha' 2>/dev/null || true)
    if [[ -z "$upstream_sha" || "$upstream_sha" == "null" ]]; then
      echo "warn  $local_file: could not resolve upstream HEAD for ${upstream_repo}:${upstream_path}"
      continue
    fi

    if [[ "$upstream_sha" == "$pinned" ]]; then
      echo "ok    $local_file"
    else
      drift_count=$((drift_count+1))
      upstream_short=${upstream_sha:0:12}
      pinned_short=${pinned:0:12}
      echo "DRIFT $local_file: pinned $pinned_short, upstream $upstream_short ($upstream_repo:$upstream_path)"
    fi
  done < "$sources_md"
done

echo
if [[ $drift_count -gt 0 ]]; then
  echo "$drift_count spec(s) drifted from upstream. Refresh with the relevant fetch script + 'make codegen' + commit."
else
  echo "All git-pinned specs are at upstream HEAD."
fi

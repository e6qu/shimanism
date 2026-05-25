#!/usr/bin/env bash
# Fetches an AWS service's Smithy JSON model from upstream
# aws/aws-sdk-go-v2 and vendors it under services/<svc>/spec/, with a
# sibling SOURCES.md capturing the upstream URL + resolved commit SHA so
# the local copy is reproducibly tied to a specific upstream snapshot.
#
# Usage:
#   scripts/fetch-aws-spec.sh <service> <local-service-dir> [<ref>]
#
# Example:
#   scripts/fetch-aws-spec.sh s3 services/storage         # tracks main
#   scripts/fetch-aws-spec.sh s3 services/storage v1.31.0 # pinned tag
#
# The script always writes:
#   <local-service-dir>/spec/aws-<service>.smithy.json
#   <local-service-dir>/spec/SOURCES.md
#
# Re-running the script overwrites both files; review the diff and commit.

set -euo pipefail

if [ $# -lt 2 ] || [ $# -gt 3 ]; then
  echo "usage: $0 <aws-service> <local-service-dir> [<ref>]" >&2
  echo "  example: $0 s3 services/storage" >&2
  exit 2
fi

aws_service="$1"
service_dir="$2"
ref="${3:-main}"

upstream_repo="aws/aws-sdk-go-v2"
upstream_path="codegen/sdk-codegen/aws-models/${aws_service}.json"

mkdir -p "${service_dir}/spec"

# Resolve <ref> to a concrete commit SHA so the SOURCES.md captures a
# pinned snapshot even when <ref> is a branch (e.g. "main").
resolved_sha=$(gh api "repos/${upstream_repo}/commits/${ref}" --jq .sha)
if [ -z "${resolved_sha}" ]; then
  echo "ERROR: could not resolve ${ref} to a commit SHA in ${upstream_repo}" >&2
  exit 1
fi

# Pull the raw file at the resolved SHA.
spec_url="https://raw.githubusercontent.com/${upstream_repo}/${resolved_sha}/${upstream_path}"
out_json="${service_dir}/spec/aws-${aws_service}.smithy.json"

echo "Fetching ${spec_url}"
curl -fsSL "${spec_url}" -o "${out_json}"

bytes=$(wc -c < "${out_json}" | tr -d ' ')
echo "Wrote ${out_json} (${bytes} bytes)"

# Update / write the SOURCES.md for this service-dir. One row per spec.
sources_md="${service_dir}/spec/SOURCES.md"
fetched_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build the new SOURCES.md from scratch — small and easier to keep
# canonical than line-edits. If the file already exists with rows for
# other services, this script overwrites them; that's fine while we are
# one-service-per-spec-dir. Revisit when a single service-dir vendors
# multiple specs.
cat > "${sources_md}" <<EOF
# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with \`scripts/fetch-aws-spec.sh\` (or \`make fetch-specs\`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| \`aws-${aws_service}.smithy.json\` | \`${upstream_repo}\` | \`${upstream_path}\` | Apache-2.0 | \`${resolved_sha}\` | ${fetched_at} |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
\`aws/aws-sdk-go-v2\` are Apache 2.0; the upstream \`LICENSE\` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [\`docs/compatible-licenses.md\`](../../../docs/compatible-licenses.md)
for the overall policy).
EOF

echo "Wrote ${sources_md}"

# Inject the `_provenance` top-level key into the freshly-fetched
# JSON so the spec self-documents its origin. SOURCES.md is the
# authoritative store; the spec file's `_provenance` is a derived
# projection that travels with the file when reviewers open it.
go run ./cmd/inject-provenance -sources="${sources_md}" -dir="${service_dir}/spec"

echo
echo "Pinned aws-${aws_service}.smithy.json to ${upstream_repo}@${resolved_sha}"

#!/usr/bin/env bash
# Fetches an Azure REST API spec from Azure/azure-rest-api-specs and
# vendors it under services/<svc>/spec/ with a sibling SOURCES.md row
# + an injected `_provenance` key at the top of the JSON.
#
# Usage:
#   scripts/fetch-azure-spec.sh <upstream-path> <local-service-dir> <local-filename> [<ref>]
#
# Examples:
#   scripts/fetch-azure-spec.sh \
#     specification/keyvault/data-plane/Secrets/stable/7.6/secrets.json \
#     services/secrets azure-keyvault-secrets.json
#
#   scripts/fetch-azure-spec.sh \
#     specification/storage/data-plane/Microsoft.BlobStorage/stable/2026-04-06/blob.json \
#     services/storage azure-blob.json
#
# The script resolves <ref> (default: main) to a concrete commit SHA
# so SOURCES.md captures a pinned snapshot.

set -euo pipefail

if [ $# -lt 3 ] || [ $# -gt 4 ]; then
  echo "usage: $0 <upstream-path> <local-service-dir> <local-filename> [<ref>]" >&2
  echo "  example: $0 specification/keyvault/data-plane/Secrets/stable/7.6/secrets.json services/secrets azure-keyvault-secrets.json" >&2
  exit 2
fi

upstream_path="$1"
service_dir="$2"
local_filename="$3"
ref="${4:-main}"

upstream_repo="Azure/azure-rest-api-specs"

mkdir -p "${service_dir}/spec"

resolved_sha=$(gh api "repos/${upstream_repo}/commits/${ref}" --jq .sha)
if [ -z "${resolved_sha}" ]; then
  echo "ERROR: could not resolve ${ref} to a commit SHA in ${upstream_repo}" >&2
  exit 1
fi

spec_url="https://raw.githubusercontent.com/${upstream_repo}/${resolved_sha}/${upstream_path}"
out_json="${service_dir}/spec/${local_filename}"

echo "Fetching ${spec_url}"
curl -fsSL "${spec_url}" -o "${out_json}"

bytes=$(wc -c < "${out_json}" | tr -d ' ')
echo "Wrote ${out_json} (${bytes} bytes)"

fetched_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
sources_md="${service_dir}/spec/SOURCES.md"

if [ ! -f "${sources_md}" ]; then
  cat > "${sources_md}" <<EOF
# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with the matching \`scripts/fetch-*.sh\` and commit the diff.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| \`${local_filename}\` | \`${upstream_repo}\` | \`${upstream_path}\` | MIT | \`${resolved_sha}\` | ${fetched_at} |

## License of vendored files

\`Azure/azure-rest-api-specs\` is MIT-licensed; vendored files retain
their upstream license. Generated code derived from these specs is
licensed AGPL-3.0 alongside the rest of shimanism (see
[\`docs/compatible-licenses.md\`](../../../docs/compatible-licenses.md)).
EOF
  echo "Wrote ${sources_md} (new)"
else
  echo "NOTE: ${sources_md} exists — review and update the ${local_filename} row by hand if the SHA changed."
fi

go run ./cmd/inject-provenance -sources="${sources_md}" -dir="${service_dir}/spec"

echo
echo "Pinned ${local_filename} to ${upstream_repo}@${resolved_sha}"

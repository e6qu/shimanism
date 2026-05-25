#!/usr/bin/env bash
# Fetches a GCP service's Discovery JSON document from the live
# endpoint and vendors it under services/<svc>/spec/, with a
# sibling SOURCES.md row + an injected `_provenance` key at the
# top of the JSON.
#
# Discovery documents are not git-pinned — Google publishes them
# from a live URL and each document embeds a `revision` field
# (e.g. "20260516"). The vendored copy captures the revision
# string in SOURCES.md and re-fetching produces a new revision
# the first time Google ships one.
#
# Usage:
#   scripts/fetch-gcp-discovery.sh <gcp-service-host> <local-service-dir> <local-filename>
#
# Examples:
#   scripts/fetch-gcp-discovery.sh secretmanager.googleapis.com \
#     services/secrets gcp-secretmanager-discovery.json
#   scripts/fetch-gcp-discovery.sh storage.googleapis.com \
#     services/storage gcp-storage-discovery.json
#
# The script always writes:
#   <local-service-dir>/spec/<local-filename>
#   <local-service-dir>/spec/SOURCES.md (or merges if present)

set -euo pipefail

if [ $# -ne 3 ]; then
  echo "usage: $0 <gcp-service-host> <local-service-dir> <local-filename>" >&2
  echo "  example: $0 secretmanager.googleapis.com services/secrets gcp-secretmanager-discovery.json" >&2
  exit 2
fi

service_host="$1"
service_dir="$2"
local_filename="$3"

discovery_url="https://${service_host}/\$discovery/rest?version=v1"
out_json="${service_dir}/spec/${local_filename}"

mkdir -p "${service_dir}/spec"

echo "Fetching ${discovery_url}"
curl -fsSL "${discovery_url}" -o "${out_json}"

revision=$(jq -r '.revision' "${out_json}")
if [ -z "${revision}" ] || [ "${revision}" = "null" ]; then
  echo "ERROR: fetched Discovery JSON has no .revision field" >&2
  exit 1
fi

bytes=$(wc -c < "${out_json}" | tr -d ' ')
echo "Wrote ${out_json} (${bytes} bytes, revision ${revision})"

# Discovery documents are Apache-2.0 (Google's stated license for
# its API discovery service output).
fetched_at=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Patch SOURCES.md: if it exists, append a row (or update an
# existing row for this file); otherwise create from a template.
sources_md="${service_dir}/spec/SOURCES.md"
if [ ! -f "${sources_md}" ]; then
  cat > "${sources_md}" <<EOF
# Vendored specs

These specs are fetched from upstream and committed to this repo so
the codegen + build are reproducible without network access. Refresh
with the matching \`scripts/fetch-*.sh\` and commit the resulting diff.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| \`${local_filename}\` | \`${service_host}\` | \`\$discovery/rest?version=v1\` (live Discovery document) | Apache-2.0 | revision \`${revision}\` | ${fetched_at} |

## License of vendored files

Google API Discovery documents are Apache-2.0. Generated code derived
from these documents is licensed AGPL-3.0 alongside the rest of
shimanism (see [\`docs/compatible-licenses.md\`](../../../docs/compatible-licenses.md)).
EOF
  echo "Wrote ${sources_md} (new)"
else
  echo "NOTE: ${sources_md} exists — review and update the ${local_filename} row by hand if the revision changed."
fi

# Inject the `_provenance` top-level key into the freshly-fetched
# JSON so the spec self-documents its origin.
go run ./cmd/inject-provenance -sources="${sources_md}" -dir="${service_dir}/spec"

echo
echo "Pinned ${local_filename} to ${service_host}@revision-${revision}"

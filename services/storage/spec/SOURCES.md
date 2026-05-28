# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-s3.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/s3.json` | Apache-2.0 | `71f1511b45ced10d1e68f9e631dcb37019759e34` | 2026-05-18T17:38:39Z |
| `azure-blob.json` | `Azure/azure-rest-api-specs` | `specification/storage/data-plane/Microsoft.BlobStorage/stable/2026-04-06/blob.json` | MIT | `be46becafeb29aa993898709e35759d3643b2809` | 2026-05-22T12:00:00Z |
| `gcp-storage-discovery.json` | `storage.googleapis.com` | `$discovery/rest?version=v1` (live Discovery document) | Apache-2.0 | revision `20260516` | 2026-05-22T11:35:00Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies. Generated
code derived from these specs is permitted under Apache 2.0's derivative-work
clause and is licensed AGPL-3.0 alongside the rest of shimanism (see
[`docs/compatible-licenses.md`](../../../docs/compatible-licenses.md) for the
overall policy).

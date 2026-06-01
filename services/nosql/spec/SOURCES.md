# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-dynamodb.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/dynamodb.json` | Apache-2.0 | `13bee3c72e77d68d6180fb633a7ce1c8714d53c0` | 2026-06-01T08:36:54Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`docs/compatible-licenses.md`](../../../docs/compatible-licenses.md)
for the overall policy).

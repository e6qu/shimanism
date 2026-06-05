# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-ecr.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/ecr.json` | Apache-2.0 | `fcf032c2fe36b0188f9b0600b2a3f9cabb74d66e` | 2026-06-05T13:33:16Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`docs/compatible-licenses.md`](../../../docs/compatible-licenses.md)
for the overall policy).

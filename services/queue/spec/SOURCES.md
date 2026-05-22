# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-sqs.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/sqs.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T10:09:27Z |
| `azure-servicebus.json` | `Azure/azure-rest-api-specs` | `specification/servicebus/data-plane/ServiceBus/stable/2021-05/servicebus.json` | MIT | `d1f7858b0e38771e072759e507e13bb536341641` | 2026-05-22T03:11:00Z |
| `gcp-queue-discovery.json` | `pubsub.googleapis.com` | `$discovery/rest?version=v1` (live Discovery document) | Apache-2.0 | revision `20260428` | 2026-05-22T11:35:00Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`doc/COMPATIBLE_LICENSES.md`](../../../doc/COMPATIBLE_LICENSES.md)
for the overall policy).

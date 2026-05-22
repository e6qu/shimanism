# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-sns.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/sns.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T14:17:00Z |
| `gcp-pubsub-discovery.json` | `pubsub.googleapis.com` | `$discovery/rest?version=v1` (live Discovery document) | Apache-2.0 | revision `20260428` | 2026-05-22T11:35:00Z |

The SNS spec covers the **publish** side of the Phase 4 pub/sub
service. The **receive** side reuses the SQS spec vendored in
[`services/queue/spec/aws-sqs.smithy.json`](../../queue/spec/aws-sqs.smithy.json)
because SNS delivers to SQS-shaped endpoints — Phase 4's AWS
backend auto-creates a backing SQS queue per subscription and the
Phase 3 SQS frontend handles the receive surface unchanged.

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`doc/COMPATIBLE_LICENSES.md`](../../../doc/COMPATIBLE_LICENSES.md)
for the overall policy).

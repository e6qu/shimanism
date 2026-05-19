# Vendored specs

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-elasticache.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/elasticache.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T15:42:00Z |

ElastiCache uses the `awsQuery` wire protocol — same as SNS (Phase
4) and RDS (Phase 5). Form-encoded request bodies dispatched on
`Action=...`, XML response envelopes.

GCP Memorystore for Redis spec is reused via
`google.golang.org/api/redis/v1` (Discovery-generated REST SDK).

Azure Cache for Redis is reused via `armredis` from
azure-sdk-for-go.

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy
models from `aws/aws-sdk-go-v2` are Apache 2.0; the upstream
`LICENSE` file applies. Generated code derived from these specs is
permitted under Apache 2.0's derivative-work clause and is licensed
AGPL-3.0 alongside the rest of shimanism.

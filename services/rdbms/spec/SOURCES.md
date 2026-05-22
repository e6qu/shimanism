# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-rds.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/rds.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T14:59:00Z |
| `gcp-rdbms-discovery.json` | `sqladmin.googleapis.com` | `$discovery/rest?version=v1` (live Discovery document) | Apache-2.0 | revision `20260510` | 2026-05-22T11:35:00Z |

The RDS spec uses the `awsQuery` wire protocol — same as Phase 4's
SNS. Form-encoded request bodies dispatched on `Action=...`, XML
response envelopes wrapped in
`<{Op}Response><{Op}Result>...<ResponseMetadata/></>`.

GCP Cloud SQL Admin spec is reused via
`google.golang.org/api/sqladmin/v1` (Discovery-generated REST SDK).

Azure DB Admin specs are reused via
`armpostgresqlflexibleservers` + `armmysqlflexibleservers` from
the azure-sdk-for-go module.

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy
models from `aws/aws-sdk-go-v2` are Apache 2.0; the upstream
`LICENSE` file applies. Generated code derived from these specs is
permitted under Apache 2.0's derivative-work clause and is licensed
AGPL-3.0 alongside the rest of shimanism (see
[`doc/COMPATIBLE_LICENSES.md`](../../../doc/COMPATIBLE_LICENSES.md)
for the overall policy).

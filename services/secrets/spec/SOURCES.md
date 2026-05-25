# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-secrets-manager.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/secrets-manager.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T07:04:47Z |
| `azure-keyvault-secrets.json` | `Azure/azure-rest-api-specs` | `specification/keyvault/data-plane/Secrets/stable/7.6/secrets.json` | MIT | `9473ef10695a6393bdd1011ce61769a79775b1ee` | 2026-05-22T00:00:00Z |
| `gcp-secretmanager-discovery.json` | `secretmanager.googleapis.com` | `$discovery/rest?version=v1` (live Discovery document) | Apache-2.0 | revision `20260424` | 2026-05-22T11:30:00Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`docs/compatible-licenses.md`](../../../docs/compatible-licenses.md)
for the overall policy).

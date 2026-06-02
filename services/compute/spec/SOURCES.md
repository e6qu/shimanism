# Vendored specs

These specs are fetched verbatim from upstream and committed to this
repo so the codegen + build are reproducible without network access.

Refresh with `scripts/fetch-aws-spec.sh` (or `make fetch-specs`)
and commit the resulting diff in a normal PR.

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-ec2.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/ec2.json` | Apache-2.0 | `e8627b4cc01977004c41ff0f42670a44d500982d` | 2026-06-02T16:16:41Z |
| `gcp-compute-discovery.json` | `www.googleapis.com` | `discovery/v1/apis/compute/v1/rest` (GCP Discovery; non-standard URL — Compute Engine does not expose `$discovery/rest`) | Apache-2.0 | revision `20260520` | 2026-06-02T16:17:00Z |
| `azure-virtualnetwork.json` | `Azure/azure-rest-api-specs` | `specification/network/resource-manager/Microsoft.Network/Network/stable/2024-10-01/virtualNetwork.json` | MIT | `21ccd4d484b76e1205207979b84d8d9b4b47fef5` | 2026-06-02T16:17:47Z |
| `azure-networksecuritygroup.json` | `Azure/azure-rest-api-specs` | `specification/network/resource-manager/Microsoft.Network/Network/stable/2024-10-01/networkSecurityGroup.json` | MIT | `21ccd4d484b76e1205207979b84d8d9b4b47fef5` | 2026-06-02T16:17:54Z |
| `azure-publicipaddress.json` | `Azure/azure-rest-api-specs` | `specification/network/resource-manager/Microsoft.Network/Network/stable/2024-10-01/publicIpAddress.json` | MIT | `21ccd4d484b76e1205207979b84d8d9b4b47fef5` | 2026-06-02T16:17:55Z |

## License of vendored files

Each vendored spec retains the license of its upstream. AWS Smithy models from
`aws/aws-sdk-go-v2` are Apache 2.0; the upstream `LICENSE` file applies.
Generated code derived from these specs is permitted under Apache 2.0's
derivative-work clause and is licensed AGPL-3.0 alongside the rest of
shimanism (see [`docs/compatible-licenses.md`](../../../docs/compatible-licenses.md)
for the overall policy).

# Vendored specs

> Phase 15.D foundational. Specs not vendored yet — frontends land in follow-on PRs that fetch + commit each upstream spec via the existing helper scripts (`scripts/fetch-aws-spec.sh`, `scripts/fetch-azure-spec.sh`, GCP Discovery refresh).

Planned vendoring (one follow-on PR per frontend):

| Local file (planned) | Upstream repo | Upstream path | Upstream license |
|---|---|---|---|
| `aws-route53.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/route-53.json` | Apache-2.0 |
| `gcp-cloud-dns-discovery.json` | `dns.googleapis.com` | `$discovery/rest?version=v1` | Apache-2.0 |
| `azure-dns.json` | `Azure/azure-rest-api-specs` | `specification/dns/resource-manager/.../<stable>/dns.json` | MIT |
| `azure-privatedns.json` | `Azure/azure-rest-api-specs` | `specification/privatedns/resource-manager/.../<stable>/privatedns.json` | MIT |

Frontends use the existing codegen lanes:

- Route 53 → `cmd/codegen` (AWS Smithy REST-XML protocol).
- Cloud DNS → `cmd/gcp-codegen` (Discovery → routing-only).
- Azure DNS + Private DNS → `cmd/azure-codegen` (one frontend dispatches on `domain.ZoneVisibility`).

The foundational PR (this one) lands the domain layer + inmem backend without spec ingest. Frontend PRs vendor the specs they need.

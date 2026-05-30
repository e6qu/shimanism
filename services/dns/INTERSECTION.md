# DNS — intersection inventory

> Phase 15.D foundational. Scoping in [`docs/phase-15-cd-scoping.md`](../../docs/phase-15-cd-scoping.md).

## Per-cloud surface

| Cloud | Service | Wire protocol | Auth |
|---|---|---|---|
| AWS | Route 53 | HTTP/REST + XML | SigV4 |
| GCP | Cloud DNS (`dns.googleapis.com/v1`) | HTTP/REST | Bearer (Google ID token) |
| Azure | Azure DNS (`Microsoft.Network/dnszones`) + Azure Private DNS (`Microsoft.Network/privateDnsZones`) | HTTP/REST (ARM) | Bearer (Microsoft Entra) |
| K8s peer | CoreDNS via file-based zone config | n/a (file mount + reload) | n/a |

## Intersection (target operations)

| Operation | Route 53 | Cloud DNS | Azure DNS | Azure Private DNS | CoreDNS | Status |
|---|---|---|---|---|---|---|
| `CreateZone` | `CreateHostedZone` | `managedZones.create` | `Zones.CreateOrUpdate` | `PrivateZones.CreateOrUpdate` | zone-file create | 1 |
| `GetZone` | `GetHostedZone` | `managedZones.get` | `Zones.Get` | `PrivateZones.Get` | read zone-file | 1 |
| `DeleteZone(force=false)` | `DeleteHostedZone` (fails if not empty) | `managedZones.delete` (fails if not empty) | `Zones.Delete` (fails if not empty) | `PrivateZones.Delete` (fails if not empty) | unlink zone-file | 1 |
| `DeleteZone(force=true)` | sweep record sets + delete | sweep + delete | sweep + delete | sweep + delete | overwrite + remove | 1 |
| `ListZones` | `ListHostedZones` (+ paging) | `managedZones.list` | `Zones.List` | `PrivateZones.List` | enumerate mounts | 1 |
| `PutRecordSet` | `ChangeResourceRecordSets` (UPSERT) | `resourceRecordSets.{create,patch}` | `RecordSets.CreateOrUpdate` | `RecordSets.CreateOrUpdate` (private) | edit zone-file | 1 |
| `GetRecordSet` | `ListResourceRecordSets` filtered | `resourceRecordSets.get` | `RecordSets.Get` | `RecordSets.Get` | parse zone-file | 1 |
| `DeleteRecordSet` | `ChangeResourceRecordSets` (DELETE) | `resourceRecordSets.delete` | `RecordSets.Delete` | `RecordSets.Delete` | remove from zone-file | 1 |
| `ListRecordSets` | `ListResourceRecordSets` (+ paging) | `resourceRecordSets.list` | `RecordSets.ListByDNSZone` | `RecordSets.ListByDnsZone` | scan zone-file | 1 |

**Record types in intersection:** A, AAAA, CNAME, MX, NS, SOA, SRV, TXT.

## Out of intersection (category 3)

- **DNSSEC** — signed records, KSK rotation. Vendor-specific configuration; cross-cloud key portability is not meaningful.
- **Route 53 alias records** — AWS-specific (ALIAS to an ELB / CloudFront / S3 / API GW). No cross-cloud equivalent.
- **Geo / latency / failover routing policies** — AWS-specific record-set routing-policy attribute; GCP / Azure don't have the same model.
- **Health checks** — Route 53 has them; Cloud DNS / Azure don't expose the same primitive.
- **DNSSEC NSEC / NSEC3 proofs** — depends on cloud-side DNSSEC signing.
- **VPC associations for private zones (cross-cloud)** — each cloud's VPC namespace is vendor-specific. Within a single destination cloud the shim plumbs `CreateZoneOptions.PrivateVPCs` through opaquely; cross-cloud (e.g. AWS-shape Terraform with `vpc_id = "vpc-aws-XXX"` against an Azure backend) fails with the destination's "no such VPC" error. Documented in [`APPLY_INTERSECTION.md`](APPLY_INTERSECTION.md).

## Normalisation rules applied

- **N3** Tags vs labels (GCP constraint enforcement) — applies to `Zone.Tags`.
- **N4** Description encoding (`shim-description` label on GCP) — applies to `Zone.Description`.
- **N6** Region / location naming — Route 53 is global; Cloud DNS is global; Azure DNS is regional but the resource is exposed cluster-wide. No translation; opaque string at the domain.
- **N17 (new)** Zone visibility dispatch — `Zone.Visibility = Public | Private` collapses Azure DNS / Azure Private DNS / Route 53 public/private hosted zones / Cloud DNS public/private managed zones into one domain abstraction. Backends dispatch on `Visibility`.

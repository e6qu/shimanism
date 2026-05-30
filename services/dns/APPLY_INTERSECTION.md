# DNS — Apply intersection

> What `terraform apply` of source-cloud-shape DNS resources composes cleanly through the shim. Phase 15.D foundational.

## Resources in contract

| Source cloud | Resource | Intersection notes |
|---|---|---|
| AWS | `aws_route53_zone` | public hosted zone → `CreateZone(Visibility=Public)`. private → `Visibility=Private` + `PrivateVPCs`. |
| AWS | `aws_route53_record` | maps to `PutRecordSet` for the (name, type) tuple. |
| GCP | `google_dns_managed_zone` | `visibility = "public"` → `Visibility=Public`; `"private"` → `Visibility=Private` + `PrivateVPCs`. |
| GCP | `google_dns_record_set` | maps to `PutRecordSet`. |
| Azure | `azurerm_dns_zone` (public) | → `CreateZone(Visibility=Public)`. |
| Azure | `azurerm_private_dns_zone` | → `CreateZone(Visibility=Private)`. |
| Azure | `azurerm_dns_<rt>_record` / `azurerm_private_dns_<rt>_record` | one resource per record type, maps to `PutRecordSet` with `Type = <rt>`. |

## Out of contract

- **DNSSEC** (`aws_route53_key_signing_key`, `google_dns_response_policy_rule`, equivalents). Cross-cloud key portability is not meaningful.
- **Route 53 alias records** (`aws_route53_record.alias { ... }`). AWS-specific (ALIAS to an AWS resource). No cross-cloud equivalent.
- **Geo / latency / failover routing policies** on Route 53 record sets. AWS-specific routing-policy attribute.
- **Health checks** (`aws_route53_health_check`). AWS-specific.
- **VPC associations on private zones across clouds.** Within a single destination cloud, the shim plumbs `private_vpcs` through opaquely (the destination cloud validates the VPC ID against its own namespace). Cross-cloud (e.g. AWS-shape `vpc_id = "vpc-aws-XXX"` against an Azure backend that expects `/subscriptions/.../virtualNetworks/...`) fails at the backend with the destination cloud's "no such VPC" error. Honest cross-cloud answer: use the destination cloud's VPC ID format in the source-cloud-shape Terraform, or accept the cell is out-of-intersection for that user.
- **Response-policy rules / firewalls** (`google_dns_response_policy_rule`, `aws_route53_resolver_firewall_rule`). Vendor-specific.

## What this contract commits the shim to

1. Accept the in-contract resources; round-trip through Read with no drift on AWS / GCP / Azure / CoreDNS / inmem cells.
2. Reject out-of-contract attributes with the source cloud's real error envelope (e.g. Route 53's `InvalidInput` for ALIAS-only fields against an Azure backend).
3. `DeleteZone(force=true)` sweeps user-managed record sets but **does not** touch the cloud-managed SOA + NS records (those go with the zone).
4. Cross-cloud Apply that touches `PrivateVPCs` only composes when the user supplies a destination-cloud-shaped VPC ID. Documented above.

## Open BUGs touching this contract

(None yet — service is foundational.)

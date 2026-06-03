# Compute Service — Intersection and NotImplemented Registry

This file documents which operations are in-intersection (implemented),
which are out-of-intersection (return the source cloud's "not supported"
error), and which are K8s-peer-specific NotImplemented rows.

See [docs/normalizations.md](../../docs/normalizations.md) rules N20–N27
for the normalization decisions that shaped this intersection.

## Phase 16.B — VPC Networking Primitives

### Networks (VPC / VNet / GCP Network)

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| CreateNetwork | `CreateVpc` | `networks.insert` | `virtualNetworks.createOrUpdate` | `namespaces.create` | K8s: name = network name |
| GetNetwork | `DescribeVpcs` | `networks.get` | `virtualNetworks.get` | `namespaces.get` | |
| ListNetworks | `DescribeVpcs` (no filter) | `networks.list` | `virtualNetworks.list` | `namespaces.list` | K8s: filtered by `shimanism.io/managed=true` label |
| DeleteNetwork | `DeleteVpc` | `networks.delete` | `virtualNetworks.delete` | `namespaces.delete` | |
| ModifyVpcAttribute | `ModifyVpcAttribute` | — | — | — | Accepted, acknowledged; DNS settings not in domain intersection |

### Subnets

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| CreateSubnet | `CreateSubnet` | `subnetworks.insert` | `subnets.createOrUpdate` | **NotImplemented** | K8s: no subnet primitive in intersection |
| GetSubnet | `DescribeSubnets` | `subnetworks.get` | `subnets.get` | **NotImplemented** | |
| ListSubnets | `DescribeSubnets` | `subnetworks.list` | — | **NotImplemented** | |
| DeleteSubnet | `DeleteSubnet` | `subnetworks.delete` | `subnets.delete` | **NotImplemented** | |
| ModifySubnetAttribute | `ModifySubnetAttribute` | — | — | — | Accepted, acknowledged; per N26 (no zone concept in intersection) |

K8s NotImplemented response: `UnsupportedOperation` (EC2 envelope) / `UNIMPLEMENTED` (GCP) / `OperationNotSupported` (Azure ARM).

### Security Groups / Firewalls / NSGs

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| CreateSecurityGroup | `CreateSecurityGroup` | `firewalls.insert` | `networkSecurityGroups.createOrUpdate` | `networkpolicies.create` | K8s: NetworkPolicy in parentNS |
| GetSecurityGroup | `DescribeSecurityGroups` | `firewalls.get` | `networkSecurityGroups.get` | `networkpolicies.get` | |
| ListSecurityGroups | `DescribeSecurityGroups` | `firewalls.list` | `networkSecurityGroups.list` | `networkpolicies.list` | |
| DeleteSecurityGroup | `DeleteSecurityGroup` | `firewalls.delete` | `networkSecurityGroups.delete` | `networkpolicies.delete` | |
| AddIngressRule | `AuthorizeSecurityGroupIngress` | `firewalls.patch` (allowed) | `securityRules.createOrUpdate` | `networkpolicies.update` (Ingress) | |
| RemoveIngressRule | `RevokeSecurityGroupIngress` | `firewalls.patch` (remove) | `securityRules.delete` | `networkpolicies.update` | |
| AddEgressRule | `AuthorizeSecurityGroupEgress` | `firewalls.patch` (egress) | `securityRules.createOrUpdate` (Outbound) | `networkpolicies.update` (Egress) | |
| RemoveEgressRule | `RevokeSecurityGroupEgress` | `firewalls.patch` (remove) | `securityRules.delete` | `networkpolicies.update` | |
| DescribeSecurityGroupRules | `DescribeSecurityGroupRules` | — | — | — | Derived from GetSecurityGroup rules; GCP/Azure have no direct equivalent |

**N21 intersection note**: Rules are allow-only (no deny). Priorities (Azure NSG) are synthetic and not stored in the domain. GCP firewall tag targeting is out-of-intersection.

### Public IPs (EIP / External Address / Azure Public IP)

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| AllocatePublicIP | `AllocateAddress` | `addresses.insert` | `publicIPAddresses.createOrUpdate` | **NotImplemented** | K8s: no public IP primitive |
| AssociatePublicIP | `AssociateAddress` | (via instance NIC) | (via NIC/LB) | **NotImplemented** | GCP/Azure: acknowledged at domain level; actual NIC plumbing is out-of-intersection |
| DisassociatePublicIP | `DisassociateAddress` | (via instance NIC) | (via NIC/LB) | **NotImplemented** | |
| ReleasePublicIP | `ReleaseAddress` | `addresses.delete` | `publicIPAddresses.delete` | **NotImplemented** | |
| ListPublicIPs | `DescribeAddresses` | `addresses.list` | `publicIPAddresses.list` | **NotImplemented** | |

K8s NotImplemented response: same error codes as Subnets above.

### Tags

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| CreateTags | `CreateTags` | (via resource `labels`) | (via resource `tags`) | (via resource labels) | AWS tags accepted, acknowledged; stateless shim doesn't persist cross-request |
| DeleteTags | `DeleteTags` | — | — | — | Accepted, acknowledged |
| DescribeTags | `DescribeTags` | — | — | — | Returns empty list (tags stored on resources, not tag store) |

## Out-of-Intersection (Phase 16.B)

These operations exist in at least one cloud but are NOT in the intersection
and return the source cloud's own "not supported" error:

- **NAT Gateways** (AWS/GCP/Azure) — complex; no K8s analog
- **Internet Gateways** (AWS) — implicit in GCP/Azure; no K8s analog
- **Route Tables / VPC Peering** — cloud-specific routing; no portable shape
- **DHCP Options Sets** (AWS) — no equivalent in GCP/Azure
- **Placement Groups / AZs** (AWS) — scheduling primitives; not in intersection
- **VPC Flow Logs** — monitoring; out of scope
- **IPv6 CIDRs / IPAM** — IPv6 is out-of-intersection for Phase 16.B
- **VPN Gateways** — connectivity products; out of scope

## Phase 16.C — Compute Instance Lifecycle

### Instances

| Operation | AWS EC2 | GCP Compute v1 | Azure Compute | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| RunInstances / Insert | `RunInstances` | `instances.insert` | `virtualMachines.createOrUpdate` | **NotImplemented** | |
| DescribeInstances / Get | `DescribeInstances` | `instances.get` / `instances.list` / `instances.aggregatedList` | `virtualMachines.get` / `virtualMachines.list` | `nodes.list` (read-only) | K8s: instance type from Node capacity; no state mutations |
| StartInstances | `StartInstances` | `instances.start` | `virtualMachines.start` | **NotImplemented** | |
| StopInstances | `StopInstances` | `instances.stop` | `virtualMachines.deallocate` | **NotImplemented** | |
| TerminateInstances / Delete | `TerminateInstances` | `instances.delete` | `virtualMachines.delete` | **NotImplemented** | AWS: instance stays as "terminated" for ~1hr (N20); GCP: instance removed; Azure: resource deleted |
| RebootInstances | `RebootInstances` | `instances.reset` | `virtualMachines.restart` | **NotImplemented** | |

**N20 instance lifecycle note**: AWS "terminated" state stays visible by instance ID for ~1 hour. GCP "TERMINATED" means stopped (not deleted). The shim inmem backend keeps terminated instances so AWS API waiters see the terminal state; GCP's list-all excludes them. See `docs/normalizations.md` rule N20.

### Machine Types

| Operation | AWS EC2 | GCP Compute v1 | Azure Compute | K8s peer | Notes |
|-----------|---------|---------------|---------------|----------|-------|
| DescribeInstanceTypes | `DescribeInstanceTypes` | `machineTypes.list` / `machineTypes.get` / `machineTypes.aggregatedList` | `virtualMachineSizes.list` | Node capacity | Fixed catalog: t3.micro (2 vCPU/1 GiB), t3.small (2/2), m5.large (2/8), m5.xlarge (4/16), c5.xlarge (4/8), r5.large (2/16) |

### Out-of-Intersection (Phase 16.C)

- **Auto Scaling Groups / Managed Instance Groups / VMSS** — diverge too much; future phase
- **EBS / Persistent Disk / Azure Managed Disk** — block storage is a separate phase
- **Instance Metadata Service (IMDS)** — tracked in sockerless #371
- **Spot / Preemptible / Spot VM** — pricing/availability semantics differ
- **Placement Groups / Availability Sets** — AWS-/Azure-specific scheduling
- **SSH keys / OS Login / Azure admin credentials** — instance-access; out of intersection
- **User data / startup scripts** — execution environment varies; out of intersection
- **Instance store volumes** — AWS-only
- **Custom images (AMI create/copy, image families)** — image management is a separate concern; the shim accepts any opaque image reference (N24)

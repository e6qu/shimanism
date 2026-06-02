# Phase 16 scoping — Compute and Networking

> Pre-implementation audit for Phase 16: VPC networking primitives, compute instance lifecycle,
> and layer-4 TCP load balancers. Follows the pattern established by
> [`docs/phase-15-cd-scoping.md`](phase-15-cd-scoping.md).

## Service layout

Two new service directories:

- **`services/compute/`** — VPC networking (networks, subnets, security groups, public IPs) + compute
  instance lifecycle (run/start/stop/terminate/describe + machine types). AWS VPC and EC2 instances
  share `ec2.amazonaws.com`; GCP networks and GCE instances share `compute.googleapis.com/v1` — one
  service directory handles both to avoid action-dispatch routing conflicts.
- **`services/loadbalancer/`** — layer-4 TCP load balancers. ELBv2 is a distinct AWS API endpoint
  (`elasticloadbalancing.amazonaws.com`); separate service directory is clean.

Internal domain split inside `services/compute/`:
- `internal/compute/domain/networking.go` — Network, Subnet, SecurityGroup, PublicIP interfaces
- `internal/compute/domain/instances.go` — Instance, MachineType interfaces

---

## Per-cloud surfaces

### 16.B — VPC networking

| Operation | AWS EC2 | GCP Compute v1 | Azure Network | K8s |
|---|---|---|---|---|
| CreateNetwork | `CreateVpc` | `networks.insert` | `virtualNetworks.createOrUpdate` | `namespaces.create` |
| GetNetwork | `DescribeVpcs` (filtered) | `networks.get` | `virtualNetworks.get` | `namespaces.get` |
| ListNetworks | `DescribeVpcs` | `networks.list` | `virtualNetworks.list` | `namespaces.list` |
| DeleteNetwork | `DeleteVpc` | `networks.delete` | `virtualNetworks.delete` | `namespaces.delete` |
| CreateSubnet | `CreateSubnet` | `subnetworks.insert` | `subnets.createOrUpdate` | NotImplemented |
| GetSubnet / ListSubnets | `DescribeSubnets` | `subnetworks.get` / `list` | `subnets.get` / `list` | NotImplemented |
| DeleteSubnet | `DeleteSubnet` | `subnetworks.delete` | `subnets.delete` | NotImplemented |
| CreateSecurityGroup | `CreateSecurityGroup` | `firewalls.insert` | `networkSecurityGroups.createOrUpdate` | `networkpolicies.create` |
| DeleteSecurityGroup | `DeleteSecurityGroup` | `firewalls.delete` | `networkSecurityGroups.delete` | `networkpolicies.delete` |
| ListSecurityGroups | `DescribeSecurityGroups` | `firewalls.list` | `networkSecurityGroups.list` | `networkpolicies.list` |
| AddIngressRule | `AuthorizeSecurityGroupIngress` | `firewalls.patch` (add allowed) | `securityRules.createOrUpdate` (Inbound Allow) | NetworkPolicy ingress rule |
| AddEgressRule | `AuthorizeSecurityGroupEgress` | `firewalls.patch` (add allowed) | `securityRules.createOrUpdate` (Outbound Allow) | NetworkPolicy egress rule |
| RemoveIngressRule | `RevokeSecurityGroupIngress` | `firewalls.patch` (remove allowed) | `securityRules.delete` | NetworkPolicy patch |
| RemoveEgressRule | `RevokeSecurityGroupEgress` | `firewalls.patch` (remove allowed) | `securityRules.delete` | NetworkPolicy patch |
| AllocatePublicIP | `AllocateAddress` | `addresses.insert` (EXTERNAL) | `publicIPAddresses.createOrUpdate` | NotImplemented |
| AssociatePublicIP | `AssociateAddress` | `instances.updateNetworkInterface` | NIC `publicIPAddress` update | NotImplemented |
| DisassociatePublicIP | `DisassociateAddress` | `instances.updateNetworkInterface` (remove) | NIC update (remove publicIP) | NotImplemented |
| ReleasePublicIP | `ReleaseAddress` | `addresses.delete` | `publicIPAddresses.delete` | NotImplemented |

### 16.C — Compute instance lifecycle

| Operation | AWS EC2 | GCP Compute v1 | Azure Compute | K8s |
|---|---|---|---|---|
| RunInstances | `RunInstances` | `instances.insert` | `virtualMachines.createOrUpdate` | NotImplemented |
| DescribeInstances | `DescribeInstances` | `instances.get` + `aggregatedList` | `virtualMachines.get` + `listAll` | `nodes.list` / `get` (read-only) |
| StartInstances | `StartInstances` | `instances.start` | `virtualMachines.start` | NotImplemented |
| StopInstances | `StopInstances` | `instances.stop` | `virtualMachines.deallocate` | NotImplemented |
| TerminateInstances | `TerminateInstances` | `instances.delete` | `virtualMachines.delete` | NotImplemented |
| RebootInstances | `RebootInstances` | `instances.reset` | `virtualMachines.restart` | NotImplemented |
| DescribeInstanceTypes | `DescribeInstanceTypes` | `machineTypes.list` / `get` | `virtualMachineSizes.list` | Node capacity fields |

### 16.D — Load balancers (layer-4 TCP only)

| Operation | AWS ELBv2 | GCP Compute v1 | Azure Network | K8s |
|---|---|---|---|---|
| CreateLoadBalancer | `CreateLoadBalancer` (type=network) | `forwardingRules.insert` + `targetPools.insert` + `healthChecks.insert` | `loadBalancers.createOrUpdate` | `services.create` (type:LoadBalancer) |
| DeleteLoadBalancer | `DeleteLoadBalancer` | delete forwarding rule + target pool + health check | `loadBalancers.delete` | `services.delete` |
| DescribeLoadBalancers | `DescribeLoadBalancers` | `forwardingRules.list` | `loadBalancers.list` | `services.list` (type=LoadBalancer) |
| CreateTargetGroup | `CreateTargetGroup` | `targetPools.insert` | (backend pool is inside the LB resource) | Endpoints resource |
| DeleteTargetGroup | `DeleteTargetGroup` | `targetPools.delete` | update LB remove backend pool | delete Endpoints |
| RegisterTargets | `RegisterTargets` | `targetPools.addInstance` | LB backend pool update | Endpoints subsets update |
| DeregisterTargets | `DeregisterTargets` | `targetPools.removeInstance` | LB backend pool update | Endpoints subsets update |
| CreateListener | `CreateListener` | (forwardingRule IS the listener) | inbound LB rule + frontend IP config inside LB | Service port entry |
| DeleteListener | `DeleteListener` | delete forwardingRule | delete LB rule | remove Service port |
| DescribeListeners | `DescribeListeners` | `forwardingRules.get` | LB rules list | Service ports |

---

## Out of intersection (Phase 16)

The following features exist on at least one cloud but are excluded from the Phase 16 intersection.
Requests targeting these features return the source cloud's own "not supported" error vocabulary.

- **NAT Gateways** — no clean K8s analog; complex async provisioning.
- **Internet Gateways** (AWS) — implicit in GCP/Azure; routing abstraction differs.
- **Route Tables / custom routing** — significant per-cloud divergence.
- **VPC Peering** — bilateral resource creation; no K8s analog.
- **Auto Scaling Groups** — future phase (depends on compute being established).
- **EBS / Persistent Disk / Azure Managed Disk** — block storage is a separate future phase.
- **ENIs / vNICs** — lower-level than the intersection; instance-to-SG attachment goes through the instance.
- **Placement Groups / Availability Sets** — scheduling primitives; AWS-specific names.
- **L7 load balancers** (ALB, HTTPS rules, host/path routing, TLS) — per N27.
- **VPN Gateways, Direct Connect, Interconnect, ExpressRoute** — connectivity products.
- **Instance Metadata Service (IMDS)** — in-guest plane; tracked in sockerless #371.
- **Spot / Preemptible / Spot VM pricing** — instance lifecycle variants; future phase.
- **Key Pairs / SSH keys at the fleet level** — per-instance cloud-init / metadata; out of intersection.

---

## Spec sources

| Service | Cloud | Spec | Upstream | Protocol |
|---|---|---|---|---|
| compute | AWS | `services/compute/spec/aws-ec2.smithy.json` | `aws/aws-sdk-go-v2` → `aws-models/ec2-2016-11-15.json` | `ec2Query` |
| compute | GCP | `services/compute/spec/gcp-compute-discovery.json` | `https://compute.googleapis.com/$discovery/rest?version=v1` | HTTP REST (Discovery) |
| compute | Azure (Compute) | `services/compute/spec/azure-compute.json` | `Azure/azure-rest-api-specs/specification/compute/resource-manager/` | ARM OpenAPI v2 |
| compute | Azure (Network) | `services/compute/spec/azure-network.json` | `Azure/azure-rest-api-specs/specification/network/resource-manager/` | ARM OpenAPI v2 |
| loadbalancer | AWS | `services/loadbalancer/spec/aws-elbv2.smithy.json` | `aws-models/elastic-load-balancing-v2-2015-12-01.json` | `awsQuery` |
| loadbalancer | GCP | (subset of Compute v1 Discovery) | shared with compute | HTTP REST (Discovery) |
| loadbalancer | Azure | (Microsoft.Network LB resources from azure-network.json) | shared with compute networking | ARM OpenAPI v2 |

---

## Codegen impact

### ec2Query (new — delivered in 16.A)

EC2 uses `aws.protocols#ec2Query`. The codegen lane was added in 16.A:

- `internal/ec2query/` — runtime package: `Router` (Action dispatch), `WriteResult` / `WriteError` /
  `WriteBackendError` with EC2 wire envelopes, `WithForm` / `FormFromContext`.
- `internal/codegen/emit/template_ec2query.tmpl` — template: flattened list decode (`Field.N`,
  no `.member.` interfix), ec2query imports.
- `internal/codegen/emit/emit.go` — `ec2-query` branch in `serviceProtocol()` and `pickTemplate()`.

The EC2 Smithy spec is large (~200k operations on the full surface). `services/compute/codegen.json`
will enumerate only the intersection operations; the codegen pipeline emits stubs only for those.

### ELBv2 (16.D)

ELBv2 uses standard `awsQuery` — the same lane as SNS/RDS/ElastiCache. No new codegen work.

### GCP Compute v1 (16.B)

Net-new Discovery doc. GCP codegen generates routing-only (`cmd/gcp-codegen` pipeline already in
place). One new `services/compute/gcp-codegen.json`.

### Azure Compute + Network (16.B / 16.C)

Both `Microsoft.Compute` and `Microsoft.Network` are new ARM specs. The existing 8-stage Azure
preprocessor pipeline handles both. Two `codegen.json` files.

---

## K8s peer design

### Networking (16.B)

| Domain concept | K8s resource | Notes |
|---|---|---|
| Network (VPC/VNet) | `Namespace` | one Namespace per network name |
| Subnet | — | NotImplemented; no subnet primitive in K8s API |
| SecurityGroup | `NetworkPolicy` | `podSelector: {}` applies to all pods in namespace; one NetworkPolicy per SG |
| PublicIP (allocate/associate) | — | NotImplemented; no public-IP primitive (LB Service IPs are in the LB domain) |

Security group rule translation to NetworkPolicy:
- Ingress allow rule → `NetworkPolicy.spec.ingress[{ports, from: [{ipBlock}]}]`
- Egress allow rule → `NetworkPolicy.spec.egress[{ports, to: [{ipBlock}]}]` + `policyTypes: [Ingress, Egress]`
- CIDR `0.0.0.0/0` → `ipBlock: {cidr: "0.0.0.0/0"}` (allow all)

### Compute (16.C)

| Operation | K8s mapping | Fidelity |
|---|---|---|
| `DescribeInstances` | `nodes.list` / `nodes.get` | Read-only; Node capacity fields → `InstanceType` |
| `DescribeInstanceTypes` | enumerate distinct Node capacity profiles | Read-only |
| `RunInstances` | NotImplemented | Cannot create K8s nodes from the shim |
| `StartInstances` | NotImplemented | No start/stop semantics on Node objects |
| `StopInstances` | NotImplemented | Same |
| `TerminateInstances` | NotImplemented | Deleting a Node is dangerous; out of intersection |
| `RebootInstances` | NotImplemented | Same |

All NotImplemented operations return the source cloud's `UnsupportedOperation` (AWS) /
`OperationNotSupported` (GCP/Azure) error in the source cloud's error envelope — never a generic 500.

### Load balancer (16.D)

| Operation | K8s resource | Notes |
|---|---|---|
| CreateLoadBalancer | `Service` (type:LoadBalancer) | name from LB name |
| DeleteLoadBalancer | `services.delete` | |
| DescribeLoadBalancers | `services.list` + `services.get` | filter by type:LoadBalancer |
| CreateTargetGroup | `Endpoints` | associated with Service by same name |
| RegisterTargets | patch `Endpoints.subsets` | add address + port |
| DeregisterTargets | patch `Endpoints.subsets` | remove address |
| CreateListener | add port to `Service.spec.ports` | protocol:TCP only |
| DeleteListener | remove port from `Service.spec.ports` | |

---

## Sockerless conformance lane

### 16.B (networking)

Networking operations (VPC/SG/EIP CRUD) are pure metadata API calls in sockerless — no Firecracker
boot required. The sockerless lane for 16.B can be green immediately, without waiting for sockerless
issues #373 / #374 / #375.

### 16.C (compute instances)

Instance lifecycle state transitions require Firecracker to confirm VM boot. The sockerless conformance
lane for 16.C is gated on the following sockerless issues closing:

| Issue | Blocker |
|---|---|
| [#373](https://github.com/e6qu/sockerless/issues/373) | `/dev/kvm` not in capability check → Firecracker exits before API socket |
| [#374](https://github.com/e6qu/sockerless/issues/374) | 3 GB rootfs per VM → disk exhaustion on 14 GB CI runners |
| [#375](https://github.com/e6qu/sockerless/issues/375) | kernel + rootfs not cached → 5-min test timeout too tight on cold runners |

The test file `services/compute/conformance/sockerless_test.go` will use a
`SOCKERLESS_COMPUTE_TLS_PORT` guard and skip with a clear message referencing the upstream issues
until they close — the same pattern as prior phases with upstream deps.

### 16.D (load balancers)

LB create / describe / delete: no Firecracker dependency. The sockerless lane for LB CRUD can be
green immediately. `RegisterTargets` / `DeregisterTargets` are gated on #373/#374/#375 (adding VM
instances to a backend pool requires instances to exist in the simulator).

---

## Normalization rules (16.A deliverables)

Rules N20–N27 are published in [`docs/normalizations.md`](normalizations.md). Cross-reference:

| Rule | Applies to |
|---|---|
| N20 — Instance state machine | 16.C: all instance backends |
| N21 — Security group semantics (allow-only, stateful intersection) | 16.B: all security group backends |
| N22 — Public IP lifecycle (allocate + associate two-step) | 16.B: public IP backends |
| N23 — Machine type naming (opaque per-cloud) | 16.C: RunInstances + DescribeInstanceTypes |
| N24 — Instance image reference (opaque per-cloud) | 16.C: RunInstances |
| N25 — VPC CIDR assignment (optional at network level for GCP) | 16.B: CreateNetwork |
| N26 — Subnet AZ (AZ-scoped AWS vs region-scoped GCP vs VNet-scoped Azure) | 16.B: CreateSubnet |
| N27 — LB layer restriction (layer-4 TCP only) | 16.D: all LB operations |

---

## PR sequence

### 16.A (this PR) — 1 PR

- [x] `docs/phase-16-scoping.md` (this file)
- [x] N20–N27 in `docs/normalizations.md`
- [x] `internal/ec2query/` runtime package
- [x] `internal/codegen/emit/template_ec2query.tmpl` + emit.go hooks
- [x] `internal/codegen/ec2query_test.go` round-trip test

### 16.B — 3–4 PRs (after 16.A)

1. Spec + codegen + `internal/compute/domain/networking.go` + inmem networking backend
2. AWS EC2 frontend (ec2Query, VPC/subnet/SG/EIP actions) + GCP Compute v1 frontend (networking)
3. Azure Network frontend (VNet/Subnet/NSG/PublicIPAddress)
4. K8s peer (Namespace + NetworkPolicy) + full conformance matrix (SDK + CLI + Terraform × 3 frontends × 4 backends) + sockerless networking lane

### 16.C — 3–4 PRs (after 16.B)

1. `internal/compute/domain/instances.go` + inmem instance backend
2. AWS EC2 frontend (instance Action handlers, same listener/router as 16.B)
3. GCP Compute v1 frontend (instances.insert/delete/start/stop/reset + machineTypes)
4. Azure Compute frontend (VMs + VirtualMachineSizes) + K8s peer (Nodes read-only) + full conformance matrix + sockerless instance lane (skip until #373–375)

### 16.D — 2–3 PRs (after 16.A, parallels 16.B)

1. `services/loadbalancer/` spec + codegen + `internal/loadbalancer/domain/` + inmem backend
2. All 3 frontends (ELBv2 awsQuery + GCP Compute v1 LB + Azure Network LB)
3. K8s peer (Service + Endpoints) + full conformance matrix + sockerless LB lane (RegisterTargets skip until #373–375)

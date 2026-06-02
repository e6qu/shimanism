# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Read top-to-bottom; pick up where Phase 16 planning left off.

## Where we are

**Phase 15 fully closed** (2026-06-02). All sub-phases complete: 15.A ✅ · 15.B ✅ · 15.C ✅ · 15.D ✅.

**Phase 16 planned** (2026-06-02). Two new services: `services/compute/` (VPC networking + instance lifecycle) and `services/loadbalancer/` (layer-4 TCP LBs). See [PLAN.md § Phase 16](PLAN.md#phase-16--compute-and-networking) for the full plan.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A, blocked on real-cloud credentials — not actionable).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. If `/tmp/sockerless` is stale: `git -C /tmp/sockerless pull --ff-only`, rebuild sims, rerun `make sockerless` (baseline: all 10 packages green).
3. Start Phase 16 with **16.A** (unblocked): scoping doc + N20–N27 normalization rules + ec2Query codegen lane.
4. Create branch `phase-16a-scoping` from `main` for the first 16.A PR.

## Sockerless rebuild (when needed)

```sh
git -C /tmp/sockerless pull --ff-only
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/aws/simulator-aws /tmp/sockerless/simulators/aws/
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/gcp/simulator-gcp /tmp/sockerless/simulators/gcp/
GOWORK=off CGO_ENABLED=0 go build -tags noui -o /tmp/sockerless/simulators/azure/simulator-azure /tmp/sockerless/simulators/azure/
cd /Users/zardoz/projects/shimanism && make sockerless
```

The Azure sim requires `SIM_SERVICEBUS_AMQP_LISTEN_ADDR` on a separate port (handled by `scripts/run-sockerless-storage.sh`).

## Phase 16 sub-phase checklist

### 16.A — Normalization audit + scoping + ec2Query codegen ✅ (PR #104)

- [x] `docs/phase-16-scoping.md` written.
- [x] N20–N27 added to `docs/normalizations.md`.
- [x] `internal/ec2query/` runtime package (router + error/response envelopes + tests).
- [x] `internal/codegen/emit/template_ec2query.tmpl` (flattened list decode, ec2query imports).
- [x] `internal/codegen/emit/emit.go` ec2Query detection + pickTemplate branch.
- [x] `internal/codegen/ec2query_test.go` round-trip test with synthetic Smithy model.

### 16.B — VPC networking primitives (3–4 PRs, after 16.A)

- [x] Vendor specs: `services/compute/spec/` — AWS `ec2-2016-11-15.json`, GCP `gcp-compute-discovery.json`, Azure `azure-network.json`. Add `services/compute/spec/SOURCES.md`. ✅ PR #105
- [x] Codegen: `services/compute/codegen.json` (AWS+Azure) + `services/compute/gcp-codegen.json`; run `make codegen`. ✅ PR #105
- [x] Define `internal/compute/domain/networking.go` — `Networking` interface + all types. ✅ PR #105
- [x] `services/compute/backends/inmem/` — inmem networking backend (7 passing tests). ✅ PR #105
- [x] BUG-52 (ec2query codegen @xmlName bug): fixed `template_ec2query.tmpl` + regenerated. ✅ this PR
- [x] AWS EC2 frontend (`internal/compute/frontends/aws_ec2/`) — ec2Query handler for VPC/subnet/SG/EIP actions. ✅ this PR
- [x] GCP Compute v1 frontend (`internal/compute/frontends/gcp_compute/`) — networks/subnetworks/firewalls/addresses. ✅ this PR
- [x] Real AWS backend (`services/compute/backends/aws/`) — EC2 SDK. ✅ this PR
- [x] Real GCP backend (`services/compute/backends/gcp/`) — Compute v1 API. ✅ this PR
- [x] Harness functions: `StartComputeServerAWS`, `StartComputeServerGCP`. ✅ this PR
- [x] Conformance SDK tests: AWS (VPC/Subnet/SG/EIP lifecycle, all green). ✅ this PR
- [x] Conformance GCP SDK tests: Network/Firewall lifecycle, all green. ✅ this PR
- [x] Sockerless networking lane (no Firecracker dep). ✅ this PR
- [x] Azure Network frontend (`internal/compute/frontends/azure_network/`) — VNet/Subnet/NSG/PublicIPAddress ARM handlers. ✅ this PR
- [x] Real Azure backend (`services/compute/backends/azure/`) — armnetwork/v6. ✅ this PR
- [x] Azure SDK conformance tests: VNet/Subnet/NSG/PublicIP lifecycle, all green. ✅ this PR
- [ ] K8s peer (`services/compute/backends/k8scompute/`) — Namespace (VPC), NetworkPolicy (SG); subnets/EIPs return NotImplemented. (PR4)
- [ ] CLI conformance tests (aws ec2, gcloud compute, az network). (PR4)
- [ ] Terraform conformance tests (hashicorp/aws + hashicorp/google + hashicorp/azurerm). (PR4)
- [ ] `services/compute/INTERSECTION.md` — intersection table with K8s NotImplemented rows documented. (PR4)

### 16.C — Compute instance lifecycle (3–4 PRs, after 16.B)

- [ ] Define `internal/compute/domain/instances.go` — `Instances` interface: RunInstances / DescribeInstances / StartInstances / StopInstances / TerminateInstances / RebootInstances / DescribeInstanceTypes.
- [ ] `services/compute/backends/inmem/` — extend with instance lifecycle.
- [ ] AWS EC2 frontend — extend with instance Action handlers (same listener/router as 16.B; new Action registrations).
- [ ] GCP Compute v1 frontend — instances.insert/delete/start/stop/reset/get/list/aggregatedList + machineTypes.list/get.
- [ ] Azure Compute frontend (`services/compute/spec/azure-compute.json`) — VirtualMachines + VirtualMachineSizes ARM handlers.
- [ ] K8s peer — extend: DescribeInstances → Node list/get; DescribeInstanceTypes → Node capacity; Run/Start/Stop/Terminate/Reboot → NotImplemented.
- [ ] Real backends: `services/compute/backends/{aws,gcp,azure}/` instance implementations.
- [ ] Full conformance matrix — SDK + CLI + Terraform × all frontends × all backends.
- [ ] Sockerless instance lane — add with `SOCKERLESS_COMPUTE_TLS_PORT` guard; `t.Skip` with clear message referencing sockerless #373/#374/#375 until they close.
- [ ] Cross-cloud Apply cell: e.g., `TestCrossCloudApply_Roundtrip_Compute_AWStoAzure` in `conformance/cross_cloud_apply_test.go`.

### 16.D — Load balancers (2–3 PRs, after 16.A; parallels 16.B)

- [ ] New `services/loadbalancer/` directory. Vendor `services/loadbalancer/spec/`: AWS `elastic-load-balancing-v2-2015-12-01.json` (awsQuery — no new codegen), GCP LB routes from Compute v1, Azure `azure-network.json` LB resources.
- [ ] Define `internal/loadbalancer/domain/` — `LoadBalancer` interface: CreateLoadBalancer / DeleteLoadBalancer / DescribeLoadBalancers / CreateTargetGroup / DeleteTargetGroup / RegisterTargets / DeregisterTargets / CreateListener / DeleteListener / DescribeListeners.
- [ ] `services/loadbalancer/backends/inmem/` — inmem LB backend.
- [ ] AWS ELBv2 frontend — awsQuery handler (reuses existing awsQuery lane; no new codegen).
- [ ] GCP Compute v1 frontend — forwarding rules + target pools + health checks.
- [ ] Azure Network frontend — loadBalancers ARM resource (same spec as 16.B azure-network.json).
- [ ] K8s peer — Service type:LoadBalancer (create/delete/list); Endpoints (RegisterTargets/DeregisterTargets).
- [ ] Real backends: `services/loadbalancer/backends/{aws,gcp,azure}/`.
- [ ] Full conformance matrix: SDK + CLI + Terraform × all frontends × all backends.
- [ ] Sockerless LB lane: create/describe/delete (no Firecracker dep) green immediately; RegisterTargets lane with `t.Skip` referencing #373/#374/#375.

## Upstream watch

Sockerless gaps blocking 16.C instance lane + 16.D RegisterTargets:

- [sockerless #373](https://github.com/e6qu/sockerless/issues/373) — `DetectFirecrackerCapabilities()` missing `/dev/kvm` check — cryptic timeout when KVM absent. **Blocks 16.C sockerless lane.**
- [sockerless #374](https://github.com/e6qu/sockerless/issues/374) — 3 GB rootfs per VM risks disk exhaustion on 14 GB runners. **Blocks 16.C sockerless lane.**
- [sockerless #375](https://github.com/e6qu/sockerless/issues/375) — kernel + rootfs downloaded fresh every run; no `actions/cache`; `ubuntu-latest` floating. **Blocks 16.C sockerless lane.**

Recent merges (no current shim dependency):

- [sockerless PR #361](https://github.com/e6qu/sockerless/pull/361) ✅ — DynamoDB `DeleteItem ReturnValues=ALL_OLD`.
- [sockerless PR #364](https://github.com/e6qu/sockerless/pull/364) ✅ — GCP/Azure security (nftables NIC filters) + load-balancer data planes.
- [sockerless PR #368](https://github.com/e6qu/sockerless/pull/368) ✅ — Azure Entra authorization-code flow (PKCE, ID tokens). No current shim dep.
- [sockerless PR #369](https://github.com/e6qu/sockerless/pull/369) ✅ — Azure SDK local portability (macOS Docker harness, Event Grid resolver). Build infra.
- [sockerless PR #370](https://github.com/e6qu/sockerless/pull/370) ✅ — Dockerfile build context fixes + `.dockerignore` + `SIM_RUNTIME=process` docs. Build infra.
- [sockerless PR #372](https://github.com/e6qu/sockerless/pull/372) ✅ — Firecracker VM lifecycle (EC2/GCE/Azure): real TAP NIC + kernel/rootfs boot + nftables/NSG wiring. Relevant when 16.C sockerless lane opens; blocked by #373–375 for CI stability.

## Standing rules

- File sockerless issues for any gap found during shim work; never paper over gaps in shim test code.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in [BUGS.md](BUGS.md) before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file + provenance; mirrors CI `codegen deterministic`.
- `make spec-freshness` — informational; weekly CI workflow surfaces upstream spec drift.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane (10 packages, all green).

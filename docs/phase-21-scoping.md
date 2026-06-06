# Phase 21 — L7 Load Balancers: Scoping

> Pre-implementation audit for layer-7 HTTP/HTTPS load balancers. Read
> [PLAN.md § Phase 21](../PLAN.md#phase-21--l7-load-balancers) for the premise
> and [AGENTS.md](../AGENTS.md) for the rules.

**Premise.** Phase 16.D (layer-4 TCP) established the load balancer domain and
the three cloud + K8s backends. Phase 21 promotes the Application LB shape into
the intersection: HTTPS termination, host/path-based routing rules, HTTP health
checks, and a backend pool per target group. The new rule N35 names exactly what
is portable.

## 0. Service Shape

Phase 21 extends the existing `services/loadbalancer/` service directory — it
does not create a new service. All new types and operations live alongside the
Phase 16.D L4 types.

```
internal/loadbalancer/
  domain/domain.go        ← extended: Rule, HealthCheck, HTTP/HTTPS protocols
  frontends/
    aws_elbv2/adapter.go  ← extended: CreateRule, ModifyListener, health checks
    gcp_lb/server.go      ← extended: urlMaps, targetHttpsProxies, global BEs
    azure_lb/server.go    ← extended: applicationGateways resource family

services/loadbalancer/
  codegen.json            ← add CreateRule/DeleteRule/DescribeRules/Modify*
  gen/aws_elbv2.gen.go    ← regenerated
  backends/inmem/inmem.go ← extended: Rule CRUD
  conformance/            ← new L7 SDK/CLI/TF tests per frontend
```

## 1. Source APIs

| Cloud | L7 LB type | Resource model | Spec |
|---|---|---|---|
| AWS | Application Load Balancer (ALB) | Decomposed: LB + TG + Listener + Rule | ELBv2 Smithy — already vendored |
| GCP | Global external HTTP(S) LB | Decomposed: BackendService + UrlMap + TargetHttpsProxy + GlobalForwardingRule + SslCertificate | Compute v1 Discovery — already vendored |
| Azure | Application Gateway | Compound ARM resource — pools, listeners, rules, probes inline | Azure OpenAPI — `microsoft.network` spec; `applicationGateways` resource |
| K8s | Ingress | `networking.k8s.io/v1 Ingress` + `IngressClass` | K8s API — dynamic client |

## 2. Intersection

### N35 — L7 routing intersection

The portable surface is:

| Capability | AWS ALB | GCP HTTP(S) LB | Azure App Gateway | K8s Ingress |
|---|---|---|---|---|
| L7 LB create/delete | `CreateLoadBalancer(type=application)` | UrlMap + TargetHttpsProxy + GlobalForwardingRule | `applicationGateways.createOrUpdate` | `Ingress` resource |
| HTTPS listener | `CreateListener(HTTPS, CertificateArn)` | `targetHttpsProxies(ssl_certificates)` | `httpListeners[protocol=Https, ssl_certificate]` | TLS via `spec.tls` |
| HTTP target group | `CreateTargetGroup(HTTP)` | `backendServices(global, HTTP)` | `backendAddressPools` + `backendHttpSettingsCollection` | `backend.service` |
| Health check | TG `HealthCheckPath + HealthCheckProtocol` | BS `healthChecks[requestPath]` | `probes[protocol+path+statusCodes]` | not standardized; NotImplemented |
| Path rules | `CreateRule(path-pattern/host-header)` | UrlMap `pathMatchers[paths → backend]` | `requestRoutingRules[pathBasedRouting]` | `spec.rules[host, paths]` |
| Forward action | `Rule.Actions[forward → TargetGroupArn]` | UrlMap default/path backend service | Routing rule → backend pool | path → service backend |

### Out of intersection (Phase 21)

- HTTP→HTTPS redirect rules (redirect action type)
- Weighted target groups / traffic shifting
- Session stickiness (LB cookie or app cookie)
- WebSocket upgrade handling
- WAF policies / ACLs
- gRPC routing
- mTLS
- L7 Logging / access logs
- Lambda / Cloud Run targets
- Geo-routing, latency-routing, rate limiting
- URL rewrite/redirect

## 3. Domain Extension Plan

The existing domain stays unchanged for L4. New types added on top:

```
Rule            — priority + conditions + action; scoped to a Listener
RuleCondition   — type (host-header | path-pattern) + values
RuleAction      — type (forward) + TargetGroupID
HealthCheck     — Protocol, Path, HTTPCodes, Port
```

`TargetGroup` gains `HealthCheck HealthCheck`.
`Listener` gains `CertificateIDs []string`.
`Protocol` gains `ProtocolHTTP`, `ProtocolHTTPS`.
`LoadBalancers` interface gains `CreateRule / GetRule / ListRules / DeleteRule`.

## 4. AWS ELBv2 Extension Plan

Codegen additions (restJson protocol stays unchanged, awsQuery):
- `CreateRule`, `DeleteRule`, `DescribeRules`, `ModifyRule`
- `ModifyListener` (for cert updates and protocol changes)
- `ModifyTargetGroup` (for health check path / protocol changes)

Adapter changes:
- `CreateLoadBalancer`: accept `type=application`; build ALB ARN.
- `CreateListener`: extract `Certificates[0].CertificateArn` for HTTPS; extract `DefaultActions[0]` for default target group.
- `CreateRule`: map `Conditions` (host-header/path-pattern) + `Actions` (forward) → domain `Rule`.
- `CreateTargetGroup`: pass `HealthCheckPath` / `HealthCheckProtocol` / `HealthCheckPort` / `Matcher.HttpCode` into `HealthCheck`.
- `DescribeRules`: list by listener ARN or rule ARN.

## 5. GCP HTTP(S) LB Extension Plan

New operations in `gcp_lb/server.go`:

| GCP resource | HTTP method | Maps to |
|---|---|---|
| `global/backendServices` | POST (insert) | `CreateTargetGroup(HTTP)` |
| `global/backendServices/{name}` | GET/DELETE | `GetTargetGroup` / `DeleteTargetGroup` |
| `urlMaps` | POST | `CreateRule` (with full path-matcher set) |
| `urlMaps/{name}` | GET/DELETE/PATCH | `GetRule` / `DeleteRule` / rule update |
| `targetHttpsProxies` | POST | `CreateListener(HTTPS, certIDs)` |
| `targetHttpsProxies/{name}` | GET/DELETE | `GetListener` / `DeleteListener` |
| `globalForwardingRules` | POST | associates listener with port 443 |
| `sslCertificates` | POST/GET/DELETE | opaque cert store (cert resource pass-through) |

**Key difference from L4**: GCP global resources live under
`/compute/v1/projects/{project}/global/…` not `…/regions/{region}/…`.

## 6. Azure Application Gateway Extension Plan

Unlike AWS and GCP's decomposed resource model, Azure Application Gateway is a
**compound ARM resource** — one `PUT` creates the entire gateway with inline
backend pools, listeners, routing rules, and probe config. This matches the
Phase 16.D Azure LB pattern.

New routes in `azure_lb/server.go` (or a new `azure_appgateway` sub-package):

```
PUT  .../applicationGateways/{name}   → CreateLoadBalancer(application) + all sub-resources
GET  .../applicationGateways/{name}   → GetLoadBalancer + assemble sub-resources
GET  .../applicationGateways          → ListLoadBalancers
DELETE .../applicationGateways/{name} → DeleteLoadBalancer + all sub-resources
```

The adapter assembles domain `LoadBalancer` + `TargetGroup`s + `Listener` + `Rule`s
from the single ARM body at create time, and re-assembles the ARM response from
domain state at get/list time.

## 7. K8s Ingress Extension Plan

`IngressClass` resource selects the controller (maps to load balancer type).
`Ingress` resource maps to:
- `LB.name` from `Ingress.name`
- `Rule` from each `spec.rules[host, paths[path, backend.service]]`
- `TargetGroup.name` from `backend.service.name`
- TLS: `spec.tls[hosts, secretName]` → `Listener.CertificateIDs` (opaque pass-through)

## 8. Sub-Phase Plan

| Track | Scope | Exit criteria |
|---|---|---|
| 21.A | Scoping (this doc) + N35 normalization rule + domain extension + inmem + AWS ALB frontend (codegen + adapter) | AWS SDK conformance green for ALB create/listener/rule/TG-health-check lifecycle |
| 21.B | GCP HTTP(S) LB extension (global backendServices + urlMaps + targetHttpsProxies + sslCertificates) | GCP SDK conformance green for full L7 LB lifecycle |
| 21.C | Azure Application Gateway + K8s Ingress + full CLI/TF conformance matrix | All 3 frontend × all 3 driver-type rows green |

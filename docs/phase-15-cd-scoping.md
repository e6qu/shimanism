# Phase 15.C + 15.D scoping

> Pre-implementation audit for two new shimmed services: **NoSQL key-value** (15.C) and **DNS** (15.D). Each section captures: per-cloud surface, intersection candidates, spec sources, K8s peer choice, auth, codegen lane, conformance plan, open questions. Implementation PRs follow this doc.

## 15.C — NoSQL key-value

### Mission

Add a new shimmed service for cross-cloud key-value workloads. Subset: `Get` / `Put` / `Delete` / `Scan` (and possibly `Query`) on a partition key. No secondary indexes, no transactions, no rich document operations — those are out of intersection for 15.C and would land in a later 15.E or Phase 16 expansion.

### Per-cloud surface

| Cloud | Service | Wire protocol | Auth | Spec source |
|---|---|---|---|---|
| AWS | DynamoDB | HTTP/JSON (X-Amz-Target) | SigV4 | `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models/dynamodb-2012-08-10.json` (Smithy) |
| GCP | Firestore Native mode | HTTP/REST + gRPC | Bearer (Google ID token) | `google.golang.org/api/firestore/v1` (REST) — chosen per the GCP REST-first rule in `AGENTS.md § Reuse over reinvention` |
| Azure | Cosmos DB Table API | HTTP/REST (Azure Storage Tables protocol — Cosmos DB exposes Table API for legacy storage-tables compatibility) | Shared Key | Azure REST API spec at `Azure/azure-rest-api-specs/specification/cosmos-db/data-plane/Microsoft.Tables/...` |
| K8s peer | etcd via the `shimakit` framework (or a thin `etcd`-backed driver) | gRPC | mTLS / token | none — peer driver is hand-written |

**Rationale for picks:**

- **Firestore Native mode (not Datastore mode).** Datastore mode is the legacy compatibility layer. Native mode is the current GCP offering for new workloads and has cleaner semantics. (`firestore.googleapis.com/v1/projects/{project}/databases/(default)/documents/...`).
- **Cosmos DB Table API (not Core SQL).** Tables API is the natural fit for a key-value subset; Core SQL is document-shaped and richer than the intersection allows. Cosmos's Tables API also has the closest wire-protocol alignment to AWS Storage Tables, which gives the shim an existing-style frontend to work from.
- **etcd as K8s peer.** Mature, distributed, key-value-native. Available as a CRD-backed operator (etcd-operator) or as a standalone deployment. Matches the "key + value blob + tags" intersection cleanly.

### Intersection (target operations)

| Operation | DynamoDB | Firestore Native | Cosmos DB Table | etcd | Status |
|---|---|---|---|---|---|
| Create table / collection / database | `CreateTable` | implicit (`/projects/.../databases/(default)`) | `CreateTable` | `MkDir`-equivalent (namespaced prefix) | 1 |
| Delete table | `DeleteTable` | document deletion (no table concept) | `DeleteTable` | recursive delete | 1 (Firestore quirk noted) |
| Put item | `PutItem` | `CreateDocument` / `PatchDocument` | `InsertEntity` / `MergeOrReplaceEntity` | `Put` | 1 |
| Get item | `GetItem` | `GetDocument` | `QueryEntity` (by RowKey + PartitionKey) | `Get` | 1 |
| Delete item | `DeleteItem` | `DeleteDocument` | `DeleteEntity` | `Delete` | 1 |
| List / Scan | `Scan` (capped) | `ListDocuments` | `QueryEntities` (no filter) | `Range` | 1 (caps differ) |
| Query by partition key | `Query` | `RunQuery` | `QueryEntities` filtered by PartitionKey | `Range` with prefix | 1 (semantics differ) |
| Tags / labels | `TagResource` / `UntagResource` | `Label` (database-level) | `SetServiceProperties` (limited) | none | 2 — feature unset on etcd / probably ◐ |
| Versioning / TTL | TTL via attribute | TTL via field | TTL via property | TTL via lease | 1 maybe (cap to common subset) |

**Out of intersection for 15.C:**

- Secondary indexes (DynamoDB GSI / Cosmos Table view / Firestore composite indexes).
- Transactions (`TransactWriteItems`, Firestore transactions, Cosmos `Begin`).
- Streams / change feed.
- Conditional writes (`ConditionExpression`).
- Provisioned throughput config.

These are eligible for a later 15.E expansion if the user-visible value justifies.

### Domain interface sketch

```go
// internal/nosql/domain/domain.go
type NoSQL interface {
    CreateTable(ctx context.Context, name string, opt CreateTableOptions) (CreateTableResult, error)
    DeleteTable(ctx context.Context, name string, force bool) error
    PutItem(ctx context.Context, table string, item Item) (PutItemResult, error)
    GetItem(ctx context.Context, table string, key Key) (Item, error)
    DeleteItem(ctx context.Context, table string, key Key) error
    Scan(ctx context.Context, table string, opt ScanOptions) (ScanResult, error)
    Query(ctx context.Context, table string, partitionKey string, opt QueryOptions) (QueryResult, error)
}

type Key struct {
    PartitionKey string
    SortKey      string // optional; empty means absent
}

type Item struct {
    Key
    Attributes map[string]any // marshalled per-cloud (DynamoDB AttributeValue, Firestore Value, Cosmos EdmType)
}
```

Carries over the existing `Tags`, `Description` (N4 encoding), version-identity (N2-style) and N3 tag-vs-label rules from the normalisations contract.

### Frontend / backend layout

```
internal/nosql/
    domain/
        domain.go
        errors.go
    frontends/
        aws_dynamodb/
        gcp_firestore/
        azure_cosmos_table/
services/nosql/
    spec/
        aws-dynamodb-spec.json     # vendored Smithy
        gcp-firestore-discovery.json
        azure-cosmos-tables.json   # vendored OpenAPI
    gen/
        aws/
        azure/
        gcp/
    backends/
        aws/
        gcp/
        azure/
        etcd/
        inmem/
    conformance/
        aws_terraform_test.go
        gcp_terraform_test.go
        azure_terraform_test.go
        cross_cloud_apply_test.go
        sockerless_test.go
```

### Codegen lane

- AWS DynamoDB: smithy → `cmd/codegen` (existing AWS Smithy generator handles JSON 1.0 protocol).
- GCP Firestore: Discovery doc → `cmd/gcp-codegen` (routing-only, REST types from `google.golang.org/api/firestore/v1`).
- Azure Cosmos Tables: OpenAPI → `cmd/azure-codegen` (existing Azure preprocessor handles Tables-style specs).

### Conformance

Per the standard 36-cell matrix: 3 frontends × 3 driver types (SDK / CLI / Terraform) × 4 backends (AWS / GCP / Azure / etcd).

- SDK row: `aws-sdk-go-v2/service/dynamodb`, `google.golang.org/api/firestore/v1`, `github.com/Azure/azure-sdk-for-go/sdk/data/aztables`.
- CLI row: `aws dynamodb`, `gcloud firestore`, `az cosmosdb table`.
- Terraform row: `hashicorp/aws` (`aws_dynamodb_table`), `hashicorp/google` (`google_firestore_database`), `hashicorp/azurerm` (`azurerm_cosmosdb_table`).

Sockerless coverage: existing AWS / GCP / Azure sims should cover DynamoDB / Firestore / Cosmos Tables. Audit pending — file at sockerless for any gap.

### Open questions

1. **Firestore "no table" semantics.** Firestore doesn't have tables — it has collections under a single database. The shim's `CreateTable` would map to "ensure the collection prefix exists" (no-op create that succeeds idempotently). Document as N17 (NoSQL table concept normalisation).
2. **Cosmos Tables vs Core SQL.** Cosmos Tables is the easier intersection match but has a smaller per-cloud user base than Core SQL. Confirm Tables is the right choice for "users migrating from DynamoDB / Firestore."
3. **Partition-key vs primary-key naming.** DynamoDB calls it "HashKey", Firestore uses document path, Cosmos uses (PartitionKey, RowKey) pair. Domain field naming choice belongs in N17.
4. **etcd peer: standalone or operator?** Phase 13 / 14 peer pattern was thin (in-tree shimakit framework for storage / secrets). For NoSQL we either embed etcd via `go.etcd.io/etcd` or run a separate etcd cluster as the K8s peer.

---

## 15.D — DNS

### Mission

Add a new shimmed service for cross-cloud DNS workloads. Public **and** private zones, standard record types (A, AAAA, CNAME, MX, TXT, NS, SOA, SRV).

### Per-cloud surface

| Cloud | Service | Wire protocol | Auth | Spec source |
|---|---|---|---|---|
| AWS | Route 53 (`route53`) + Route 53 Resolver (for private) | HTTP/REST + XML | SigV4 | `aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models/route-53-2013-04-01.json` (Smithy) |
| GCP | Cloud DNS (`dns.googleapis.com/v1`) | HTTP/REST | Bearer | `google.golang.org/api/dns/v1` (REST) |
| Azure | Azure DNS (`Microsoft.Network/dnszones`) + Azure Private DNS (`Microsoft.Network/privateDnsZones`) | HTTP/REST (ARM) | Bearer (Microsoft Entra) | Azure REST spec at `Azure/azure-rest-api-specs/specification/dns/...` + `.../privatedns/...` |
| K8s peer | CoreDNS via `external-dns` sync | DNS over UDP/TCP + CoreDNS plugin config | n/a (cluster-local) | none |

**Rationale for picks:**

- **Public + private zones in scope** per the user's earlier decision. Route 53 / Cloud DNS / Azure DNS all have explicit public/private distinction; the shim's domain carries `ZoneVisibility` as an enum.
- **CoreDNS as K8s peer.** Standard K8s DNS server with a rich plugin ecosystem. `external-dns` is the conventional bridge between K8s resources and DNS providers; shimanism uses CoreDNS directly for the cluster-local lookup path.

### Intersection (target operations)

| Operation | Route 53 | Cloud DNS | Azure DNS (public) | Azure Private DNS | CoreDNS | Status |
|---|---|---|---|---|---|---|
| Create zone | `CreateHostedZone` | `managedZones.create` | `Zones.CreateOrUpdate` | `PrivateZones.CreateOrUpdate` | zone-file mount | 1 |
| Delete zone | `DeleteHostedZone` | `managedZones.delete` | `Zones.Delete` | `PrivateZones.Delete` | zone-file unmount | 1 |
| List zones | `ListHostedZones` | `managedZones.list` | `Zones.List` | `PrivateZones.List` | enumerate mounts | 1 |
| Create record set | `ChangeResourceRecordSets` (CREATE batch) | `resourceRecordSets.create` | `RecordSets.CreateOrUpdate` | `RecordSets.CreateOrUpdate` (private) | zone-file entry | 1 |
| Update record set | `ChangeResourceRecordSets` (UPSERT) | `resourceRecordSets.patch` | `RecordSets.CreateOrUpdate` | same | edit zone-file | 1 |
| Delete record set | `ChangeResourceRecordSets` (DELETE) | `resourceRecordSets.delete` | `RecordSets.Delete` | same | remove from zone-file | 1 |
| List record sets | `ListResourceRecordSets` | `resourceRecordSets.list` | `RecordSets.ListByDNSZone` | `RecordSets.ListByDnsZone` | scan zone-file | 1 |
| **Record types in intersection:** A, AAAA, CNAME, MX, TXT, NS, SOA, SRV | all | all | all | all | all | 1 |

**Out of intersection for 15.D:**

- DNSSEC (signed records, KSK rotation).
- Route 53 alias records (AWS-specific, no cross-cloud equivalent).
- Geo / latency routing (vendor-specific routing policies).
- Health checks (Route 53 has them; Cloud DNS / Azure don't expose the same model).
- VPC associations for private zones (heavy networking integration; cross-cloud requires destination-cloud VPC concepts).

VPC associations are the most likely candidate for 15.E expansion if user demand surfaces.

### Domain interface sketch

```go
// internal/dns/domain/domain.go
type DNS interface {
    CreateZone(ctx context.Context, name string, opt CreateZoneOptions) (Zone, error)
    DeleteZone(ctx context.Context, name string, force bool) error
    GetZone(ctx context.Context, name string) (Zone, error)
    ListZones(ctx context.Context, opt ListZonesOptions) (ListZonesResult, error)

    PutRecordSet(ctx context.Context, zone string, rs RecordSet) error
    DeleteRecordSet(ctx context.Context, zone string, name string, rtype RecordType) error
    ListRecordSets(ctx context.Context, zone string, opt ListRecordSetsOptions) (ListRecordSetsResult, error)
}

type Zone struct {
    Name       string
    Visibility ZoneVisibility // Public | Private
    NameServers []string       // populated by destination cloud
    Tags       map[string]string
}

type RecordSet struct {
    Name    string       // FQDN within the zone
    Type    RecordType   // A | AAAA | CNAME | MX | TXT | NS | SOA | SRV
    TTL     int          // seconds
    Records []string     // record-type-specific encoding (e.g. "1.2.3.4" for A, "10 mail.example.com." for MX)
}
```

### Frontend / backend layout

```
internal/dns/
    domain/
    frontends/
        aws_route53/
        gcp_cloud_dns/
        azure_dns/          # handles both public + private via ZoneVisibility
services/dns/
    spec/
    gen/
    backends/
        aws/
        gcp/
        azure/
        coredns/
        inmem/
    conformance/
```

### Codegen lane

- Route 53: smithy → `cmd/codegen` (REST-XML protocol).
- Cloud DNS: Discovery doc → `cmd/gcp-codegen`.
- Azure DNS + Private DNS: OpenAPI → `cmd/azure-codegen` (one frontend dispatches on `ZoneVisibility`).

### Conformance

3 frontends × 3 driver types × 4 backends = 36 cells. Same matrix shape as existing services. Sockerless coverage:

- Route 53 — covered by sockerless's AWS sim (per sockerless#288's API Gateway / Route 53 client-surface coverage work).
- Cloud DNS — needs sockerless gap audit; file if missing.
- Azure DNS + Private DNS — needs sockerless gap audit; file if missing.

### Open questions

1. **`coredns` peer integration shape.** Does shimanism manage a CoreDNS instance directly (start / stop / reconfigure), or does it expose a "DNS protocol" shape that any CoreDNS instance can read? The latter is more K8s-native (CRDs + a controller that reconciles into CoreDNS's `Corefile`) but bigger scope. Smaller scope: shimanism runs a CoreDNS process with a file-based zone config that the shim updates.
2. **Azure DNS vs Private DNS as two backends or one.** Both speak the same ARM REST shape with different resource types. One backend with a `ZoneVisibility` dispatch is cleaner than two near-identical backends; the `ZoneVisibility` enum belongs in the domain.
3. **NS records for delegated subzones.** When the shim manages `example.com` and a user creates a subzone `dev.example.com` (delegated NS), the parent zone's NS record needs to point at the destination cloud's name servers. The destination cloud emits its name servers when the zone is created; the shim plumbs them through. Cross-cloud delegation is an edge case worth a normalisation rule (probably N18) when it lands.

---

## Ordering + dependencies

- **15.D first** (smaller scope, fewer open questions, sockerless coverage closer to ready). ~2-3 PRs.
- **15.C second** (bigger scope, more open questions, etcd peer choice + Firestore "no table" need decisions). ~3-4 PRs.

Both unblocked: no Phase 15.B residuals remaining; no external sockerless issues open.

Implementation PRs land per existing shimanism conventions (spec ingest → codegen → frontend → backend → conformance, one branch per sub-phase).

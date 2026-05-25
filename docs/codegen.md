# Codegen

shimanism generates server stubs from each cloud's published specs. Hand-written code is restricted to per-operation `translate.go` files (or per-frontend adapters that implement the generated `ServerInterface`). All three lanes — AWS Smithy, Azure OpenAPI v2, GCP Discovery — are wired through `make codegen` and produce deterministic output. See [`docs/codegen-pipelines.md`](../docs/codegen-pipelines.md) for the full architecture; this file is a high-level overview.

## Current state

| Service | AWS Smithy | Azure OpenAPI | GCP Discovery |
|---|---|---|---|
| storage | ✅ `services/storage/gen/` | ✅ `gen/azure/azure_blob.gen.go` (Blob data-plane, 1.1 MB) | ✅ `gen/gcp/gcp_gcs.gen.go` (82 routes) |
| secrets | ✅ `services/secrets/gen/` | ✅ `gen/azure/azure_keyvault.gen.go` | ✅ `gen/gcp/gcp_secretmanager.gen.go` (32 routes) |
| queue | ✅ | ✅ Service Bus (shared) | ✅ (46 routes) |
| pubsub | ✅ | ✅ Service Bus (shared) | ✅ (46 routes) |
| rdbms | ✅ | ✅ PostgreSQL FlexibleServers ARM | ✅ Cloud SQL (74 routes) |
| cache | ✅ | ✅ Redis ARM | ✅ Memorystore (45 routes) |
| functions | ✅ | ✅ ContainerApps ARM | ✅ Cloud Run (58 routes) |
| apigateway | ✅ | ✅ APIM (minimal) | ✅ (30 routes) |

8/8 AWS frontends ship fully migrated through `gen.HandlerWithOptions`. 1/8 Azure frontend (`azure_keyvault`) ships fully migrated; the other 7 keep hand-written dispatch with the gen inventory as the spec-drift contract — adapter migration is Phase 13 follow-on.

`make codegen` regenerates every spec-driven file across all three lanes. `make codegen-check` (and CI's `codegen deterministic` job) asserts the output is byte-identical to the committed copy.

## Toolchain

- `cmd/codegen/main.go` — AWS Smithy emitter. Handles all four protocols (REST-XML / awsJson1_0 / awsJson1_1 / awsQuery / restJson1) via per-protocol templates in `internal/codegen/emit/`.
- `cmd/azure-codegen/main.go` — Azure preprocessor (8 stages: inline external refs, x-ms-examples skip, x-ms-enum promotion with parameter/header gating, parameter/definition name dedup, ARM `allOf` flatten, x-ms-paths flatten, empty-AllOf normalize, deterministic walk) + `kin-openapi/openapi2conv.ToV3` + `oapi-codegen` as a library.
- `cmd/gcp-codegen/main.go` — Discovery JSON → routing-only Go. Reuses `google.golang.org/api/<svc>/v1` wire types per AGENTS.md #11. Emits `Routes []Route` + `Match()` / `MatchAll()` helpers.
- `cmd/inject-provenance/main.go` — writes a `_provenance` top-level key into each vendored spec JSON; idempotent; CI guard blocks merges without it.
- `scripts/fetch-{aws,azure,gcp}-*.sh` — resolve `<ref>` to a SHA against the upstream repo, download the spec, seed SOURCES.md, and run inject-provenance.

## Why codegen

Three reasons:

1. **Fidelity.** The shim must match the cloud's published API exactly — response shapes, error envelopes, HTTP status codes, async-operation semantics, path templates, query-parameter names, header names. Generating from the upstream spec eliminates the "is the field name right?" class of bug.
2. **Drift detection.** When the upstream cloud changes its spec, regeneration produces a diff. The translation table (per-operation `translate.go`) is the only thing to review by hand.
3. **Reuse over reinvention.** Per [PLAN.md § locked-in decisions](../PLAN.md#locked-in-decisions): the cloud's own spec is the source of truth. We never fork or maintain a parallel implementation.

## Spec inputs

Always vendored from upstream-canonical sources. Three formats, three publishers, three different transport mechanisms — every spec is reproducibly pinned in this repo with a commit SHA (or Discovery `revision`) recorded in `services/<svc>/spec/SOURCES.md` and stamped into the spec's own `_provenance` top-level key.

| Cloud | Spec format | Upstream source | How to refresh | Pinned by |
|---|---|---|---|---|
| AWS | [Smithy 2.0 JSON](https://smithy.io/2.0/) | [`aws/aws-sdk-go-v2/codegen/sdk-codegen/aws-models/<svc>.json`](https://github.com/aws/aws-sdk-go-v2/tree/main/codegen/sdk-codegen/aws-models) (Apache-2.0) | `scripts/fetch-aws-spec.sh <svc> services/<dir>` | commit SHA |
| Azure | Swagger 2.0 (preserved as v2, our preprocessor converts to v3 at codegen time) | [`Azure/azure-rest-api-specs/specification/.../<api>.json`](https://github.com/Azure/azure-rest-api-specs) (MIT) | `scripts/fetch-azure-spec.sh <upstream-path> services/<dir> <local-filename>` | commit SHA |
| GCP REST | [Google API Discovery JSON](https://developers.google.com/discovery/v1/reference/apis) | `https://<svc>.googleapis.com/$discovery/rest?version=v1` (Apache-2.0, live document) | `scripts/fetch-gcp-discovery.sh <svc>.googleapis.com services/<dir> <local-filename>` | Discovery `revision` string (e.g. `20260516`) |
| GCP gRPC | Protobuf | [`googleapis/googleapis`](https://github.com/googleapis/googleapis) | not yet wired into the shim — gRPC frontends are a separate phase | commit SHA |

Specs live under `services/<svc>/spec/`. They're vendored, not forked — when upstream releases a new revision, we vendor it again and review the diff. `cmd/inject-provenance` writes the `_provenance` block on fetch, and a CI test (`TestEveryVendoredSpecCarriesProvenance`) blocks merges where the key is missing or stale.

A concrete example of `SOURCES.md`, from `services/storage/spec/SOURCES.md`:

```text
| Local file                     | Upstream repo                | Upstream path                                                                           | License    | Pinned at                                | Fetched (UTC)         |
| aws-s3.smithy.json             | aws/aws-sdk-go-v2            | codegen/sdk-codegen/aws-models/s3.json                                                  | Apache-2.0 | 71f1511b45ced10d1e68f9e631dcb37019759e34 | 2026-05-18T17:38:39Z  |
| azure-blob.json                | Azure/azure-rest-api-specs   | specification/storage/data-plane/Microsoft.BlobStorage/stable/2026-04-06/blob.json      | MIT        | be46becafeb29aa993898709e35759d3643b2809 | 2026-05-22T12:00:00Z  |
| gcp-storage-discovery.json     | storage.googleapis.com       | $discovery/rest?version=v1 (live Discovery document)                                    | Apache-2.0 | revision 20260516                        | 2026-05-22T11:35:00Z  |
```

Each fetch script resolves `<ref>` to a concrete SHA via the GitHub API, downloads the file at that pinned revision, regenerates the SOURCES.md row, and re-runs `cmd/inject-provenance`. Discovery documents don't ship under git, so the GCP script captures the document's own `revision` field instead. Re-fetching is the only way to bump a pin — there is no in-place "edit + commit" path that wouldn't be caught by the determinism check.

## Wire types: reuse first

Prefer reusing the official Go SDK's wire-type structs over re-emitting equivalents:

| Cloud | Reusable wire-type package |
|---|---|
| AWS | `github.com/aws/aws-sdk-go-v2/service/<svc>/types` |
| GCP REST | `google.golang.org/api/<svc>/v1` (generated from Discovery) |
| GCP gRPC | The proto-generated structs in `cloud.google.com/go/<svc>` |
| Azure | The SDK's internal `generated/` package (the types `azblob` etc. use under the hood) |

Re-emit server-side types **only when SDK types fight server-side handling** — for example, client-only fields, pointer-heavy shapes that don't round-trip, or struct tags that target the SDK's middleware rather than direct (un)marshalling. When re-emitting, generate from the same spec the SDK is generated from, not from a copy.

## Server-side codegen

For each spec format, the most authoritative existing generator:

| Spec format | First choice | Fallback rationale |
|---|---|---|
| Smithy (AWS) | Custom emitter (no official Smithy → Go *server* generator exists; `smithy-go` is client-side) | n/a — custom is the only option |
| OpenAPI v3 (Azure) | `oapi-codegen` server-stubs | Custom emitter when stubs can't match the handler shape after reasonable adapter glue |
| Discovery / protobuf (GCP) | Reuse `google.golang.org/api` wire types directly; emit only the routing + dispatch layer | Custom emitter when the generated types are too SDK-coupled to import cleanly |

The custom emitter lives in `internal/codegen/`. Each service has a `services/<svc>/codegen.json` manifest that tells the emitter which operations to generate stubs for. Generated stubs land in `services/<svc>/gen/`.

## Auth verification

Use the cloud's official signer/verifier libraries — **never roll a SigV4 / OAuth2 / SharedKey implementation**:

| Cloud | Signer / verifier |
|---|---|
| AWS | `aws-sdk-go-v2/aws/signer/v4` |
| GCP | `golang.org/x/oauth2` |
| Azure | The signer in `azure-sdk-for-go/sdk/azcore/auth` and the SharedKey verifier exposed by the storage SDK |

## Validation

The cloud's spec carries field-level constraints (string lengths, enum sets, pattern regexes). Honor them at the wire-decode boundary so an invalid request fails with the **source cloud's own error vocabulary**, not a generic 500. When the spec generator emits validation (Smithy + OpenAPI do), wire it in.

## Regenerating

```sh
make codegen
```

Today this only regenerates the storage stubs (the emitter walks `services/storage/codegen.json`). As other services gain spec-driven codegen, the `make` target will fan out. The diff is what reviewers look at — generated stubs vs hand-written `translate.go` should be cleanly separable.

When upstream changes a spec:

1. Update the vendored spec under `services/<svc>/spec/`.
2. Run `make codegen`.
3. Review the diff: are there new operations? Renamed fields? Deprecated paths?
4. Update the per-operation `translate.go` files if needed.
5. Run conformance.

Stale generated code (where the spec moved and the gen didn't follow) is a bug — see [BUGS.md § class-of-bug rules](../BUGS.md#class-of-bug-rules-carried-forward).

## When *not* to reuse

Reuse is a tool, not a contract. If reusing a piece of SDK or generator output forces the shim to lie (synthetic responses, swallowed errors, fabricated success), drop the reuse and emit our own honest implementation. The fidelity rule beats the reuse rule.

## Hands-on tour: three formats, three lanes, one mental model

The most useful thing about this project to learn first is that all three lanes — Smithy / Swagger / Discovery — converge on the **same shape** at the adapter boundary: a Go method that takes a generated request struct (or a `(*http.Request, params...)` triple) and returns a generated response struct, with the wire decode and encode owned by codegen. What changes across lanes is everything *underneath* that boundary.

This section walks one operation end-to-end through each of the three lanes. Read it once and the rest of `services/<svc>/gen/` makes sense without further explanation. Every code snippet below is copied from the actual repo; line numbers omitted but file paths are exact, so you can `grep` them.

### Lane 1 — AWS Smithy: `HeadBucket`

**Spec.** `services/storage/spec/aws-s3.smithy.json` (pinned at `71f1511b…`, Apache-2.0). Smithy describes an operation as an `input` shape, an `output` shape, an `errors` list, and a set of `traits` that carry the HTTP binding:

```jsonc
"com.amazonaws.s3#HeadBucket": {
  "type": "operation",
  "input":  { "target": "com.amazonaws.s3#HeadBucketRequest"  },
  "output": { "target": "com.amazonaws.s3#HeadBucketOutput" },
  "errors": [ { "target": "com.amazonaws.s3#NotFound" } ],
  "traits":  { "smithy.api#http": { "method": "HEAD", "uri": "/{Bucket}", "code": 200 } }
}
```

The input shape says which Go fields come from URI labels vs headers vs query vs body. Each member carries a binding trait:

```jsonc
"com.amazonaws.s3#HeadBucketRequest": {
  "type": "structure",
  "members": {
    "Bucket":              { "target": "com.amazonaws.s3#BucketName",
                             "traits": { "smithy.api#httpLabel": {}, "smithy.api#required": {} } },
    "ExpectedBucketOwner": { "target": "com.amazonaws.s3#AccountId",
                             "traits": { "smithy.api#httpHeader": "x-amz-expected-bucket-owner" } }
  }
}
```

**Manifest.** `services/storage/codegen.json` lists which Smithy operations to emit. The cross-cloud intersection is hand-curated here, so opting an operation in is a one-line edit:

```jsonc
{ "spec": "services/storage/spec/aws-s3.smithy.json",
  "package": "gen",
  "out":     "services/storage/gen/aws_s3.gen.go",
  "operations": [ "ListBuckets", "CreateBucket", "HeadBucket", "ListObjectsV2", … ] }
```

**Emit.** `make codegen` runs `cmd/codegen`, which dispatches on the AWS service's protocol trait (S3 = `aws.protocols#restXml`) and walks per-protocol templates under `internal/codegen/emit/`. The generated file `services/storage/gen/aws_s3.gen.go` carries the spec's commit SHA in its header:

```go
// Code generated by cmd/codegen from services/storage/spec/aws-s3.smithy.json.
// Upstream commit: 71f1511b45ced10d1e68f9e631dcb37019759e34.
// DO NOT EDIT.
```

…and contains, among many other things, this for `HeadBucket`:

```go
// HeadBucketRequest is a generated Smithy structure.
type HeadBucketRequest struct {
    Bucket              string  // bound to label=Bucket
    ExpectedBucketOwner *string // bound to header=x-amz-expected-bucket-owner
}

// HeadBucketOutput is a generated Smithy structure.
type HeadBucketOutput struct {
    AccessPointAlias   *bool         // bound to header=x-amz-access-point-alias
    BucketArn          *string       // bound to header=x-amz-bucket-arn
    BucketLocationName *string       // bound to header=x-amz-bucket-location-name
    BucketLocationType *LocationType // bound to header=x-amz-bucket-location-type
    BucketRegion       *string       // bound to header=x-amz-bucket-region
}

// HeadBucketBackend serves the HeadBucket operation.
type HeadBucketBackend interface {
    HeadBucket(ctx context.Context, in *HeadBucketRequest) (*HeadBucketOutput, error)
}

const HeadBucketURITemplate = "/{Bucket}"
const HeadBucketMethod      = "HEAD"

// HeadBucketHandler decodes a HeadBucket request, dispatches to the backend, and encodes the
// response per AWS REST-XML semantics.
func HeadBucketHandler(b HeadBucketBackend) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        in := &HeadBucketRequest{}
        labels, ok := restxml.MatchURI(r.URL.Path, HeadBucketURITemplate)
        if !ok { restxml.WriteError(w, 400, "InvalidURI", "path does not match"); return }
        if v, ok := labels["Bucket"]; ok { in.Bucket = v }
        if v := r.Header.Get("x-amz-expected-bucket-owner"); v != "" { s := v; in.ExpectedBucketOwner = &s }
        out, err := b.HeadBucket(r.Context(), in)
        if err != nil { restxml.WriteBackendError(w, err); return }
        if out.AccessPointAlias  != nil { w.Header().Set("x-amz-access-point-alias",  strconv.FormatBool(*out.AccessPointAlias)) }
        if out.BucketArn         != nil { w.Header().Set("x-amz-bucket-arn",          *out.BucketArn) }
        if out.BucketLocationName != nil { w.Header().Set("x-amz-bucket-location-name", *out.BucketLocationName) }
        if out.BucketLocationType != nil { w.Header().Set("x-amz-bucket-location-type", string(*out.BucketLocationType)) }
        if out.BucketRegion      != nil { w.Header().Set("x-amz-bucket-region",       *out.BucketRegion) }
    })
}
```

Every per-binding line (`if v, ok := labels["Bucket"]…`, `w.Header().Set("x-amz-bucket-arn"…)`) is generated directly from the trait on the corresponding Smithy member. The 16 individual `HeadBucket*` operations in S3 produce 16 nearly-identical-but-trait-driven blocks; this is why the emitter is custom — no off-the-shelf tool turns `smithy.api#httpHeader: "x-amz-..."` traits into Go statements that set those headers.

**Adapt.** A hand-written adapter under `internal/storage/frontends/aws_s3/` implements the composed `gen.AmazonS3Backend` interface and forwards each method to the neutral domain:

```go
func (a *Adapter) HeadBucket(ctx context.Context, in *gen.HeadBucketRequest) (*gen.HeadBucketOutput, error) {
    if _, err := a.s.HeadBucket(ctx, in.Bucket); err != nil { return nil, awsErrFromDomain(err) }
    return &gen.HeadBucketOutput{ BucketRegion: aws.String("us-east-1") /* … */ }, nil
}
```

This is the **only** per-frontend code; the volume of it is bounded by the number of intersection operations, not by the size of S3's wire surface.

### Lane 2 — Azure OpenAPI (Swagger 2.0): `SetSecret`

**Spec.** `services/secrets/spec/azure-keyvault-secrets.json` (pinned at `9473ef10…`, MIT). Azure ships its REST surface as Swagger 2.0 with a constellation of `x-ms-*` extensions. The path entry for `PUT /secrets/{secret-name}` looks like:

```jsonc
"/secrets/{secret-name}": {
  "put": {
    "operationId": "SetSecret",
    "parameters": [
      { "$ref": "#/parameters/Azure.Core.Foundations.ApiVersionParameter" },
      { "name": "secret-name", "in": "path", "required": true, "type": "string",
        "x-ms-client-name": "secretName" },
      { "name": "parameters", "in": "body", "required": true,
        "schema": { "$ref": "#/definitions/SecretSetParameters" } }
    ],
    "responses": {
      "200": { "schema": { "$ref": "#/definitions/SecretBundle" } },
      "default": { "schema": { "$ref": "#/definitions/KeyVaultError" } }
    }
  }
}
```

…and the body type:

```jsonc
"SecretSetParameters": {
  "type": "object",
  "properties": {
    "value":       { "type": "string" },
    "tags":        { "type": "object", "additionalProperties": { "type": "string" } },
    "contentType": { "type": "string" },
    "attributes":  { "$ref": "#/definitions/SecretAttributes" }
  },
  "required": [ "value" ]
}
```

**Pipeline.** `cmd/azure-codegen` cannot hand this straight to `oapi-codegen` — Swagger 2.0 ARM specs use `x-ms-paths`, external `$ref`s into the common-types repo, `x-ms-enum` inline-vs-top-level collisions, and 1-element `allOf` patterns that all break the v2→v3 converter or the generator. The preprocessor runs an 8-stage normalizer first (full mechanics in [`codegen-pipelines.md`](codegen-pipelines.md)), then converts to OpenAPI v3 via `kin-openapi/openapi2conv.ToV3`, then calls `oapi-codegen` as a library with `Models: true` + `StdHTTPServer: true`.

**Emit.** The generated `services/secrets/gen/azure/azure_keyvault.gen.go` contains:

```go
// SecretSetParameters defines model for SecretSetParameters.
type SecretSetParameters struct {
    Attributes  *SecretAttributes  `json:"attributes,omitempty"`
    ContentType *string            `json:"contentType,omitempty"`
    Tags        *map[string]string `json:"tags,omitempty"`
    Value       string             `json:"value"`
}

// SetSecretParams defines parameters for SetSecret.
type SetSecretParams struct {
    ApiVersion AzureCoreFoundationsApiVersionParameter `form:"api-version" json:"api-version"`
}

// ServerInterface is the std-net-http server interface oapi-codegen emits.
type ServerInterface interface {
    SetSecret(w http.ResponseWriter, r *http.Request, secretName string, params SetSecretParams)
    // … one per operation
}
```

…plus, at the bottom of the file, a `HandlerWithOptions(si ServerInterface, opts StdHTTPServerOptions) http.Handler` that hangs each operation off `net/http`'s 1.22+ ServeMux pattern:

```go
m.HandleFunc(http.MethodPut+" "+options.BaseURL+"/secrets/{secret_name}", wrapper.SetSecret)
```

**Adapt.** `internal/secrets/frontends/azure_keyvault/server.go` implements `gen.ServerInterface` by decoding the body and calling the domain:

```go
func (srv *Server) SetSecret(w http.ResponseWriter, r *http.Request, name string, _ gen.SetSecretParams) {
    var body gen.SecretSetParameters
    if !decodeJSON(w, r, &body) { return }
    val := []byte(body.Value)
    createRes, err := srv.s.CreateSecret(r.Context(), name, domain.CreateSecretOptions{
        Tags:         derefTags(body.Tags),
        InitialValue: val,
    })
    if err != nil {
        var de *domain.Error
        if errors.As(err, &de) && de.Kind == domain.KindSecretAlreadyExists {
            putRes, perr := srv.s.PutSecretValue(r.Context(), name, val)
            if perr != nil { mapDomainError(w, perr); return }
            writeSecretBundle(w, http.StatusOK, name, val, putRes.Version, time.Now().UTC(), tags, "", r)
            return
        }
        mapDomainError(w, err); return
    }
    writeSecretBundle(w, http.StatusOK, name, val, createRes.Version, time.Now().UTC(), tags, "", r)
}
```

The adapter wires the same `gen.SecretSetParameters` struct shape that the spec said, with the same JSON tags, encoded the same way the SDK expects.

### Lane 3 — GCP Discovery: `storage.buckets.list`

**Spec.** Discovery documents are live JSON, fetched from `https://storage.googleapis.com/$discovery/rest?version=v1` and vendored at `services/storage/spec/gcp-storage-discovery.json` (revision `20260516`, Apache-2.0). Each operation is named under the service's resource tree:

```jsonc
{
  "rootUrl":     "https://storage.googleapis.com/",
  "servicePath": "storage/v1/",
  "revision":    "20260516",
  "resources": {
    "buckets": {
      "methods": {
        "list": {
          "id":          "storage.buckets.list",
          "path":        "b",
          "httpMethod":  "GET",
          "parameters": {
            "project":    { "type": "string",  "required": true, "location": "query" },
            "prefix":     { "type": "string",  "location": "query" },
            "maxResults": { "type": "integer", "location": "query", "default": "1000" },
            "pageToken":  { "type": "string",  "location": "query" }
          },
          "response": { "$ref": "Buckets" }
        }
      }
    }
  }
}
```

**Emit.** Why isn't this lane a custom server generator like the AWS lane? Because Google already publishes one — `google.golang.org/api/storage/v1` is generated from this same Discovery document and provides perfectly good Go structs for every wire type. Re-generating those structs ourselves would violate the reuse-over-reinvention rule (AGENTS.md #11), and they'd drift from the SDK we already depend on.

So `cmd/gcp-codegen` emits **only** the routing inventory. The output `services/storage/gen/gcp/gcp_gcs.gen.go` is one slice of `Route` quintuples:

```go
// Code generated by cmd/gcp-codegen from gcp-storage-discovery.json.
// Discovery revision: 20260516.
// Base URL: https://storage.googleapis.com/storage/v1/.
// DO NOT EDIT.

package gcp

type Route struct {
    ID         string
    HTTPMethod string
    URIPattern string
    Vars       []string
    Pattern    *regexp.Regexp
}

const BasePath = "/storage/v1"

var Routes = []Route{
    { ID: "storage.buckets.list", HTTPMethod: "GET", URIPattern: "b",
      Vars: nil, Pattern: regexp.MustCompile("^/?/storage/v1/b$") },
    { ID: "storage.buckets.get",  HTTPMethod: "GET", URIPattern: "b/{bucket}",
      Vars: []string{"bucket"}, Pattern: regexp.MustCompile("^/?/storage/v1/b/([^/]+)$") },
    // … 82 routes total for storage; 593 across all 8 services
}
```

Discovery URI templates are translated literally: `{var}` → `([^/]+)`, `{+var}` → `(.+)`, anchored against `BasePath + path`. The slice is sorted by `(HTTPMethod, URIPattern, ID)` so the diff stays stable when Google ships a Discovery revision.

**Adapt.** The GCS frontend at `internal/storage/frontends/gcs/` walks `gen.Routes` for dispatch and uses `google.golang.org/api/storage/v1` types directly for marshalling. Pseudocode:

```go
import storagev1 "google.golang.org/api/storage/v1"

route, params, ok := gen.Match(r.Method, r.URL.Path)
if !ok { writeGCSError(w, 404, "notFound"); return }

switch route.ID {
case "storage.buckets.list":
    res, err := s.ListBuckets(r.Context(), domain.ListBucketsOptions{
        Project: r.URL.Query().Get("project"),
        Prefix:  r.URL.Query().Get("prefix"),
    })
    if err != nil { mapDomainError(w, err); return }
    out := &storagev1.Buckets{ Items: toStoragev1Buckets(res) }
    writeJSON(w, http.StatusOK, out)
}
```

The wire type `*storagev1.Buckets` is the **same struct** the official Google SDK marshals — that's the reuse payoff. The shim's GCS frontend is wire-fidelity-correct by construction whenever a client uses the SDK, because both ends share the marshaller.

### What stays the same across all three lanes

Each lane writes very different Go code, but at the adapter boundary they all look the same: a method that takes a request type derived from the spec and returns a response type derived from the spec, with the wire-level coding owned by `gen/`. The constants that change:

| What | AWS lane | Azure lane | GCP lane |
|---|---|---|---|
| Generator | custom emitter (`cmd/codegen`) | `oapi-codegen` after 8-stage preprocess (`cmd/azure-codegen`) | routing-only emitter (`cmd/gcp-codegen`) |
| Wire types | re-emitted from Smithy | emitted by oapi-codegen | **reused** from `google.golang.org/api/<svc>/v1` |
| Routing | per-protocol `internal/restxml` / `internal/awsjson` / `internal/awsquery` `Router` | `net/http` 1.22+ ServeMux patterns from oapi-codegen | walk `gen.Routes` + regex match |
| Adapter shape | `gen.<Op>Backend` method per op | `gen.ServerInterface` method per op | `switch route.ID` dispatcher |
| Pinning | git commit SHA | git commit SHA | Discovery `revision` string |

What stays constant:

- **The spec is the source of truth.** Generated files carry the upstream SHA in their header. Stale gen = CI failure.
- **The adapter is the *only* place handwritten per-frontend code lives.** Its size is bounded by the number of intersection operations, not by the size of the cloud's published surface.
- **The neutral domain interface** (`internal/<svc>/domain/domain.go`) is the same regardless of lane. The S3 adapter, the Azure Key Vault adapter, and the GCS adapter all call into `domain.Storage` / `domain.Secrets` / `domain.Storage` respectively — the lane affects only what's *above* the domain, never what's *below*.
- **Backends are oblivious to lanes.** `services/storage/backends/gcs/translate.go` doesn't care whether its caller came from the AWS S3 frontend, the Azure Blob frontend, or the GCS frontend. It implements `domain.Storage` and the rest is wiring.

This is what makes three completely different spec formats produce code that composes: the lanes converge at the domain interface, and the domain interface is the cross-cloud intersection. Anything that won't fit the intersection isn't in the domain, the spec-driven handler returns the source cloud's own *operation-not-supported* envelope, and the project's fidelity rule is preserved end-to-end.

### How to make a change

Three common scenarios, each with a concrete one-line entry point:

1. **Upstream cloud shipped a new spec revision.** Re-fetch:
   ```sh
   scripts/fetch-aws-spec.sh s3 services/storage              # AWS lane
   scripts/fetch-azure-spec.sh specification/.../blob.json \
                                services/storage azure-blob.json   # Azure lane
   scripts/fetch-gcp-discovery.sh storage.googleapis.com \
                                services/storage gcp-storage-discovery.json  # GCP lane
   ```
   Then `make codegen`. Review the diff under `services/<svc>/gen/`. The translation table (adapter + backend) is the only thing that ever needs human attention; everything in `gen/` is regenerated for you.

2. **Want to add a new operation to the intersection.** Edit `services/<svc>/codegen.json` (AWS) or the relevant `*-codegen.json` to include the operation, run `make codegen`, implement the new method on the adapter, implement the matching domain interface change + backend translation. The whole loop is `make codegen-check && make test`.

3. **Spotted a wire-fidelity bug** (response shape mismatch, wrong XML element name, etc.). It's almost always at the emitter. Fix the emitter in `cmd/codegen` / `cmd/azure-codegen` / `cmd/gcp-codegen` (or the per-protocol templates under `internal/codegen/emit/`), regenerate, ship one PR that updates the emitter + the regenerated files. One emitter fix can correct hundreds of sites at once — see [issue #32](https://github.com/e6qu/shimanism/issues/32) for an example covering 45 sites in one diff.

### Where the spec→code rule earns its keep

The fidelity rule (response shapes match, error envelopes match, element names match) is enforced by the spec being the source of truth. When a wire-level mismatch escapes — e.g. the `@xmlFlattened` member-name vs inner-target-name ambiguity surfaced by [issue #32](https://github.com/e6qu/shimanism/issues/32) — the fix is at the emitter, regen everything, and one diff covers every site at once. The translation tables ride above this contract; they never duplicate it.

## Cross-link

- [PLAN.md § locked-in decisions](../PLAN.md#locked-in-decisions) — why codegen is the rule, not the exception.
- [AGENTS.md § spec is the source of truth](../AGENTS.md#spec-is-the-source-of-truth) — the hard rule for agents.
- [docs/development.md § adding a new operation](development.md#adding-a-new-operation) — where codegen fits in the recipe.
- [docs/testing.md](testing.md) — the conformance lanes that verify each emitted stub actually round-trips.

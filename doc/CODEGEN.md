# Codegen pipelines

`make codegen` drives three independent spec-driven pipelines, one
per cloud. Each lives under `cmd/<name>-codegen` and reads its
per-service manifest. Outputs land under `services/<svc>/gen/`.

## AWS Smithy (`cmd/codegen`)

| Spec format | AWS Smithy 2.0 JSON (model files vendored from `aws/aws-sdk-go-v2`) |
|---|---|
| Manifest | `services/<svc>/codegen.json` (spec + package + out + operations) |
| Emitter | `internal/codegen/emit/` (REST-XML, awsJson1_0, awsJson1_1, awsQuery, restJson1 templates) |
| Output | `services/<svc>/gen/aws_<svc>.gen.go` |
| Wire types | re-emitted per service from the Smithy model |
| Routing | per-protocol Router (`internal/restxml.Router`, `internal/awsjson.Router`, `internal/awsquery.Router`) |

Phase 11 closed the AWS lane: 8/8 services spec-driven; 3853 LOC of
hand-written wire deleted.

## Azure OpenAPI (`cmd/azure-codegen`)

| Spec format | Swagger 2.0 (data-plane + ARM specs vendored from `Azure/azure-rest-api-specs`) |
|---|---|
| Manifest | `services/<svc>/azure-codegen.json` |
| Toolchain | spec → 6-stage preprocessor → `kin-openapi/openapi2conv.ToV3` → `oapi-codegen` (as a library) |
| Output | `services/<svc>/gen/azure/azure_<spec>.gen.go`, package `azure` |
| Wire types | emitted per service by `oapi-codegen` (types + `std-net-http` ServerInterface) |

The preprocessor stages, in order:

1. **External-ref inliner** (`inlineExternalRefs`). Merges `definitions`/`parameters` from Azure's common-types files (vendored under `services/common-types/resource-management/v<N>/`) and from sibling spec files (`./<file>.json` from the spec's own directory) into the main doc. Rewrites every external `$ref` to a local pointer.
2. **`x-ms-examples` skip** (in `classifyRef`). Refs under `./examples/<file>` are metadata; the inliner doesn't follow them.
3. **`x-ms-enum` inline→`$ref`** (`promoteXMsEnumName`). When an inline schema's `x-ms-enum.name` matches a top-level definition declaring the same `x-ms-enum.name`, the inline gets `$ref`'d to the top-level. The walker tracks a "non-schema depth": inside a `parameters` or `headers` container, inline-enum promotion is suppressed (the converted v3 doc would reject parameter/header refs that point at schemas). A `schema` or `items` sub-key inside a parameter/header resets the depth so body-parameter schemas still get the inline→ref rewrite.
4. **Parameter/definition name dedup** (`dedupeParameterDefNameCollisions`). When `parameters.<N>` and `definitions.<N>` both exist, stamps `x-go-name: <N>Parameter` on the parameter so oapi-codegen doesn't emit two `type <N>` declarations. Azure Blob ships such a collision (string-enum `LeaseDuration` schema vs integer header parameter).
5. **ARM `allOf` inliner** (`flattenARMAllOf`). Azure ARM resource definitions use `{ allOf: [{$ref: TrackedResource}], properties: {own props} }`. oapi-codegen sees the 1-element allOf and emits `type X = TrackedResource` — a Go type alias that discards the schema's own properties. The preprocessor inlines each allOf $ref's properties + required into the local definition (local properties win on key collision; iterate until no definition changes to handle inheritance chains like `X → allOf [Y]; Y → allOf [Z]`). Result: ContainerApp / RedisResource / Server (PostgreSQL FlexibleServer) emit as proper Go structs with their own field sets.
6. **`x-ms-paths` flatten** (`flattenXMSPaths`). Azure data-plane specs use `x-ms-paths` to disambiguate same-URL operations by query parameter; OpenAPI v2/v3 don't model that, so we move the entries into `paths` with the same key.
7. **Empty-`AllOf` normalize** (`normalizeAllOf`). `kin-openapi`'s v2→v3 converter attaches empty `AllOf: []` to scalar enum schemas; oapi-codegen panics on `allOf[0]`. Nil out the empty slice everywhere.
8. **Deterministic walk** (`sortedKeys`). Every map walk uses sorted keys so multi-version common-types merges produce byte-identical output across runs.

After preprocessing, `oapi-codegen` runs with `Models: true` + `StdHTTPServer: true`.

Services covered today: secrets / queue / pubsub (shared) / cache / apigateway / functions / rdbms / storage (Blob). Storage was the final unlock — its spec hits all four oddities at once: external $ref shapes, 60 `x-ms-paths` entries, parameters whose inline `x-ms-enum` collides with definition schemas (forced the `schema`/`items`-reset depth tracker in `promoteXMsEnumName`), and a top-level parameter sharing its name with a definition (handled by `dedupeParameterDefNameCollisions`, which stamps `x-go-name: <N>Parameter` on the colliding parameter).

## GCP Discovery (`cmd/gcp-codegen`)

| Spec format | Google API Discovery JSON (live document from `<svc>.googleapis.com/$discovery/rest`) |
|---|---|
| Manifest | `services/<svc>/gcp-codegen.json` |
| Output | `services/<svc>/gen/gcp/gcp_<svc>.gen.go`, package `gcp` |
| Wire types | reused from `google.golang.org/api/<svc>/v1` (per AGENTS.md decision #11); the emitter is routing-only |

Output is a `Routes []Route` slice of `(HTTPMethod, URIPattern, OperationID, Vars, Pattern)` quintuples sorted by `(HTTPMethod, URIPattern, ID)` for stable diffs. Each Route's `Pattern` is a pre-compiled `*regexp.Regexp` derived from the Discovery URI template — `{var}` → `([^/]+)`, `{+var}` → `(.+)` — and anchored against `BasePath + path`. A `gen.Match(method, path) (*Route, params, ok)` helper walks Routes in declaration order and returns the first match with extracted variables. Frontends that want a different match precedence iterate `Routes` themselves.

First consumers: `services/<svc>/conformance/gcp_routes_test.go` — assert the inventory is non-empty + sorted + covers each service's cross-cloud-intersection op IDs. A spec drift (rename / removal / addition upstream) surfaces as a test failure on the next regeneration.

Services covered today: all 8 (storage / secrets / queue / pubsub / rdbms / cache / functions / apigateway). 593 routes total.

## Adding a new service

1. Vendor the spec under `services/<svc>/spec/` via the matching `scripts/fetch-*.sh` (AWS Smithy / Azure REST / GCP Discovery). Each script seeds SOURCES.md and runs `cmd/inject-provenance` so the spec's `_provenance` top-level key is current.
2. Write a `<lane>-codegen.json` manifest pointing at the spec.
3. Run `make codegen` — output lands under `services/<svc>/gen/<lane>/`.
4. Write the per-service adapter that implements the generated `ServerInterface` (Azure) or compiles `gen.Routes` into dispatch (GCP).
5. Run `make codegen-check` locally to confirm the output is deterministic before pushing.

## Vendored-spec provenance

Every JSON file under `services/*/spec/` and `services/common-types/` carries a `_provenance` top-level key as the first field. JSON has no comment syntax; the field is the closest analogue and the codegen tools tolerate unknown top-level keys.

```json
{
  "_provenance": {
    "upstream_repo": "Azure/azure-rest-api-specs",
    "upstream_path": "specification/.../blob.json",
    "upstream_license": "MIT",
    "pinned_at": "<commit-sha-or-revision>",
    "fetched_utc": "2026-05-22T12:00:00Z",
    "note": "..."
  },
  ...rest of the spec verbatim...
}
```

`SOURCES.md` is the authoritative store; `_provenance` is a derived projection so reviewers see the origin when they open the spec file. `make inject-provenance` re-syncs all specs from their SOURCES.md after a manual table edit; `cmd/inject-provenance` is idempotent and preserves source-file key ordering. The `TestEveryVendoredSpecCarriesProvenance` test in `cmd/inject-provenance/` blocks merges where a spec slipped in without the key.

## Determinism guarantees

- `make codegen-check` runs `make codegen` and `git diff --exit-code -- services`. CI's `codegen deterministic` job runs the same.
- Every map walk in the emitters uses sorted keys.
- Discovery routes are sorted by `(HTTPMethod, URIPattern, ID)` at emit time.
- The vendored spec's commit SHA appears in each `.gen.go` file's header so a spec bump that doesn't regenerate the gen file fails the determinism check.

## Where pipelines stop

The codegen pipelines own:
- Wire-type definitions (AWS Smithy; Azure oapi-codegen).
- Route inventories (GCP Discovery).
- Per-protocol Router runtime helpers (AWS).
- `ServerInterface` shape (Azure).

They do **not** own:
- Per-operation translation between source-cloud request shape and the shim's neutral domain layer — that's hand-written in `translate.go` (or per-service adapter `.go` files).
- Cross-cloud intersection definition — that lives in `services/<svc>/OPERATIONS.md`.
- Dispatch wiring — each frontend chooses how to compose the generated bits with its hand-written translation layer.

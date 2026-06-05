# Phase 18 — Container Registry: Scoping

> Pre-implementation audit for the container-registry service family. Mirrors
> [`docs/phase-16-scoping.md`](phase-16-scoping.md). Read [PLAN.md § Phase 18](../PLAN.md#phase-18--container-registry)
> for the premise and [AGENTS.md](../AGENTS.md) for the rules.

**Premise.** All three clouds speak the **OCI Distribution Spec v1** for image
push/pull; a per-cloud **control plane** (create/delete repository, list images,
delete tags) sits on top. No compute dependency — a standalone service family.

## 0. Service layout — two protocol planes

The registry service is unusual: it has **two planes** in one service.

- **Control plane** — three distinct cloud protocols (ECR awsJson1_1, Artifact
  Registry Discovery REST, ACR ARM OpenAPI). Codegen-driven, like every prior phase.
- **Data plane** — one shared **OCI Distribution `/v2/`** HTTP API, served
  identically behind all three frontends. **Hand-written router**, not codegen
  (§4) — the registry analog of `internal/ec2query`.

```
internal/registry/
  domain/domain.go                       # domain.Registry (control + streaming data plane)
  ocidistribution/                        # shared OCI /v2/ runtime (hand-written)
    router.go  digest.go  errors.go
  frontends/
    aws_ecr/              ecr.go          # awsJson1_1 control + GetAuthorizationToken + mounts /v2/
    gcp_artifactregistry/ server.go       # Discovery REST control + mounts /v2/
    azure_acr/            server.go        # ARM control + oauth2 token exchange + mounts /v2/
services/registry/
  spec/  codegen.json  gcp-codegen.json  azure-codegen.json  gen/
  backends/ {aws, gcp, azure, inmem, distribution}/   # distribution = CNCF registry K8s peer
  conformance/  INTERSECTION.md  APPLY_INTERSECTION.md
```

`ocidistribution/` lives under `internal/registry/` (not top-level) — it has one
consumer. It is the registry analog of `internal/ec2query`: a hand-written
protocol runtime that the three codegen'd control-plane frontends each *mount* at
`/v2/` behind their own auth middleware.

## 1. Intersection

### (a) OCI Distribution data plane (`/v2/`) — shared handler

All four backends speak OCI Distribution v1 natively; the intersection here is the
spec itself.

| Operation | Method + path |
|---|---|
| Base / version check | `GET /v2/` → 200 + `Docker-Distribution-API-Version` |
| Initiate blob upload | `POST /v2/{name}/blobs/uploads/` → 202 + `Location` |
| Monolithic blob upload | `PUT /v2/{name}/blobs/uploads/{uuid}?digest=…` |
| Chunked blob upload | `PATCH` (range chunks) → `PUT` (finalize) |
| Blob existence | `HEAD /v2/{name}/blobs/{digest}` |
| Blob pull | `GET /v2/{name}/blobs/{digest}` |
| Manifest put | `PUT /v2/{name}/manifests/{reference}` (tag or digest) |
| Manifest get / head | `GET`/`HEAD /v2/{name}/manifests/{reference}` |
| Manifest delete | `DELETE /v2/{name}/manifests/{reference}` |
| Tags list | `GET /v2/{name}/tags/list` |

Cross-repo blob mount (`?mount=&from=`) is spec-**optional** — out of intersection
(fallback: full re-upload). `GET /v2/_catalog` is spec-optional and inconsistently
exposed — out of the data plane; repository listing is served via the control plane.

### (b) Control plane

| Domain op | AWS ECR | GCP Artifact Registry | Azure ACR | K8s (distribution) |
|---|---|---|---|---|
| CreateRepository | `CreateRepository` | `repositories.create` (LRO) | **implicit** (auto on first push) | NotSupported (no empty-repo API) |
| DeleteRepository | `DeleteRepository` | `repositories.delete` (LRO) | data-plane delete-all | delete all tags |
| DescribeRepository | `DescribeRepositories` | `repositories.get` | `/acr/v1/{repo}` | derive from tags |
| ListRepositories | `DescribeRepositories` | `repositories.list` | `/acr/v1/_catalog` | `/v2/_catalog` |
| ListImages | `ListImages`/`DescribeImages` | `dockerImages.list` | `/acr/v1/{repo}/_manifests` | tags/list + HEAD |
| DeleteImage / tag | `BatchDeleteImage` | `dockerImages.delete` | `manifests.delete` | OCI manifest delete |

**Key asymmetry (→ N30):** a "repository" is a first-class lifecycle resource on
ECR and AR (explicit create/delete), but on **Azure ACR and CNCF Distribution it is implicit** — it
materializes on first push and is addressed only via data-plane catalog/manifest
APIs. ACR's `registries.create` creates the *registry host* (one level up — the
analog of "the registry endpoint", not "a repository"). Distribution has no
empty-repository create call, so the connected backend returns the source
frontend's not-supported error for `CreateRepository` instead of keeping a
sidecar catalog.

### Out of intersection

| Feature | Only on | Disposition |
|---|---|---|
| Vulnerability / image scanning | ECR, AR (Container Analysis), ACR (Defender) | source-cloud "not supported" |
| Geo-replication | ACR `replications`, ECR cross-region | out |
| Lifecycle policies | ECR `PutLifecyclePolicy`, AR cleanup policies | out |
| Image signing (cosign/notation) | layered on OCI by all three | out (it is just OCI artifact pushes) |
| Tag immutability *as a mode* | ECR, ACR, AR (differing APIs) | out (→ N33) |
| Pull-through cache rules | ECR, ACR | out |
| Scoped auth tokens / scope-maps | ECR, ACR | out |

## 2. Per-cloud surfaces, specs, and data-plane auth

| Plane | AWS ECR | GCP Artifact Registry | Azure ACR |
|---|---|---|---|
| Control protocol | awsJson1_1 | Discovery REST (`artifactregistry/v1`) | ARM OpenAPI (`Microsoft.ContainerRegistry`) |
| Control auth | SigV4 (`ecr`) | GCP Bearer | Azure Bearer (Entra JWT) |
| Data-plane host | `*.dkr.ecr.<region>.amazonaws.com` | `<loc>-docker.pkg.dev` | `<name>.azurecr.io` |
| **Data-plane auth** | HTTP **Basic** (`GetAuthorizationToken` → base64(`AWS:<pw>`)) | **Bearer** (OAuth2 access token) | **token exchange** (`/oauth2/exchange` AAD→ACR refresh, `/oauth2/token` refresh→scoped access) then Bearer |

**Spec sources to vendor:**

| Cloud | Spec | Upstream | Generator |
|---|---|---|---|
| AWS ECR control | `services/registry/spec/aws-ecr.smithy.json` | `aws-models/ecr-2015-09-21.json` | `cmd/codegen` (awsJson1_1 — exists) |
| GCP AR control | `.../gcp-artifactregistry-discovery.json` | `artifactregistry.googleapis.com/$discovery` | `cmd/gcp-codegen` |
| Azure ACR control | `.../azure-containerregistry.json` | `azure-rest-api-specs/.../containerregistry` | `cmd/azure-codegen` |
| OCI data plane | **not vendored** | OCI Distribution Spec v1 (a stable standard) | **hand-written router** (§4) |

Auth verifiers reuse existing building blocks: ECR Basic (the shim issues the
`GetAuthorizationToken` credential and checks Basic on `/v2/`); GCP Bearer via
`internal/gcpbearer`; ACR exchange validates the Entra JWT via `internal/azurebearer`,
then mints + checks an ACR-scoped token. **Note:** the sockerless ACR sim leaves
`/v2/` ungated (no `/oauth2/token`), so ACR token-exchange fidelity is exercised by
shim unit tests + real-Azure Track A, not the sim.

## 3. K8s peer — CNCF `distribution/distribution` (connected)

**Recommendation: use CNCF `distribution/distribution` (the OCI reference registry,
formerly Docker Registry v2) as a *connected* backend — not an in-tree
`shimaregistry` on `shimakit`.**

- `distribution` speaks OCI `/v2/` natively and authoritatively — it *is* the
  reference implementation. Reusing it honors "reuse over reinvention" (decision #11).
- License **Apache-2.0** → allowlisted; and it is *connected* (over the wire), so no
  copyleft obligation regardless.
- Mirrors the established pattern: Phase 1 used MinIO, Phase 2 used Vault — both
  connected OSS, not shimakit peers. `shimakit`'s `Put(ns,name,version)` model is
  name+version-keyed; OCI is **digest-keyed** (content-addressable), so forcing OCI
  onto shimakit would be the wrong abstraction.
- The "peer ships in the same phase" rule is met: `distribution` is an off-the-shelf
  container we run in CI (like MinIO/Vault), with a thin `backends/distribution/`
  adapter shipping in 18.D. For the data plane the shim is largely a **verifying
  reverse proxy** to `distribution`; for the control plane it derives repo/image
  lists from `/v2/_catalog` + `/v2/{name}/tags/list` + manifest HEADs.

## 4. Codegen impact

| Plane | Generator | New lane work? |
|---|---|---|
| ECR control (awsJson1_1) | `cmd/codegen` | No — lane exists (KMS uses it). New `codegen.json`. |
| AR control (Discovery) | `cmd/gcp-codegen` | No — routing-only emitter exists. New manifest. AR repo create/delete are **LROs** (frontend polls). |
| ACR control (ARM OpenAPI) | `cmd/azure-codegen` | No new preprocessor stage anticipated (standard ARM RP). Validate in 18.A. |
| **OCI `/v2/` data plane** | **none — hand-written** | Content-addressable HTTP API: digest verification on upload, chunked `PATCH`+`Content-Range` state machine, streaming blob bodies, `Location`-header upload sessions, OCI error envelope. No Smithy/Discovery/OpenAPI source the generators consume. |

**A single hand-written `internal/registry/ocidistribution/` router, shared across
all three frontends, is the right call** — the registry analog of `internal/ec2query`.
Each codegen'd control-plane frontend mounts this one router at `/v2/` behind its own
auth middleware, supplying a thin per-frontend `Adapter` for repo-name extraction and
the cloud's `Location`-header base.

## 5. Sockerless reality (verified) and its effect on sequencing

Probed `simulators/{aws/ecr.go, gcp/artifactregistry.go, azure/acr.go}`:

| Sim | Control plane | OCI `/v2/` data plane |
|---|---|---|
| AWS `ecr.go` | yes (Create/Describe/Delete repo, PutImage, BatchGetImage, GetAuthorizationToken, lifecycle, cache) | **NO `/v2/` routes** |
| GCP `artifactregistry.go` | yes (repos CRUD, dockerImages.list) | **YES** (manifests, blobs, uploads) |
| Azure `acr.go` | yes (ARM registries, cacheRules, replications) | **YES** (blobs chunked/monolithic, manifests, `/acr/v1/` catalog) — but `/v2/` ungated (no token exchange) |

Consequences:
1. **AWS-ECR OCI push/pull sockerless lane is blocked** — needs a sockerless ECR
   `/v2/` data-plane addition. Plan: describe the gap to the user, propose filing a
   sockerless issue, and `t.Skip` the AWS-ECR data-plane cell referencing it (same
   pattern as Phase 16's Firecracker skips). The ECR **control-plane** sockerless lane
   is green immediately.
2. GCP AR + Azure ACR full push/pull sockerless lanes can be green immediately.
3. ACR data-plane **auth** is validated by unit tests + real-Azure, not the sim.

So: build the data-plane router + a frontend whose sim supports `/v2/` **first**
(GCP AR — Bearer auth, no new token-exchange code), and defer the ECR data-plane
sockerless cell to a documented skip.

## 6. Domain interface + statelessness

`domain.Registry` combines repository CRUD with OCI blob/manifest/tag operations. The
data-plane methods take/return **`io.Reader`/`io.ReadCloser`**, never `[]byte`, so
layers stream through the shim without buffering (multi-GB images; statelessness).

Statelessness — the load-bearing constraint:
- **Blobs and manifests live in the backend registry, never the shim.** On `GET` the
  shim streams the backend body straight through; on `PUT` it streams the client body
  to the backend while computing the sha256 to verify the digest in-flight (per-request
  scratch, not persisted).
- **Upload-session state lives in the backend.** OCI chunked upload uses a server-issued
  `Location` session URL across `PATCH`es. Per "multipart-style coordination state goes
  in the backend," the shim holds no session→bytes map: each backend's `UploadSession`
  carries the backend's own upload URL/UUID, and the shim rewrites the `Location` header
  so the next `PATCH` forwards to the same backend session. (Same pattern as the GCS
  multipart / Azure block-blob N-rules.)
- **Content-addressable storage is naturally stateless** — a blob is named by its own
  digest, so the shim needs no name→location table; the canonical "shim cache jar" risk
  never arises. The shim streams end-to-end and any replica answers any `/v2/` request.

## 7. Sub-phases

| Track | What | Dependency | Status |
|---|---|---|---|
| 18.A | Scoping (this doc) + N30–N34 + `domain.Registry` + `ocidistribution` router + inmem (real digest-keyed store) + round-trip unit test | — | ◐ planned |
| 18.B | OCI data-plane router behind the first frontend (**GCP Artifact Registry**) + ECR/AR/ACR codegen manifests; push/pull round-trip through the shim against inmem | 18.A | ◐ planned |
| 18.C | AWS ECR frontend (awsJson1_1 + `GetAuthorizationToken`) + Azure ACR frontend (ARM + `/oauth2/exchange`+`/oauth2/token`); full control-plane SDK/CLI/TF conformance | 18.B | ◐ planned |
| 18.D | Connected backends (aws/gcp/azure) + CNCF `distribution` K8s peer + full 3×3×4 conformance matrix + sockerless lanes (GCP AR + ACR green; ECR data-plane skip-with-issue) | 18.C | ◐ planned |

### Exit criteria

- `docs/phase-18-scoping.md` published; N30–N34 in `docs/normalizations.md`, each with code reference + test.
- `internal/registry/ocidistribution/` round-trips a push/pull against inmem in a Go test.
- `services/registry/` full 3 frontends × 3 driver types × 4 backends conformance matrix; NotImplemented rows in `INTERSECTION.md`.
- Sockerless GCP AR + Azure ACR push/pull lanes green; AWS-ECR data-plane lane documented-skipped on the pending sockerless gap; all three control-plane sockerless lanes green.
- CNCF `distribution` peer push/pull green; cross-cloud Apply cell for at least one repository op.

## 8. Normalization rules

N30–N34 added to [`docs/normalizations.md`](normalizations.md):
- **N30** — Repository lifecycle: explicit on AWS/GCP/distribution, implicit on Azure ACR.
- **N31** — Data-plane auth-token exchange (Basic vs Bearer vs ACR refresh→access exchange).
- **N32** — Manifest media-type pass-through (OCI vs Docker schema2 verbatim; digest-preserving).
- **N33** — Tag immutability is not enforced by the shim (mutable-tag intersection).
- **N34** — Digest algorithm: sha256 canonical (verified in-flight), sha512 opaque pass-through.

## 9. Architectural trade-offs

1. **Shared OCI router vs per-frontend** — shared (`internal/registry/ocidistribution/`),
   mounted behind each frontend's auth middleware. The data plane is byte-identical
   across clouds by spec; a thin per-frontend `Adapter` handles cosmetic differences
   (ECR host-based repo addressing, AR path prefix, ACR `/acr/v1/` catalog extension).
   The `internal/ec2query`-style "shared runtime + thin per-service binding" pattern.
2. **CNCF `distribution` (connected) vs `shimaregistry` on `shimakit`** — connected
   `distribution`. shimakit is name+version-keyed; OCI is digest-keyed. shimakit stays
   reserved for services with no clean OSS fit; registry has the cleanest fit possible.
3. **Stateless shim + content-addressable storage** — the hardest-looking constraint is
   the easiest fit: digest-named blobs need no name→location table. Sessions live in the
   backend (shim rewrites `Location`); digest verification is streaming scratch.
4. **Streaming proxy vs full translation on the data path** — for same-protocol data
   traffic the shim is a verifying reverse proxy (translates auth N31 + addressing,
   verifies digests N34, streams bodies). The control plane is where shape-translation
   happens. Composes with the existing HTTP frontend stack — no new listener.

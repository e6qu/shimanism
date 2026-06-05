# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–17 + 19 complete; Phase 18 (Container Registry) is in 18.D.

## Where we are

**Phase 19 (Key Management) is complete** — PRs #127 (19.A) / #128 (19.B) / #129 (19.C) / #130 (19.D CLI/TF) / #131 (19.D sockerless), all merged. KMS across AWS/GCP/Azure + K8s: domain + inmem (real AES-256-GCM) + all three frontends + real backends + full SDK/CLI/TF conformance + all sockerless lanes green with **zero skips**. All four KMS sockerless gaps closed upstream (#407/#413/#419/#423).

**Phase 18 — Container Registry is in 18.D.** OCI Distribution `/v2/` data plane (shared hand-written router) + ECR/AR/ACR control planes + connected backends. See [docs/phase-18-scoping.md](docs/phase-18-scoping.md). 18.A, 18.B, and 18.C are complete: GCP AR, AWS ECR, and Azure ACR frontends all have SDK/CLI/Terraform control-plane coverage plus go-containerregistry OCI data-plane coverage. 18.D PR1 (#139) merged the real CNCF `distribution` backend. Current PR2 adds the real AWS ECR backend using the AWS SDK control plane plus ECR's own `/v2/` data-plane credentials; GCP AR and Azure ACR cloud backends remain next.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Continue Phase 18 (see below), per [docs/phase-18-scoping.md](docs/phase-18-scoping.md).

## Phase 18 — Container Registry ◐

Scoping: [docs/phase-18-scoping.md](docs/phase-18-scoping.md). Normalizations N30–N34. Two planes in one service: OCI Distribution `/v2/` data plane (shared `internal/registry/ocidistribution/` router, hand-written) behind three codegen'd control-plane frontends (ECR awsJson1_1, AR Discovery, ACR ARM).

### 18.A — scoping + domain + router + inmem

- [x] `docs/phase-18-scoping.md` + N30–N34 + Phase 19 closeout (PR #132).
- [x] `internal/registry/domain/domain.go` — `domain.Registry` (control plane + streaming OCI data plane, `io.Reader`-based; sentinels incl. `ErrDigestMismatch`).
- [x] `internal/registry/ocidistribution/` — hand-written `/v2/` router (base/blob-monolithic+chunked/manifest/tags) + `digest.go` (sha256 verify, N34) + OCI error envelope + round-trip unit tests (push/pull, chunked, digest-mismatch, manifest+tags).
- [x] `services/registry/backends/inmem/` — real digest-keyed content-addressable store (test backend-of-record): verifies blob digests, holds chunked-upload sessions, auto-creates repo on push; repository-lifecycle + force-delete unit tests.
- Next: 18.B wires the router behind the GCP Artifact Registry frontend.

### 18.B — OCI data plane behind the GCP Artifact Registry frontend

- [x] **PR1 (this):** `internal/registry/frontends/gcp_artifactregistry` — Bearer-challenge (`WWW-Authenticate`, N31) + `gcpbearer` verify wrapping the shared `ocidistribution` router. Conformance via **go-containerregistry** (real OCI client, new dep, Apache-2.0): full image push/pull through the shim → inmem, by-tag + by-digest, tags list, layer-digest round-trip, plus unauthenticated-challenge case. `TestGCR_AR_ImagePushPull`.
- [x] **PR2:** AR control plane — `repositories.create/get/list/delete` (LROs, returned done) + `dockerImages.list` via `google.golang.org/api/artifactregistry/v1` SDK (`TestARSDK_RepositoryLifecycle`). Repos keyed by full AR resource name; control plane `gcpbearer`-gated, data plane keeps the OCI Bearer challenge.
- [x] **PR3:** AR control-plane CLI (`gcloud artifacts`) + Terraform (`google_artifact_registry_repository`). Handler accepts both `repositoryId` (gcloud) and `repository_id` (TF provider) query forms. **GCP AR frontend now conformance-complete: SDK + CLI + TF control plane + go-containerregistry data plane.**

### 18.C — AWS ECR + Azure ACR frontends

- [x] **PR1 — AWS ECR.** Codegen'd awsJson1_1 control plane (`services/registry/{spec,codegen.json,gen}`, 6 ops) + SigV4; OCI `/v2/` data plane gated by HTTP **Basic** auth minted by `GetAuthorizationToken` (N31; HMAC token, stateless verify). ECR repo names are flat, so control + data planes **unify** on the repo name. Conformance: `aws-sdk-go-v2/service/ecr` SDK (repo lifecycle) + go-containerregistry push/pull via the real docker-login flow (`GetAuthorizationToken`→Basic) + ListImages-after-push + unauthenticated-challenge. New dep `aws-sdk-go-v2/service/ecr`.
- [x] **PR2 — Azure ACR.** `internal/registry/frontends/azure_acr` with stateless `/oauth2/exchange` → `/oauth2/token`, Bearer-gated OCI `/v2/`, ACR `/acr/v1/_catalog` + `/{repo}/_manifests`, minimal ARM registry-host envelope, configurable passthrough + `/metadata/endpoints` for Azure drivers. Conformance: token-exchange + go-containerregistry push/pull, ACR catalog/manifests after push, unauthenticated challenge, raw ARM host shape, official `armcontainerregistry` SDK create/get/delete, sockerless-gated `az acr`, and sockerless-gated `azurerm_container_registry`. Merged PR #138.

### 18.D
- [x] **PR1 — CNCF `distribution` connected backend.** Added `services/registry/backends/distribution` as a real connected backend to the reference OCI registry. It implements `domain.Registry` by calling `/v2/`, `/v2/_catalog`, tags, manifests, blobs, and upload sessions; it stores no sidecar catalog and returns `ErrNotSupported` for empty repository creation because Distribution has no honest API for it. Conformance: `TestDistributionBackend_GCPAR_ImagePushPull` pushes/pulls through the GCP AR-shaped frontend into a live registry when `SHIMANISM_DISTRIBUTION_URL` is set; package tests skip only when that live registry is unavailable. Also fixed BUG-62 by omitting optional source-cloud time fields when a backend exposes no real timestamp.
- [ ] **PR2 — AWS ECR connected backend (in flight).** `services/registry/backends/aws_ecr` uses a real `*ecr.Client` for repository lifecycle, tag reads, image list, image delete, and `GetAuthorizationToken`; OCI blob/manifest/tag traffic delegates to the real ECR `/v2/` endpoint with the Basic credential ECR returns. No repository/image sidecar state. Also fixes BUG-61 by returning ECR `BatchDeleteImage.failures[]` instead of dropping per-image delete failures.
- [ ] **PR3 — GCP Artifact Registry + Azure ACR connected backends.** Use official SDK/control APIs where available and real data-plane HTTP. No synthetic repository/image state; derive from backend APIs only. Be explicit about source-shaped repository-name asymmetries.
- [ ] **PR4 — sockerless lanes.** GCP AR + Azure ACR push/pull through-shim green. AWS ECR data-plane lane must fail loudly/document skip on the known simulator gap (sockerless ECR has no `/v2/`); if confirmed, ask user before filing at `github.com/e6qu/sockerless`.
- [ ] Full matrix/docs closeout: `services/registry/INTERSECTION.md`, Apply/cross-cloud cell, PLAN/STATUS/WHAT_WE_DID updates.

## Phase 19 — Key Management ✅ COMPLETE

### 19.A — domain + inmem + AWS KMS lane ✅ (PR #127)

domain.KMS (symmetric ENCRYPT_DECRYPT; Decrypt key-ref-in-ciphertext) + inmem (real AES-256-GCM) + AWS KMS frontend (SigV4, awsJson1_1) + SDK/CLI/Terraform conformance + N29 + INTERSECTION.md. GetKeyPolicy returns AWS default (policies out of intersection); ListResourceTags reads real tags.

### 19.B — GCP Cloud KMS + Azure Key Vault keys ✅ (PR #128)

GCP frontend (`cryptoKeys.create/get/list` + `cryptoKeyVersions.encrypt/decrypt` + rotation) and Azure Key Vault keys frontend (`PUT/GET/LIST /keys` + `encrypt/decrypt`) + SDK conformance. CLI/Terraform breadth deferred to 19.D.

### 19.C — K8s NotImplemented + sockerless lane (PR #129)

- [x] K8s peer: all KMS ops NotImplemented (no core built-in key-crypto API; etcd-encryption is cluster config).
- [x] Sockerless: AWS KMS through-shim lane wired; GCP Cloud KMS absent upstream (skip referencing the filed gap); Azure KV-keys lane is a 19.D follow-on.
- [x] **CI unblock (BUG-52):** `sockerless through-shim e2e` was red on a *secrets* test, not KMS — `TestSockerless_E2E_GCPSecrets_..._BackendAzure` deterministically crashed `terraform-provider-google` v5.45.2. Root cause: sockerless KV lists secret versions by random UUID (not creation order) + 1s-resolution `created` → shim's created-ordered version mapping resolves "version 2" to the empty placeholder → empty `payload.data` → provider panic. Filed [sockerless#407](https://github.com/e6qu/sockerless/issues/407). **Closed by [sockerless PR #412](https://github.com/e6qu/sockerless/pull/412) (2026-06-04)** — version listing now creation-ordered. Test un-gated. No shim-logic change (per user: "Only file sockerless").

### 19.D — KMS conformance breadth + sockerless lanes

**PR1 (#130, open):** GCP `gcloud kms` CLI + `hashicorp/google` Terraform conformance. Surfaced + fixed real frontend fidelity gaps: CRC32C data-integrity (BUG-53), cryptoKeyVersions list/get/`:destroy`, std/URL-safe base64, AAD rejection. **Also made keyRings honest (BUG-58):** promoted keyRing to a non-optional `domain.KMS` capability — inmem + native GCP track rings for real, AWS/Azure return `NotSupported` (no honest container home; keyRing is out of the cross-cloud data-plane intersection — see INTERSECTION.md). Reported the missing GCP Cloud KMS *simulator* to sockerless ([#419](https://github.com/e6qu/sockerless/issues/419)). GCP-KMS × AWS/Azure keyRing cell documented out-of-intersection.

**PR2 (#TBD, in flight):** real sockerless lanes. Un-gated `TestSockerless_AzureKVKeys_Through_Shim` (azkeys → Azure KV-keys frontend → Azure backend → sockerless KV sim; real RSA-OAEP round-trip) and added `TestSockerless_AWSKMS_Through_Shim_TerraformTaggedKey` (tagged `aws_kms_key` TF → shim → sockerless, exercising the #413/#415 tag round-trip; clean refresh-plan proves no perpetual diff). **BUG-59:** the kms conformance package was never in `scripts/run-sockerless-storage.sh`, so the 19.C AWS KMS sockerless lane never ran — added it. Azure `az`/`azurerm` data-plane CLI+TF cells added as documented skips (tooling can't redirect vault DNS/ARM — secrets precedent).

- [x] **GCP CLI + Terraform** KMS conformance (`gcloud kms` + `hashicorp/google` `google_kms_crypto_key`/`key_ring`) — PR #130.
- [ ] **Azure CLI + Terraform** Key Vault keys conformance (`az keyvault key` + `hashicorp/azurerm` `azurerm_key_vault_key`) — 19.B shipped Azure SDK only.
- [ ] **Azure KV-keys sockerless lane** — wire `TestSockerless_AzureKVKeys_Through_Shim` (currently a 19.D-gated `t.Skip` behind `SOCKERLESS_AZURE_TLS_PORT`).
- [ ] **AWS KMS tagged-`aws_kms_key` Terraform-against-sockerless lane** — now unblocked by [sockerless#413](https://github.com/e6qu/sockerless/issues/413) (KMS tagging) closed by [PR #415](https://github.com/e6qu/sockerless/pull/415) (2026-06-04): sockerless now round-trips KMS tags + implements `TagResource`/`UntagResource`/`ListResourceTags`.
- [ ] GCP Cloud KMS sockerless lane stays gated until sockerless adds a Cloud KMS simulator (still absent; `TestSockerless_GCPKMS_Through_Shim` skips referencing the filed gap).
- [ ] Close Phase 19 in PLAN.md + WHAT_WE_DID.md narrative once the matrix is green.

## Phase 17 ✅ (PRs #122–#125)

Block storage — AWS/GCP/Azure SDK+CLI+Terraform, K8s PVC volume CRUD, sockerless EBS. Provider wire-quirks absorbed in-shim: EBS `CreateTime` nil-deref, Azure disk/snapshot 200-vs-201 poller, GCP `sizeGb` unquoted-vs-`,string`.

## Upstream watch

All Firecracker blockers resolved. Sockerless PRs #392/#395 merged.

## Standing rules

- **One PR open at a time.** Before opening any PR, check `gh pr list --state open`; if one exists, ask the user first. Close stale/superseded PRs.
- **Fix shim problems in the shim.** Only ever file with `github.com/e6qu/sockerless` (real sockerless-side gaps, after asking) — never Hashicorp or any other upstream.
- Test driver is always the cloud SDK / CLI / Terraform provider.
- Never auto-merge; user merges every PR.
- File BUGs in BUGS.md before fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT every significant chunk.

## Validation lanes

- `make codegen-check` — regenerates every gen file; mirrors CI.
- `make test` — all unit + conformance tests.
- `make sockerless` — through-shim e2e lane.

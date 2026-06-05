# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–17 + 19 complete; Phase 18 (Container Registry) starting at 18.A.

## Where we are

**Phase 19 (Key Management) is complete** — PRs #127 (19.A) / #128 (19.B) / #129 (19.C) / #130 (19.D CLI/TF) / #131 (19.D sockerless), all merged. KMS across AWS/GCP/Azure + K8s: domain + inmem (real AES-256-GCM) + all three frontends + real backends + full SDK/CLI/TF conformance + all sockerless lanes green with **zero skips**. All four KMS sockerless gaps closed upstream (#407/#413/#419/#423).

**Now starting Phase 18 — Container Registry.** OCI Distribution `/v2/` data plane (shared hand-written router) + ECR/AR/ACR control planes + CNCF `distribution` K8s peer. See [docs/phase-18-scoping.md](docs/phase-18-scoping.md). 18.A is in flight (scoping + N30–N34 + domain + ocidistribution router + inmem).

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Continue Phase 18 (see below), per [docs/phase-18-scoping.md](docs/phase-18-scoping.md).

## Phase 18 — Container Registry ◐

Scoping: [docs/phase-18-scoping.md](docs/phase-18-scoping.md). Normalizations N30–N34. Two planes in one service: OCI Distribution `/v2/` data plane (shared `internal/registry/ocidistribution/` router, hand-written) behind three codegen'd control-plane frontends (ECR awsJson1_1, AR Discovery, ACR ARM).

### 18.A — scoping + domain + router + inmem (this PR: scoping/docs portion)

- [x] `docs/phase-18-scoping.md` + N30–N34 + Phase 19 closeout.
- [ ] `internal/registry/domain/domain.go` — `domain.Registry` (control plane + streaming OCI data plane, `io.Reader`-based).
- [ ] `internal/registry/ocidistribution/` — hand-written `/v2/` router + digest verify + OCI error envelope + round-trip unit test.
- [ ] `services/registry/backends/inmem/` — real digest-keyed content-addressable store (test backend-of-record).

### 18.B–D (planned)

- **18.B** — OCI router behind GCP Artifact Registry frontend first (Bearer auth, sim has `/v2/`); ECR/AR/ACR codegen manifests; push/pull round-trip vs inmem.
- **18.C** — AWS ECR frontend (`GetAuthorizationToken` Basic) + Azure ACR frontend (`/oauth2/exchange`+`/oauth2/token`); full control-plane SDK/CLI/TF.
- **18.D** — connected backends + CNCF `distribution` K8s peer + full 3×3×4 matrix + sockerless lanes (GCP AR + ACR green; **AWS-ECR data-plane skip-with-issue** — sockerless ECR has no `/v2/`, ask user before filing).

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

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–17 complete; Phase 19 (Key Management) in progress — 19.A/B/C merged, 19.D next.

## Where we are

**Phase 19.A/B/C all merged** (PRs #127/#128/#129). KMS across AWS/GCP/Azure: domain + inmem (real AES-256-GCM) + all three frontends + real backends + K8s NotImplemented + sockerless AWS KMS lane. **19.D is next** (CLI/TF conformance breadth + sockerless lanes — see below). Phase 17 (block storage) complete (#122–#125). Phase 18 (Container Registry) not started.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Continue Phase 19.D (see below), or pick up Phase 18 / 20–23 from PLAN.md.

## Phase 19 — Key Management ◐

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

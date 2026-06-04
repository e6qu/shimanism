# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **Cold-start entry point.** Phases 1–17 complete; Phase 19 (Key Management) in progress.

## Where we are

**Phase 19.A merged** (PR #127). AWS KMS lane: domain + inmem (real AES-256-GCM) + SDK/CLI/Terraform. Phase 17 (block storage) complete (#122–#125). Phase 18 (Container Registry) not started.

**Open bugs:** BUG-8 · BUG-15 · BUG-41 (Track A only — blocked on real GCP credentials).

## Session-start checklist

1. `git fetch origin && git checkout main && git pull --ff-only origin main` — sync `main`.
2. Continue Phase 19 (see below), or pick up Phase 18 / 20–23 from PLAN.md.

## Phase 19 — Key Management ◐

### 19.A — domain + inmem + AWS KMS lane ✅ (PR #127)

domain.KMS (symmetric ENCRYPT_DECRYPT; Decrypt key-ref-in-ciphertext) + inmem (real AES-256-GCM) + AWS KMS frontend (SigV4, awsJson1_1) + SDK/CLI/Terraform conformance + N29 + INTERSECTION.md. GetKeyPolicy returns AWS default (policies out of intersection); ListResourceTags reads real tags.

### 19.B — GCP Cloud KMS + Azure Key Vault keys (next)

- [ ] GCP frontend: `cryptoKeys.create/get/list` + `cryptoKeyVersions.encrypt/decrypt` + rotation (rotationPeriod).
- [ ] Azure frontend: Key Vault keys `PUT/GET/LIST /keys` + `encrypt/decrypt`.
- [ ] Real backends + SDK/CLI/Terraform conformance for both.

### 19.C — K8s NotImplemented + sockerless lane (PR #129)

- [x] K8s peer: all KMS ops NotImplemented (no core built-in key-crypto API; etcd-encryption is cluster config).
- [x] Sockerless: AWS KMS through-shim lane wired; GCP Cloud KMS absent upstream (skip referencing the filed gap); Azure KV-keys lane is a 19.D follow-on.
- [x] **CI unblock (BUG-52):** `sockerless through-shim e2e` was red on a *secrets* test, not KMS — `TestSockerless_E2E_GCPSecrets_..._BackendAzure` deterministically crashed `terraform-provider-google` v5.45.2. Root cause: sockerless KV lists secret versions by random UUID (not creation order) + 1s-resolution `created` → shim's created-ordered version mapping resolves "version 2" to the empty placeholder → empty `payload.data` → provider panic. Filed [sockerless#407](https://github.com/e6qu/sockerless/issues/407). **Closed by [sockerless PR #412](https://github.com/e6qu/sockerless/pull/412) (2026-06-04)** — version listing now creation-ordered. Test un-gated. No shim-logic change (per user: "Only file sockerless").

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

# Do Next

Status [STATUS.md](STATUS.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · narrative [WHAT_WE_DID.md](WHAT_WE_DID.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

> **This is the resume-from-cold file.** A fresh agent or post-compaction session should read this top-to-bottom and pick up work without re-deriving context from older messages.

## Where we are

- **Last merged:** PR #6 (Phase 1 — object storage, full 3 × 5 × 3 matrix + CI tooling) at `1f64d9f` on `origin/main`, 2026-05-19.
- **Active branch:** `phase-2-secrets` — fresh branch off `main`, no commits yet, no PR yet.
- **Project phase:** **Phase 2 — Secrets management.** Three frontends (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × four backends (the three clouds + Vault as K8s peer) × three driver types (SDK + CLI + Terraform). Same N × N matrix discipline as Phase 1.

## Phase 2 sub-task table

| Sub | Status | Headline |
|---|---|---|
| **2.1** | ✅ | Spec ingest. AWS Secrets Manager Smithy 2.0 JSON vendored under `services/secrets/spec/`, pinned to `aws/aws-sdk-go-v2@2517fe9f`. `services/secrets/codegen.json` manifest names the 7 intersection ops. GCP + Azure specs reused via their official Go SDKs' wire-type packages (same approach as the GCS + Azure Blob frontends in Phase 1.14/1.15). |
| **2.2** | ✅ | `internal/secrets/domain/` neutral interface — `Secrets` interface + types (`Secret`, `SecretValue`, `Version`, `CreateSecretOptions`, `*Result`, `ListSecretsOptions`); typed `Error` with `Kind` discriminator + sentinel constructors. Versions: monotonic uint64; mapping to/from native cloud handles derived per-request (stateless). |
| **2.3** | ✅ | AWS Secrets Manager frontend `internal/secrets/frontends/aws_secretsmanager/`. Hand-written awsJson1_1 dispatch + per-op JSON shapes mirroring the Smithy spec; tags + version IDs round-trip; AWSCURRENT / AWSPREVIOUS stage labels resolved by listing versions (no shim-side mapping table). ARN normalisation accepts both shim-issued and real-AWS-shaped ARNs. The existing Smithy codegen emits REST-XML; extending it to JSON-protocol is deferred (noted in commit 3b83aa5). |
| **2.4** | ✅ | `services/secrets/backends/inmem/` covering all 7 ops with versioned values + soft-delete state. `services/secrets/conformance/aws_sdk_test.go` drives `aws-sdk-go-v2/service/secretsmanager` against the in-mem backend: secret lifecycle, AWSCURRENT/AWSPREVIOUS + explicit-VersionId reads, ARN normalisation, duplicate-create rejection. `harness.StartSecretsServerAWS` mounts the frontend → in-mem chain on a random localhost port. |
| **2.5** | ✅ | **Vault backend** (K8s peer): `services/secrets/backends/vault/` via `hashicorp/vault/api`. Maps domain secrets to KV v2 paths under a configurable mount (default `secret`). Vault's native version numbering is monotonic → 1:1 with the domain. Description encoded as `shim-description` key in custom_metadata. Soft delete = `DELETE <mount>/data/<name>` (current version marked deleted); force = `DELETE <mount>/metadata/<name>` (removes everything). `deploy/k8s/peer-secrets/` manifests follow in 2.13. |
| **2.6** | ✅ | **AWS Secrets Manager passthrough backend** `services/secrets/backends/aws/` via `aws-sdk-go-v2/service/secretsmanager`. Monotonic version ↔ AWS UUID derived per request by calling `ListSecretVersionIds` + sorting by CreatedDate (no shim-side mapping table). |
| **2.7** | ◻ | **GCP Secret Manager backend** via `cloud.google.com/go/secretmanager`. Cross-cloud version-id mapping: GCP uses `projects/.../secrets/.../versions/<N>` and `latest`; domain stores the monotonic int. |
| **2.8** | ◻ | **Azure Key Vault backend** via `azure-sdk-for-go/sdk/security/keyvault/azsecrets`. Cross-cloud version-id mapping: Azure uses GUIDs; domain caches the GUID-↔-monotonic mapping per secret. |
| **2.9** | ◻ | **GCP Secret Manager frontend.** Wire types from `google.golang.org/api/secretmanager/v1` (Discovery-generated, same as the SDK). Routes cover `projects/*/secrets/*` + `versions/*` + `:access` and `:addVersion` paths. SDK + `gcloud secrets` CLI + `hashicorp/google` Terraform conformance. |
| **2.10** | ◻ | **Azure Key Vault frontend.** Wire types match the shapes the Azure SDK's `azsecrets/internal/generated` package uses. Routes cover `/secrets/{name}` + `/secrets/{name}/{version}` + `/deletedsecrets`. SDK + `az keyvault secret` CLI + `hashicorp/azurerm` Terraform conformance. |
| **2.11** | ◻ | CLI conformance for AWS frontend (`aws secretsmanager`). Terraform conformance for AWS frontend (`aws_secretsmanager_secret` + `aws_secretsmanager_secret_version`). |
| **2.12** | ◻ | CI conformance lanes: `conformance-vault` (Vault dev container), `conformance-gcp-secretmanager` (no public emulator — defer; gate on `GCP_SECRETMANAGER_CONFORMANCE=1`), `conformance-azure-keyvault` (defer; no public emulator). Each frontend × backend matrix test still runs against in-mem so the SDK row is fully green per-lane. |
| **2.13** | ◻ | `cmd/shim secrets [-frontend=<...> -backend=<...>]` subcommand. Multi-service binary; secrets sits alongside the existing `storage` subcommand. |
| **2.14** | ◻ | Phase 2 closer: SDK matrix green across all (frontend × backend) cells; CLI + TF rows green where their tooling admits endpoint override; ◇ skipped cells documented per Phase 1 convention. CI green across all required checks. |

Status legend: ✅ done · ◐ in progress · ◻ pending · ⏸ paused.

## Phase 2 design notes

**Version semantics — the hard part.** The three clouds model versions differently:

| Cloud | Version handle | "Latest" alias | Stage labels |
|---|---|---|---|
| AWS Secrets Manager | `VersionId` (UUID) + `VersionStages[]` (e.g. `AWSCURRENT`, `AWSPREVIOUS`) | `AWSCURRENT` | yes, multiple per version |
| GCP Secret Manager | numeric `versions/<N>` | `versions/latest` (alias) | no |
| Azure Key Vault | hex GUID | bare secret name returns latest | no |
| Vault KV v2 | numeric `metadata.current_version` | implicit on bare GET | no |

The domain interface uses **monotonic uint64** version numbers as the canonical identifier. Each backend's adapter maps that to/from the cloud's native form:

- AWS adapter: store `(monotonic, VersionId)` pairs in the backend's metadata; map staleness via `AWSCURRENT` stage.
- GCP adapter: monotonic maps directly to `versions/<N>`.
- Azure adapter: monotonic ↔ GUID cached in the backend's metadata.
- Vault adapter: monotonic maps directly to `metadata.versions[i].version`.

`AWSCURRENT` / `AWSPREVIOUS` stage semantics live in the AWS frontend adapter (not in the domain) — they're AWS-specific.

**Out-of-intersection features (return source-cloud "not supported" error):**
- AWS rotation lambdas, replication regions, KMS encryption-context overrides
- GCP TTL-based expiration, replication policies, Pub/Sub topic notifications
- Azure HSM-backed keys, cert imports, soft-delete recovery beyond a basic 7-day window
- Vault dynamic secrets, transit-engine ops, PKI roles

**K8s peer choice — Vault.** Per [PLAN.md § Phase 2](PLAN.md#phase-2--secrets), Vault is the K8s-native peer. The shim talks to it via the KV v2 secrets engine. Other Vault engines (transit, pki, etc.) are out of intersection.

## Invariants snapshot (full list in [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions))

- Never auto-merge; user merges every PR.
- **One PR at a time.** Work piles on the single open PR; new branches only start after the current PR merges.
- File BUGs in [BUGS.md](BUGS.md) *before* fixing.
- Update STATUS / WHAT_WE_DID / DO_NEXT at every significant chunk.
- Fidelity to the source cloud's API. Out-of-intersection features return source cloud's own error; never fabricate success.
- Real backends only; no emulators (the in-mem backend is a real-secrets test fixture, not an emulator).
- Tests from official client surfaces: SDK + CLI + Terraform provider per operation, per backend, same commit.
- Kubernetes is a first-class fourth backend.
- **Reuse over reinvention** ([AGENTS.md](AGENTS.md#reuse-over-reinvention)): wire types from each cloud's official Go SDK; spec inputs from upstream-canonical sources; auth verification via the cloud's official verifier libraries.

## Resumable tracks (longer-horizon)

- **Track A — Cloud test accounts.** Decide where live cloud accounts for nightly conformance runs live, and who pays. Live-cloud rows for AWS / GCS / Azure Blob are skipped on every phase until this lands.
- **Track B — Coding-agent automation.** Auto-PR template per service, agent permissions for upstream spec bumps, conformance-failure → BUG-filing automation.

## Session-resume checklist

When picking up after compaction or in a fresh session:

1. `git fetch origin && git checkout main && git pull` — sync.
2. `gh pr list --state open` — find the single open PR. **Don't open a new one** if any are open; pile work onto the existing branch.
3. `git checkout <pr-branch>` — get on the active branch.
4. Read [STATUS.md § Snapshot](STATUS.md#snapshot) and this file's "Where we are" section.
5. Read [STATUS.md § Invariants](STATUS.md#invariants-carry-across-compactions--fresh-sessions) and [AGENTS.md](AGENTS.md) before any code change.
6. Skim [BUGS.md § Open](BUGS.md#open) — anything in there pre-empts new feature work unless explicitly deferred in the bug entry.
7. Pick the next ◻ sub-task above; mark ◐ when starting; include continuity-doc updates in the same PR.

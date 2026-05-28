# shimanism — What We Did

Status [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · roadmap [PLAN.md](PLAN.md) · bugs [BUGS.md](BUGS.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md).

> Reverse chronological. One section per phase. The *why*, the surprises, the root causes — not per-PR detail. For commit-level history, `git log`. For per-bug detail, [BUGS.md](BUGS.md). For pipeline + verifier architecture, [docs/codegen-pipelines.md](docs/codegen-pipelines.md) + [docs/verifiers.md](docs/verifiers.md).

## Phase 14 — In flight

PR #21 merged on 2026-05-25 at `45985e7`, landing 14.A, the 14.D simulator audit, and the current 14.B sockerless lane. Phase 14.B and 14.C are now closed (PR #46). Phase 13.A is also closed — PR #47 retired the last ◐ migration (`azure_blob`). What remains in Phase 14: PR 3 (14.D Track A, real-cloud-credentials-gated). 14.E cross-cloud Apply re-opens once shim-side ARM-shimming exists (see PR #46 narrative below).

### 14.E.5 — Through-shim azurerm Terraform Apply for Key Vault (in flight 2026-05-28)

Second un-skipped azurerm Terraform Apply, mirroring the storage pattern from PR #54. Confirms the mock-AAD approach generalizes across services with an ARM frontend.

**Frontend changes** — `internal/secrets/frontends/azure_arm_keyvault/server.go`:
- `Options{VaultURI, TrackVaults}` (parallels storage's `Options{BlobEndpoint, TrackAccounts}` from PR #53).
- `syntheticVault` is now a method (was a free function) so it can read `srv.vaultURI` for the synthetic `properties.vaultUri`. Falls back to the real-Azure-shaped `https://<name>.vault.azure.net/` default when no override is set.
- `recordVault` / `hasVault` / `forgetVault` track which vault names have been PUT — used by `VaultsGet` to return 404 before the first PUT (azurerm's pre-create idempotency check otherwise sees "already exists").

**Harness** — `StartSecretsServerAzureARM` now takes a variadic `vaultURI string`. The existing sockerless cell at `services/secrets/conformance/sockerless_test.go` continues to work (no URI passed → real-Azure-shaped default). `SecretsServer` exposes a `CertFile` field (populated by `StartSecretsServerAzure`'s TLS server) so tests can build a combined CA bundle including the data-plane cert.

**Test** — `services/secrets/conformance/azurerm_apply_test.go`. The HCL creates `azurerm_key_vault.kv` (ARM PUT, vault tracking flips, vault_uri = data-plane shim URL) followed by `azurerm_key_vault_secret.secret` (data-plane PUT to the URI azurerm read from ARM). Assertion: `domain.Secrets.ListSecrets` contains `shim-applied-secret`.

The previously-skipped `TestTerraform_AzureSecrets_ResourceLifecycle` stays as a redirect-stub pointing at the new cell.

**Why KV was easier than storage:** KV's data-plane auth uses bearer tokens (same `azurebearer` middleware as ARM) instead of SharedKey. No synthetic `listKeys` equivalent needed — the same bearer azurerm got from mock-AAD works for the vault data plane.

End state: `make sockerless` still 45 passing. The new Linux-only test is opt-in via system-CA-bundle presence (same skip-on-macOS as PR #54).

### 14.E.4 — Mock Microsoft Entra + first through-shim azurerm Terraform Apply (in flight 2026-05-28)

The last barrier between PR #51–#53's ARM-shimming infrastructure and a green `hashicorp/azurerm` Terraform Apply against the shim: the provider routes through Microsoft Entra (Azure AD) to exchange a `client_secret` for a bearer token. The shim doesn't shim Entra in production.

**`internal/mockaad`** — minimal AAD-compatible HTTP surface that serves:
- `GET /metadata/endpoints` — Azure cloud-metadata document with `authentication.loginEndpoint` pointing at the mock itself + `resourceManager` pointing at the shim's ARM frontend. azurerm fetches this when `metadata_host` is set.
- `GET /{tenant}/.well-known/openid-configuration` — OIDC discovery.
- `POST /{tenant}/oauth2/v2.0/token` — accepts any `client_credentials` grant, returns an HS256-signed JWT minted with `azurebearer.TestJWT`. Ignores client_id + client_secret entirely (the mock is permissive — Entra-side rejection isn't its job).

**TLS.** The mock runs over `httptest.NewTLSServer` because azurerm refuses non-HTTPS `metadata_host`. The auto-generated self-signed cert is written to a temp file (`MockAADServer.CertFile`) so tests can pass it to Terraform via a combined CA bundle.

**Linux-only.** Go's TLS stack honors `SSL_CERT_FILE` on Linux/Unix but uses the Security framework directly on macOS (where the env var is ignored). The test skips on darwin and runs only when a system CA bundle is found at one of the known Unix paths. CI runs on Linux so the test validates fully there.

**`TestCrossCloudApply_Roundtrip_StorageAzureToAWS`** drives the first through-shim azurerm Terraform Apply:
1. Start: shared in-memory backend, blob frontend, ARM frontend (with `blobEndpoint=blobShim.URL`), mock-AAD.
2. Combined CA bundle: system roots + mock-AAD self-signed cert.
3. Terraform config: `provider "azurerm"` with `metadata_host` pointing at mock-AAD, static `client_id`/`client_secret`/`tenant_id`/`subscription_id`. Resources: `azurerm_storage_account` + `azurerm_storage_container`.
4. `terraform init && terraform apply`. azurerm: fetches metadata → fetches OIDC config → POSTs to token endpoint → gets bearer → calls ARM (shim) → calls blob (shim, via PrimaryEndpoints.Blob from PR #53).
5. Assert `applied-container` lands in the backend's bucket list.

**What this unlocks.** Eight currently-skipped `azurerm_*_terraform_test.go` cells (storage, KV, cache, queue, pubsub, functions, rdbms, apigateway) can now follow the same pattern. PR 5 extends it; this PR proves it works for storage.

End state: `make sockerless` still 45 passing. The new Linux-only test is opt-in via system-CA-bundle presence.

### 14.E.3 — Storage ARM blob-endpoint propagation (in flight 2026-05-28)

The missing piece between PR #51 (Microsoft.Storage ARM frontend) and a working `hashicorp/azurerm` Terraform Apply through the shim.

**The original constraint** (recorded in the pre-existing `TestTerraform_AzureBlob_ResourceLifecycle` skip message): azurerm 4.x doesn't expose a per-resource blob endpoint override. It derives blob URLs from the ARM `azurerm_storage_account` resource's `primary_blob_endpoint` attribute, which the provider fetches from `https://management.azure.com/.../storageAccounts/{name}?api-version=...` and reads from `properties.primaryEndpoints.blob`.

**The fix.** Extend `azure_arm_storageaccounts.New` to accept an `Options{BlobEndpoint string}` parameter. When set, every synthetic StorageAccount response includes `properties.primaryEndpoints.blob = BlobEndpoint`. The harness `StartStorageServerAzureARM(t, backend, blobShim.URL)` now takes the co-running blob frontend's URL as an optional variadic argument, so tests can wire ARM → Blob discovery in one call.

**Verification.** The existing `TestSockerless_E2E_AzureARM_StorageAccount_Through_Shim` cell now passes `blobShim.URL` to the ARM helper and asserts that `accountProps.Account.Properties.PrimaryEndpoints.Blob == blobShim.URL + "/"` after the ARM GET. Mechanism proven end-to-end via the `armstorage` SDK.

**Remaining gap before the azurerm Terraform Apply test un-skips.** The provider needs to exchange a credential for an ARM bearer token. Standard modes (Azure CLI, service principal client_secret, managed identity, OIDC) all hit Microsoft Entra (Azure AD). The shim doesn't shim Entra. Two paths forward — mock-AAD on the shim that azurerm trusts (substantial), or a provider option for a static bearer (currently doesn't exist). Updated the skip message on `TestTerraform_AzureBlob_ResourceLifecycle` to reflect that the mechanism gap is closed; auth is the last barrier.

End state: `make sockerless` still **45 passing + 0 skipped** (cell extension, not addition).

### 14.E ARM-shimming PR 2 — Microsoft.KeyVault/vaults (in flight 2026-05-28)

Second PR in the ARM-shimming workstream. Unblocks through-shim `azurerm_key_vault` Terraform Apply (the data-plane `azurerm_key_vault_secret` resource already works via the existing `azure_keyvault` data-plane frontend).

Key Vault was sequenced ahead of Service Bus because it's simpler: 17 ARM ops vs SB's 100+, single sibling `$ref` to `common.json` vs SB's `../../common/v<N>/` form, no cross-version chasing.

**Vendored specs.** Two files at `services/secrets/spec/`:
- `azure-arm-keyvault.json` (67KB) — `specification/keyvault/resource-manager/Microsoft.KeyVault/KeyVault/stable/2024-11-01/keyvault.json`.
- `common.json` (3KB) — the sibling that defines `SystemData` + `CloudError`. Named after upstream rather than namespaced (the spec directory is service-scoped, so no collision risk).

**Two related infrastructure fixes shipped here:**

1. `scripts/fetch-azure-spec.sh` now auto-appends a new row to SOURCES.md when vendoring a new file (previously it noted "edit by hand" and `inject-provenance` would skip the file on the first pass). PR #51's CI tripped on this exact gap; the fix lands here so PR 2 (and all future ARM-shimming PRs) don't repeat it.
2. `cmd/azure-codegen`'s `sameVersionPattern` accepts bare-filename `$ref` (no `./` prefix). KV's `keyvault.json` references `common.json` directly without the `./` shorthand; the existing pattern required `./common.json`. One-character regex change (`^\./` → `^(?:\./)?`).

**Codegen.** `services/secrets/azure-arm-codegen.json` manifest. Generated `services/secrets/gen/azure_arm/azure_arm_keyvault.gen.go` (104KB, 17 ServerInterface methods).

**Frontend.** `internal/secrets/frontends/azure_arm_keyvault/` — backend-free (vaults don't map to anything in `domain.Secrets`; the shim is vault-agnostic at the ARM level and the existing `azure_keyvault` data-plane frontend already strips vault URL prefixes):

- **7 in-intersection synthetic acknowledgements:** `VaultsCreateOrUpdate / Get / Update / Delete / ListBySubscription / ListByResourceGroup / CheckNameAvailability`. Returns canonical `Vault` shape with stable defaults (Standard SKU, soft-delete enabled, 90-day retention).
- **10 stubs:** soft-delete operations (`VaultsListDeleted` / `GetDeleted` / `PurgeDeleted`), access policy patches, private-endpoint connections (4 ops), private-link resources, and the legacy generic `VaultsList` over `/subscriptions/{s}/resources`.

**Harness.** `harness.StartSecretsServerAzureARM(t)` (no backend arg — the ARM frontend doesn't need one). Same `azurebearer` middleware as other ARM frontends.

**Sockerless cell.** `TestSockerless_E2E_AzureARM_KeyVault_Through_Shim` drives `armkeyvault.NewVaultsClient` against the shim: BeginCreateOrUpdate + PollUntilDone + Get + Delete. The cell uses `SOCKERLESS_AWS_ENDPOINT` as a soft lane-presence sentinel — vault ARM operations are pure shim acknowledgements and don't actually need sockerless on the destination side.

End state: `make sockerless` reports **45 passing + 0 skipped** (was 44 after PR #51).

### 14.E ARM-shimming PR 1 — Microsoft.Storage/storageAccounts (in flight 2026-05-27)

First PR in the multi-PR workstream that grows shim-side ARM-shimming so the `azurerm` Terraform provider can drive cross-cloud Apply through the shim. Today: the shim's Azure frontends (`azure_blob`, `azure_servicebus`, `azure_keyvault`) only speak data planes. This PR adds an ARM frontend for Microsoft.Storage; future PRs follow for Service Bus + Key Vault.

**Vendored spec.** `services/storage/spec/azure-arm-storage.json` (622KB) — `specification/storage/resource-manager/Microsoft.Storage/stable/2026-04-01/openapi.json` from Azure/azure-rest-api-specs@337bb8679. Added via `scripts/fetch-azure-spec.sh`; SOURCES.md updated.

**Codegen.** New `services/storage/azure-arm-codegen.json` manifest pointing at the new spec. The Makefile glob expanded to find `azure*codegen.json` so a service can carry both `azure-codegen.json` (existing data-plane) and `azure-arm-codegen.json` (new ARM) side-by-side. `make codegen` emits `services/storage/gen/azure_arm/azure_arm_storage.gen.go` (768KB, 120 ServerInterface methods).

**Frontend.** New `internal/storage/frontends/azure_arm_storageaccounts/` implements all 120 methods:

- **11 in-intersection bridges.** `StorageAccountsCreate/GetProperties/Update/Delete/List/ListByResourceGroup/CheckNameAvailability` acknowledge synthetically (the shim is account-agnostic — accounts are routing-fiction). `BlobContainersCreate/Get/Delete/List` bridge to `domain.Storage.CreateBucket/HeadBucket/DeleteBucket/ListBuckets`. Account names from the URL are stripped; the backend bucket namespace is shared across all "accounts" (the data-plane `azure_blob` frontend already does the same).
- **109 stubs.** Everything else (encryption scopes, blob inventory, lifecycle management, queue/file/table services, network rules, private endpoints, SAS / keys, etc.) returns the ARM error envelope via `notImplemented` (HTTP 501, `OperationNotSupported`).

**Harness.** New `harness.StartStorageServerAzureARM(t, backend)` wraps the new frontend with the same `azurebearer` middleware (`Audience: https://management.azure.com/`, test HS256 key) every other ARM-shimmed service uses.

**Sockerless cell.** `TestSockerless_E2E_AzureARM_StorageAccount_Through_Shim` exercises the full ARM → data-plane round trip:
1. `armstorage.NewAccountsClient` PUT `storageAccount` (shim acknowledges synthetically).
2. ARM GET `storageAccount` (shim returns synthetic resource — always "exists").
3. `armstorage.NewBlobContainersClient` PUT `container` (shim bridges to `backend.CreateBucket`).
4. `azblob.UploadBuffer` PUT blob through the data-plane frontend (shim's existing `azure_blob` → `backend.PutObject`).
5. `azblob.DownloadStream` GET blob → assert content matches.
6. ARM DELETE container + account.

Two shim frontends backed by the same backend instance (`awsbackend.New(...)` pointing at sockerless's AWS sim) prove that the account-name-as-routing-fiction works end-to-end: the bucket created via ARM `BlobContainersCreate` is visible to the data-plane `azure_blob` frontend without any account-level state in the shim.

**Surprise during the run:** the Azure SDK refuses to send bearer tokens to non-HTTPS endpoints by default. The shim's `httptest.NewServer` is HTTP. Added `InsecureAllowCredentialWithHTTP: true` to the test's `arm.ClientOptions` — a flag azcore added in v1.21 specifically for test harnesses. Not suitable for production but exactly the right knob for the through-shim test.

End state: `make sockerless` reports **44 passing + 0 skipped** (was 43 after PR #50).

### BUG-24 reverse-direction expansion: 5 new cells (in flight 2026-05-27)

Every service family now has both cross-cloud directions covered. PR #46 added the first batch (cache, secrets, queue — all GCP→AWS). This PR fills out the remaining 5:

- **storage GCS→AWS** — `TestSockerless_E2E_GCSFrontendToAWSBackend`. Real GCS client drives shim's GCS frontend; AWS S3 backend targets sockerless AWS. Storage now has the full 6-direction matrix (every frontend × every backend).
- **pubsub GCP→AWS** — `TestSockerless_GCPPubsubFrontendToAWSBackend_RoundTrip`. Real GCP Pub/Sub topic admin API drives shim's GCP pubsub frontend; AWS SNS+SQS backend targets sockerless AWS.
- **rdbms CloudSQL→AWS RDS** — `TestSockerless_GCPCloudSQLFrontendToAWSBackend_CRUD`. Real GCP Cloud SQL admin API drives shim's GCP rdbms frontend; AWS RDS backend targets sockerless AWS.
- **functions CloudRun→AWS Lambda** — `TestSockerless_GCPCloudRunFrontendToAWSBackend_CRUD`. Real Cloud Run v2 admin API drives shim's GCP functions frontend; AWS Lambda backend targets sockerless AWS. The functions service didn't previously have any cross-cloud cell (the existing AWS Lambda cell is same-cloud); this PR adds the first.
- **apigateway GCP→AWS APIGW v2** — `TestSockerless_GCPAPIGatewayFrontendToAWSBackend_CRUD`. Real GCP API Gateway admin API drives shim's GCP apigateway frontend; AWS APIGW v2 backend targets sockerless AWS.

Each cell follows the same pattern as PR #46's reverse cells: GCP SDK client with `option.WithEndpoint(shim.URL)` + `option.WithTokenSource(gcpStaticTokenSource{token: gcpHS256Bearer(t, audience)})` to satisfy the shim's gcpbearer middleware in test mode (HS256 with shared test key); AWS client pointed directly at sockerless's AWS endpoint with static credentials and `AWS_S3_CONFORMANCE_INSECURE_TLS=1` honored for the self-signed cert.

Storage was the outlier — its GCS frontend uses `gcpbearer.TestJWT` minted inside `newGCSClient`, not the per-file `gcpHS256Bearer` helper. Mirrors what the other GCS sockerless cells already do.

**Two surprises during the run:**

- `apigwapi.ApiGateway` doesn't exist as a Go SDK type — the real name is `ApigatewayApi`. Caught by `go build`; fixed before push.
- The rdbms helper `newSockerlessRDSClient` didn't honor `AWS_S3_CONFORMANCE_INSECURE_TLS=1`. Forward cells didn't need it because they point the RDS client at the shim's HTTP test server; this reverse cell points it at sockerless's HTTPS endpoint, surfacing the TLS gap. Solved inline (don't use the helper for this cell) rather than touch the helper, to minimize blast radius. Worth refactoring later — the same TLS check exists 3-4 times across services.

End state: `make sockerless` reports **43 passing + 0 skipped** (was 38 after PR #49).

### BUG-35 closure + PR #47 narrative bookkeeping (PR #48, in flight 2026-05-27)

Two threads bundled because both are doc-adjacent:

- **BUG-35 closed by sockerless PR #245.** The upstream maintainer landed a single PR that addressed both #243 (Azure ARM endpoint emission across non-Storage services — endpoints now derived from request host) and #244 (Container Apps `Architecture: "linux/arm64"` hardcode — now derived from the resolved image manifest, sidecars included). After bumping the local sockerless clone past `b056d8d` and rebuilding the Azure sim binary, `scripts/run-sockerless-storage.sh` re-defaults `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE=docker.io/library/nginx:alpine` (same as the Cloud Run default — both now work on any host arch). `make sockerless` reports **38 passing + 0 skipped** (was 37 + 1 skip).
- **PR #47 doc-narrative folded forward.** PR #47 (Phase 13.A.6) didn't include its own continuity-doc narrative in-diff; this PR closes that debt for the second time in two PRs (PR #47 caught up PR #46's debt; this PR catches up PR #47's). The lesson is in WHAT_WE_DID.md's PR #47 section: subsequent work PRs should write the WHAT_WE_DID entry *before* pushing, so the narrative ships in the same commit. If this pattern keeps repeating, the rule needs to graduate to a pre-commit hook.

### Phase 13.A.6 — `azure_blob` full handler migration (PR #47, merged 2026-05-27)

The last `◐` in `DO_NEXT.md § Phase 13.A`. After this PR every Azure frontend in the shim implements `gen.ServerInterface` directly (with a `var _ gen.ServerInterface = (*Server)(nil)` compile-time gate), not just via blank import.

`azure_blob` is the largest by surface area — 69 gen operations vs ~6-66 for the other Azure frontends. The migration retains the existing hand-written query-discriminated `ServeHTTP` dispatcher (Go 1.22's `ServeMux` can't dispatch on `?restype=container` or `?comp=list`); it adds the gen.ServerInterface methods as a parallel surface, with 12 in-intersection methods bridging to the existing handlers and 57 returning the Azure error envelope via `notImplemented` (HTTP 501, `x-ms-error-code: OperationNotSupported`, XML body). Pattern matches `azure_servicebus` from Phase 13.A.4.

**In-intersection mapping (12):** `ServiceListContainersSegment` → `listContainers`; `ContainerCreate`/`GetProperties`/`Delete` → `createContainer` / `getContainerProperties` / `deleteContainer`; `ContainerListBlobFlatSegment` + `ContainerListBlobHierarchySegment` → `listBlobs` (the hierarchical variant just sets a delimiter); `BlockBlobUpload`/`BlobDownload`/`BlobGetProperties`/`BlobDelete` → `putBlob` / `getBlob` / `headBlob` / `deleteBlob`; `BlobStartCopyFromURL` + `BlobCopyFromURL` → `copyBlob` (the shim treats async and sync copy identically because it doesn't expose async operation polling).

**Out-of-intersection stubs (57):** lease ops (Acquire/Break/Change/Release/Renew Lease × Blob + Container); page-blob ops (Create, Clear/Upload/UploadFromURL/GetRanges/GetRangesDiff/Resize/CopyIncremental/UpdateSequenceNumber); append-blob ops (Create/AppendBlock/AppendBlockFromUrl/Seal); block staging (StageBlock/StageBlockFromURL/GetBlockList/CommitBlockList — the shim's multipart support runs through the *backend* directly to its destination cloud, not through frontend block-staging); tags (Get/Set); snapshots; tier; service-level (Properties/Stats/AccountInfo/UserDelegationKey/SubmitBatch/FilterBlobs); ACLs (GetAccessPolicy/SetAccessPolicy); container-level batch/filter/rename/restore/metadata; immutability + legal hold; expiry; HTTPHeaders; undelete; Query.

Three tests pin the contract: a compile-time assertion (interface satisfaction), an in-intersection bridge test (PUT container + GET list via `ServeHTTP`), and an out-of-intersection envelope test (`BlobSetTier` direct call → 501 + `OperationNotSupported` XML body).

This PR also folded forward the continuity-doc updates that should have shipped inside PR #46. PR #46 closed Phase 14.B + 14.C without updating STATUS / DO_NEXT / WHAT_WE_DID in its own diff — a continuity-rule violation caught only after merge. Subsequent PRs should update the docs inside the same PR that does the work.

End state: `make sockerless` still **37 passing + 1 documented-skipped**. Storage + sockerless conformance unaffected (the migration is pure spec-drift-contract refactor; ServeHTTP routing is unchanged).

### Phase 14.B closure (PR #46, merged 2026-05-27)

Single PR bundling four threads — the user's "don't be sneaky! bundle everything that was planned as part of the first PR" rule applied to the 3-PR closure plan from PR #45.

**1. BUG-35 — Container Apps pre-pull plumbing.** Extended `scripts/run-sockerless-storage.sh` to pre-pull `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` / `SOCKERLESS_GCP_CLOUDRUN_IMAGE` (defaults to `docker.io/library/nginx:alpine` for Cloud Run; Container Apps stays unset by default) pinned to the host arch via `go env GOARCH`. Cloud Run worked immediately. Container Apps surfaced `simulators/azure/containerapps_apps.go:444` hardcoding `Architecture: "linux/arm64"` regardless of pulled image platform — filed [sockerless#244](https://github.com/e6qu/sockerless/issues/244); shim-side plumbing is correct, the lane skips by default on amd64 CI until the upstream fix lands.

**2. GCP Cloud Run sockerless lane.** Added `TestSockerless_GCP_CloudRun_CRUD` against `services/functions/backends/gcp` using `runapi "google.golang.org/api/run/v2"` with `option.WithEndpoint` + `option.WithoutAuthentication`. Bare-minimum CRUD against sockerless's Cloud Run handler.

**3. Reverse-direction through-shim cells (BUG-24).** Three exemplars added — cache, secrets, queue — all GCP→AWS direction (the existing cells were AWS→GCP). Each test mints an HS256 bearer with audience matching the service's published audience (`https://secretmanager.googleapis.com/`, `https://pubsub.googleapis.com/`, etc.) signed with the shared test key, and feeds it through the gcpbearer middleware to the AWS-backed handler. Inline `gcpHS256Bearer(t, audience)` helper + `gcpStaticTokenSource{token}` implementing `oauth2.TokenSource` per file.

**4. 14.C — all 7 GCP frontend handler migrations.** Retired the `regexp.MustCompile` dispatch table from `internal/{functions,cache,queue,pubsub,storage,rdbms,apigateway}/frontends/gcp_*/server.go`. New shape: `strings.CutPrefix` to strip the version prefix → `strings.Split` → `segs[N]` inspection → optional `IndexByte(':')` to peel action suffixes. The reference pattern is `gcp_secretmanager` from PR #21. Per-frontend nuance:

- `gcp_memorystore` reuses the existing `stripVersionPrefix` for `/v1` vs `/v1beta1`.
- `gcs` got new `stripGCSPrefix` + `splitBucketObject` helpers — bucket+object addressing isn't a flat `projects/{p}/...` walk like the others.
- `gcp_cloudsql` got `stripSQLPrefix` for the two coexisting prefixes (`/v1` SDK + `/sql/v1beta4` Terraform).
- `gcp_apigateway` pins APIs collection to `locations/global`.
- `gcp_pubsub` (queue + pubsub sides identical in shape) uses `IndexByte(':')` to split entity from action suffix (e.g. `subscriptions/sub-x:pull`).

The `regexp` import is fully retired from each frontend. Existing `TestGCPRoutes_*_FrontendDispatchCoverage` tests pin behaviour — migration is a pure refactor, mechanically validated.

**What didn't make this PR: 14.E cross-cloud Apply cells.** Initially in scope. During the audit I realised 14.E for Azure-source cells needs the *shim* to grow ARM-shimming (`Microsoft.Storage/storageAccounts`, `Microsoft.Cache/Redis`, `Microsoft.DBforPostgreSQL/flexibleServers`, etc.) before sockerless can be on the destination side. sockerless#243 was the analogous question on the sockerless side — the maintainer reframed it to require real data planes per Azure service, which would be sockerless internal architecture (not shimanism's critical path). 14.E re-opens once shim-side ARM-shimming exists; that's separate from this closure PR.

**Upstream sockerless story.** Two issues filed during this PR:

- [sockerless#243](https://github.com/e6qu/sockerless/issues/243) — Azure ARM endpoint emission consistency. Originally framed as "rewrite the ARM strings for 5 services"; the maintainer reframed to "endpoint emission must be paired with real data-plane implementation, all Azure-shaped." Service Bus can land standalone (real data plane already exists via PR #231). Redis/PG/APIM/Container Apps need real data planes first — sockerless internal architecture, not shimanism's critical path.
- [sockerless#244](https://github.com/e6qu/sockerless/issues/244) — Container Apps `linux/arm64` hardcode. Blocks BUG-35 closure on amd64 CI runners.

Earlier review follow-ons (sockerless#239 validation, #240 redundant clone, #241 write-guard) all closed via sockerless PR #242.

**CI surprises during PR #46.**
- First failure: `docker pull nginx:alpine` selected amd64 manifest on ARM dev → fixed via `--platform=linux/$(go env GOARCH)`.
- Second failure: same pull-platform mismatch on amd64 CI, but root cause was sockerless's Container Apps handler hardcoding arm64 (not the pull). Worked around by leaving `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` unset by default — existing `t.Skip` triggers.
- Third failure: transient github.com 502 cloning sockerless during the workflow's setup step. Retry-rerun resolved it. Pure infrastructure flake.

End state: **37 passing + 1 documented-skipped**.

### Storage CopyObject sockerless lanes (PR #44, merged 2026-05-27)

Closed BUG-37 + BUG-38 by adding three CopyObject lanes — `TestSockerless_AWS_S3_Copy`, `TestSockerless_GCS_Copy`, `TestSockerless_Azure_Blob_Copy` — that exercise the shim's `domain.Storage.CopyObject` code path against the new sockerless surfaces from sockerless PR #235 (Azure Blob `x-ms-copy-source`, GCS `rewriteTo`/`copyTo`, GCS list lex-order). The storage matrix is now complete 3×3 (single-shot + multipart + copy across AWS S3 + GCS + Azure Blob).

The upstream flow was driven by a tight loop: I'd file an upstream sockerless issue with full repro + suggested fix; the maintainer reframed each through the "public-surface fidelity" principle (the sim should expose what real cloud exposes, not what's convenient for any specific caller); the maintainer landed PRs that often re-scoped my acceptance criteria to match real cloud contracts more precisely. Specifically:

- **sockerless#223 → PR #225** added the Service Bus namespace-level ATOM XML admin protocol (the protocol `azservicebus/admin` actually speaks). Unblocked BUG-34 (closed in PR #39).
- **sockerless#228 → PR #229** added AMQP-over-WebSocket. I initially proposed this but then realized using `azservicebus.ClientOptions.NewWebSocketConn` would leak WebSocket-dial code into test layer — violating the "test driver is the cloud SDK" principle. Declined the WebSocket path in shim and filed **sockerless#230** asking for raw AMQP/TLS.
- **sockerless#230 → PR #231** added raw AMQP-over-TCP/TLS with namespace routing from TLS SNI / AMQP Open hostname and entity routing from AMQP link source/target addresses. The maintainer reframed the issue from "test convenience" to "Service Bus public-surface fidelity." Unblocked the shim's SB Send/Receive lanes via `azservicebus.ClientOptions.CustomEndpoint` + `TLSConfig` — same SDK-clean shape as every other 14.B lane. Closed BUG-36 (PR #42).
- **sockerless#232 → PR #235** added Azure Blob Copy Blob via `x-ms-copy-source`. Closed BUG-37 (PR #44).
- **sockerless#233 → PR #235** added GCS `rewriteTo` + `copyTo`. Closed BUG-38 (PR #44).
- **sockerless#234 → PR #235** made GCS `objects.list` return objects in lexicographic order (real GCS contract). CI surfaced this as a flaky failure in the shim's GCS multipart lane — the shim was relying on the documented order — and the shim's `CompleteMultipartUpload` was also patched to build the part-object list from the caller's explicit `PartNumber` sequence (defensive: removes the dependency on listing order entirely).

A pattern emerged in PR reviews: review observations get filed by the maintainer as separate trackable issues. sockerless#236/#237 came from the PR #235 review (destination metadata + persistence helper) and were closed by PR #238. sockerless#239/#240/#241 came from the PR #238 review (field validation, redundant clone, write-guard) and remain open as internal sockerless improvements that don't block any shim lane.

The architectural principle that kept proving itself across the cluster: **the shim's test driver is the cloud SDK / CLI / Terraform provider; transport beneath that is the SDK's business**. When integrating against any cloud sim, the shim uses only the SDK-public configuration knobs — `arm.ClientOptions.Cloud.ResourceManager.Endpoint`, `admin.ClientOptions.Transport`, `azservicebus.ClientOptions.CustomEndpoint` + `TLSConfig`, `BaseEndpoint` for AWS, `STORAGE_EMULATOR_HOST` for GCS — and never writes protocol code in tests. This is what makes the lanes both real-cloud-compatible and sim-correct simultaneously.

End state after PR #44: `make sockerless` reports **33 passing + 1 documented-skipped** (the documented-skipped is Container Apps, BUG-35 — pre-pull plumbing, planned for the next PR).

### Storage multipart sockerless lanes (PR #41 + #43, merged 2026-05-26 / 27)

Two-step storage multipart matrix completion. PR #41 landed the Azure Blob multipart lane against sockerless's new block-blob staging (`?comp=block`, `?comp=blocklist`, `?comp=blocklist&blocklisttype=…`) added in sockerless PR #229. PR #43 added the AWS S3 + GCS Compose-based multipart lanes — both surfaces already implemented in sockerless, just not previously exercised by the shim.

PR #41 also declined the AMQP-over-WebSocket SDK path that PR #229 added (architectural reason captured above) and filed sockerless#230 for raw AMQP/TLS.

PR #43's GCS multipart lane was flaky in CI — caught the GCS list-order bug (sockerless#234) that prompted the shim's defensive refactor of `CompleteMultipartUpload`.

### Azure Service Bus AMQP Send/Receive lanes (PR #42, merged 2026-05-27)

After sockerless PR #231 added raw AMQP/TLS, two new shim lanes:

- `TestSockerless_Azure_ServiceBus_Queue_SendReceive` — CreateQueue (admin, ATOM XML) → SendMessage (AMQP/TLS) → ReceiveMessages → DeleteQueue.
- `TestSockerless_Azure_ServiceBus_Topic_PublishReceive` — CreateTopic + CreateSubscription (admin) → Publish (AMQP/TLS) → Receive.

Same SDK-clean integration shape as the admin lanes: `azservicebus.ClientOptions.CustomEndpoint` + `TLSConfig`. Zero protocol code in test layer.

`scripts/run-sockerless-storage.sh` extended to set `SIM_SERVICEBUS_AMQP_LISTEN_ADDR=:14570` when starting the Azure sim and export the corresponding `SOCKERLESS_AZURE_SB_AMQP_PORT` for tests.

Closed BUG-36.

### Azure Service Bus admin lanes (branch `phase-14b-azure-servicebus-lanes`, 2026-05-26)

Follow-on to the ARM-lanes PR. Two new green sockerless lanes against sockerless's brand-new namespace-level ATOM XML admin protocol:

- `TestSockerless_Azure_ServiceBus_Queue_CRUD` — admin-only Create / SetAttributes / Head / List / Delete via `azservicebus/admin`.
- `TestSockerless_Azure_ServiceBus_Topic_CRUD` — admin-only CreateTopic / CreateSubscription / ListTopics / ListSubscriptions.

Pattern. Each Azure SB backend (`services/{queue,pubsub}/backends/azure`) gained two optional fields on `Config`: `AdminClientOptions *admin.ClientOptions` and `DataClientOptions *azservicebus.ClientOptions`. Production callers pass nil; the sockerless tests pass an `AdminClientOptions` with a transport that dials `127.0.0.1:<sim-port>` regardless of host, with `InsecureSkipVerify` for the self-signed TLS. The connection string is `Endpoint=sb://test-ns.servicebus.windows.net/;…`; the Host header survives the dial rewrite so sockerless's `*.servicebus.*` host dispatcher parses the namespace prefix.

AMQP data plane (SendMessage / ReceiveMessage on the queue side, Publish / Receive on the pubsub side) is **not** in the lane. Sockerless implements the REST data plane but not AMQP; the shim's `azservicebus` data client speaks AMQP. The admin lanes prove the management surface; AMQP is real-cloud or future-sim territory.

Caused BUG-34 to close. End state: `make sockerless` reports 25 passing + 1 documented-skipped (Container Apps, BUG-35 still open for pre-pull plumbing). The previously filed [sockerless#223](https://github.com/e6qu/sockerless/issues/223) closed in [sockerless PR #225](https://github.com/e6qu/sockerless/pull/225); [sockerless PR #226](https://github.com/e6qu/sockerless/pull/226) merged alongside adding Storage data-plane SDK coverage that doesn't unblock new shim lanes but strengthens the existing Azure Blob coverage.

### Azure ARM backend-adapter lanes (PR #38, merged 2026-05-26)

Pushing past the AWS+GCP-only sockerless coverage, this slice wired three new Azure ARM backends through the sim:

- `TestSockerless_Azure_Cache_Redis_CRUD` — `armredis/v3` against `Microsoft.Cache/Redis`.
- `TestSockerless_Azure_RDBMS_PostgreSQL_CRUD` — `armpostgresqlflexibleservers/v4` against `Microsoft.DBforPostgreSQL/flexibleServers`.
- `TestSockerless_Azure_APIGateway_APIM_CRUD` — `armapimanagement/v3` against `Microsoft.ApiManagement/service` + `apis`. The test pre-creates the parent Service via the SDK (matching how real users provision APIM via Terraform / ARM template) before invoking the shim backend.

Pattern. Each affected Azure backend (`services/{cache,rdbms,apigateway,functions}/backends/azure/`) gained an optional `ClientOptions *arm.ClientOptions` field on `Config`, forwarded into the SDK factory. Production callers pass nil; the sockerless tests pass a value that overrides `cloud.ResourceManager.Endpoint` to point at the local sim plus an `InsecureSkipVerify` transport that dials `127.0.0.1:<port>`. Tokens use a `noOpCredential` because the sim's `AzureAuthMiddleware` passes through unverified Bearer tokens.

Two surprises along the way, both filed upstream before any workaround attempt:

- **Azure Service Bus admin: wrong protocol family in the sim.** I started by trying to wire `services/queue/backends/azure` (and the parallel pubsub backend) — they use the `azservicebus/admin` SDK, which speaks the namespace-level ATOM XML admin protocol at `<namespace>.servicebus.windows.net` (`PUT /<queue>?api-version=…` with `application/atom+xml;type=entry`, plus `GET /$Resources/Queues`). Sockerless's Azure sim implements (a) the ARM management API for `Microsoft.ServiceBus/namespaces/queues` and (b) the REST data plane at `*.servicebus.*` hosts (`POST /<queue>/messages`), but **not** the ATOM XML admin protocol. The admin SDK's calls fall through to the data-plane handler and 404. Filed as [sockerless#223](https://github.com/e6qu/sockerless/issues/223) (BUG-34); the Azure SB queue + pubsub lanes wait on that closing.
- **Container Apps actually runs containers.** First Container Apps test failed with `ContainerAppRevisionFailed: no such image`. I initially mis-filed this as a sim bug ([sockerless#224](https://github.com/e6qu/sockerless/issues/224)), thinking ARM operations should be control-plane only — but the user clarified that sockerless intentionally does real execution with the underlying runtime opaque to the caller, matching real Azure (which also tries to pull images and fails the same way if they're unreachable). Closed #224 as not-a-bug, marked `TestSockerless_Azure_Functions_ContainerApps_CRUD` default-skipped, opt-in via `SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE` for now (BUG-35). Resolution path is either pre-pulling in the run script or asking upstream for a no-op-image mode — both follow-ons.

End state: `make sockerless` runs 23 passing + 1 documented-skipped tests (up from 21). 14.C handler migrations and 14.E cross-cloud cell expansion stay deferred — the existing `TestGCPRoutes_*_FrontendDispatchCoverage` tests already pin the regex dispatchers' shape so the migration is cosmetic, and 14.E becomes easier once these new lanes exist as targets.

### End-to-end-walkthrough fidelity-bug cluster (PR #37, merged 2026-05-26)

Walking [docs/end-to-end-examples.md](docs/end-to-end-examples.md) against sockerless after PR #36 surfaced four wire-fidelity gaps — filed as GitHub #32-#35 and as BUG-30..33 — that the maintained test lane had missed because the scripts only exercise `mb`/`cp`/`rm`/`rb`. None of the four changed semantics; all were emit-side mistakes the spec already covered.

- **BUG-30 / #32 — AWS S3 codegen mis-named every `@xmlFlattened` list element.** The emitter at `internal/codegen/emit/emit.go:626` was, on encountering a flattened list, overwriting the correctly-resolved outer member name with the inner list-target's local shape name. So `ListObjectsV2Output.Contents []Object` rendered with `xml:"Object,omitempty"` — and the AWS SDK's parser, expecting `<Contents>`, silently produced zero rows. Per the [Smithy XML protocol](https://smithy.io/2.0/spec/protocol-traits.html#smithy-api-xmlflattened-trait) the outer member name wins, with the inner member's `@xmlName` overriding only when explicitly set. The fix is six lines; regeneration produced six corrections at once in `aws_s3.gen.go` — `Contents`, `CommonPrefixes`, `Part` on `CompletedMultipartUpload.Parts`, `Upload` on `ListMultipartUploadsOutput.Uploads`, and `Rule` on the lifecycle + replication configs. One emitter fix, all sites at once — the spec-as-source-of-truth payoff exactly as `docs/codegen.md` describes.
- **BUG-31 / #33 — GCS frontend leaked the backend's Azure region** as the `location` field (e.g. `"EASTUS"`, not a valid GCS location). The frontend was doing `strings.ToUpper(backend.Region)` unconditionally. Fixed with a new `gcsLocation()` helper that keeps GCS-shaped values as-is and folds non-GCS values into the default `"US"` multi-region. The zero-`timeCreated`-on-list half of the same issue traced upstream to sockerless's `handleListContainers` omitting the per-container `<Properties>` block — filed as [sockerless#220](https://github.com/e6qu/sockerless/issues/220) and closed by sockerless PR #221 the same day. With the upstream fix in place the shim's existing `c.Properties.LastModified` read path now produces real timestamps end-to-end, and the standalone E2E script asserts a non-zero `creation_time` to guard the regression path.
- **BUG-32 / #34 — Azure Blob list ETag handling was inconsistent.** Upload and download returned `"<hex>"` (quoted); blob list returned `<hex>` (unquoted, breaks `If-Match` round-trips); container list returned an empty string. The blob list path called `strings.Trim(o.ETag, "\"")` once and never re-quoted; centralized in a `quoteETag()` helper. The empty-container-ETag half had the same upstream root cause (sockerless#220) — once sockerless PR #221 added the `<Properties><Etag>` emission, the shim added an `ETag` field to `domain.Bucket`, plumbed it through the Azure backend's `ListBuckets` + `HeadBucket`, and dropped the synthetic `"shim"` placeholder in the frontend. Backends with no per-container ETag concept (AWS S3) honestly emit empty rather than fake a value.
- **BUG-33 / #35 — Doc + conformance-coverage gap.** `docs/end-to-end-examples.md` told readers to `az storage blob download --file -`, which the Azure CLI treats as a literal file path (writing a file named `-` to `cwd`) rather than streaming to stdout — so the implicit "I verified the content matched" was unverifiable. The example now writes to a real path and `cmp`s it. The standalone-E2E script gained one wire-fidelity assertion per route: `aws s3 ls` must list a key after upload (would have caught BUG-30 in CI), the GCS `location` must be a valid GCS location (BUG-31), and the Azure blob-list ETag must be quoted (BUG-32). All three assertions failed against pre-fix code and pass post-fix.

The root cause across the cluster is the same: the standalone E2E script only exercised the happy path of mutation operations and never read state back through the wire shapes it had written. The four fixes shipped together with the test-coverage extension that would have made them fail loud.

**14.B docs + through-shim sockerless E2E branch.** The `phase-181-e2e-docs-and-shims` branch switches the user-facing examples from scattered snippets into [docs/end-to-end-examples.md](docs/end-to-end-examples.md): start from real cloud credentials or from sockerless, then drive shimanism through CLI, SDK, and Terraform provider endpoint overrides. The doc includes Terraform import-state examples and cross-cloud route tables for AWS -> GCP, GCP -> Azure, and Azure -> AWS across all eight service families.

Two local gaps surfaced and were filed before fixing:

- **BUG-23 / GitHub #23** — sockerless validation had backend-adapter coverage, but no explicit source-client -> shim frontend -> shim backend -> sockerless cross-cloud cells. Fixed for storage with `TestSockerless_E2E_AWSFrontendToGCSBackend`, `TestSockerless_E2E_GCSFrontendToAzureBlobBackend`, and `TestSockerless_E2E_AzureBlobFrontendToAWSBackend`.
- **BUG-25 / GitHub #25** — `make test` exposed a Terraform provider-cache race. Parallel Terraform tests shared package-level `TF_PLUGIN_CACHE_DIR` paths while running `terraform init`, producing provider handshake failures and checksum mismatches. Fixed by routing every Terraform working directory to its own `.terraform-plugin-cache`.

**BUG-24 / GitHub #24 remains open** to extend the through-shim sockerless pattern beyond storage to secrets, queue, pubsub, rdbms, cache, functions, and apigateway. Validation on the branch: `make sockerless`, `make test`, `make vet`, and `make build` all pass.

**14.B/14.D current state after sockerless PR #219.** The upstream simulator audit loop is clear. After the first Phase 14 commits, the user merged additional sockerless fix PRs (#200, #202, #211, #216, #219). Each time, the lane was rebuilt locally and re-probed; gaps were reopened or filed with full reproductions when fixes were partial. The current state on 2026-05-25:

- `/tmp/sockerless` is at `06ee3a5` (sockerless PR #219, merged 2026-05-25).
- [sockerless#218](https://github.com/e6qu/sockerless/issues/218) is closed; no upstream sockerless blocker is open at this checkpoint.
- `make sockerless` passes the current shim lanes: storage AWS S3 / GCS / Azure Blob plus the three through-shim storage cross-cloud E2E cells; secrets AWS Secrets Manager / GCP Secret Manager / Azure Key Vault; queue AWS SQS / GCP Pub/Sub queue; pubsub GCP Pub/Sub; apigateway GCP API Gateway.
- BUG-8 is narrowed to the hashicorp/google API Gateway Terraform leg; the GCP APIGW backend/SDK leg is green.
- BUG-15 is narrowed to the hashicorp/google Terraform state-drift question; the GCP queue backend retention PATCH/read round-trip is green.

The extra sockerless issues surfaced after the original round-3 commit were: #193-199, #201, #203-210, #213-215, and #218. The important lesson was the same as the earlier audit: a green simulator PR still needs post-merge probes because several fixes were partial on first landing (#190, #193, #196, #209, #210). PR #216 closed the last five audit items (#209, #210, #213, #214, #215); PR #219 closed the GCP Secret Manager lifecycle gap (#218).

**GCP Secret Manager lane added after upstream fix.** The next planned 14.B lane was `services/secrets/backends/gcp` against sockerless using the official `cloud.google.com/go/secretmanager/apiv1` REST client. The first probe found sockerless supports create/add/access but missed:

- `GET /v1/projects/{project}/secrets/{secret}/versions` (`ListSecretVersions`) — backend `ListVersions` returns `NoSuchSecret`.
- `PATCH /v1/projects/{project}/secrets/{secret}?updateMask=labels` (`UpdateSecret`).
- `DELETE /v1/projects/{project}/secrets/{secret}` (`DeleteSecret`).

Filed [e6qu/sockerless#218](https://github.com/e6qu/sockerless/issues/218) with curl reproduction and expected REST contracts before adding any shim test. After the user merged sockerless PR #219, rebuilt the GCP sim and added `TestSockerless_GCPSecretManager_RoundTrip`, covering CreateSecret, PutSecretValue, HeadSecret, GetSecretValue(latest + explicit version), ListVersions, ListSecrets, UpdateSecret, and DeleteSecret. No local simulator patch or shim workaround is carried.

**BUG-21 — kind CI installer failure fixed.** After pushing the GCP Secret Manager lane, PR #21's `conformance envoy` job failed before tests ran: `helm/kind-action@v1` downloaded the kind release URL without following GitHub's release redirect, then checksum validation compared the redirect body. Filed BUG-21 and added a checksum-verified `scripts/ci-preinstall-kind.sh` step before every kind-backed conformance job so the action finds the pinned kind/kubectl binaries in its expected tool-cache layout.

**BUG-22 — pre-commit gofmt drift fixed.** The rerun cleared the kind install failure, then `pre-commit` failed because `services/storage/conformance/sockerless_test.go` import ordering was not gofmt-clean. Filed BUG-22 and ran gofmt on that file.

**14.A — sockerless round-1 fixes landed.** While Phase 14's continuity docs landed on PR #20, the user shepherded sockerless PR #179 (their "Phase 173" umbrella) closing all six of our round-1 issues (#173 S3 prefix, #174 aws-chunked envelope, #175 missing ListSecretVersionIds, #176/#177/#178 missing AWS/GCP/Azure services). With the simulators rebuilt:

- Dropped the `/s3` URL workaround from `scripts/run-sockerless-storage.sh` + the test-file comment.
- Renamed `TestSockerless_AWS_BucketLifecycle` → `TestSockerless_AWS_S3RoundTrip` and added `PutObject` (non-seekable body, exercises aws-chunked) + `HeadObject` + `GetObject` with full body equality.
- Re-enabled `HeadSecret` + `GetSecretValue` assertions in `TestSockerless_AWSSecretsManager_RoundTrip` (previously skipped on the missing ListSecretVersionIds path).

`make sockerless-storage` now passes three lanes: AWS S3 full round-trip, GCS full round-trip, AWS Secrets Manager full round-trip.

**14.D — fidelity audit done.** With sockerless's now-larger surface area, ran SDK-shaped probes across every newly added service. Eight fidelity gaps surfaced and were filed (no shim references; each issue carries its own self-contained reproduction):

- **[#181](https://github.com/e6qu/sockerless/issues/181)** Azure Cache for Redis ARM route only matches capital `Redis` — lowercase (which the SDK + azurerm provider use) returns 404. Pure case-sensitivity miss; ARM is supposed to be case-insensitive.
- **[#182](https://github.com/e6qu/sockerless/issues/182)** GCP Pub/Sub `Subscription` create + get drop 5 of 7 fields on response (`messageRetentionDuration`, `retainAckedMessages`, `expirationPolicy`, `enableMessageOrdering`, `filter`). **This is the same drift shape as BUG-15.** Closing #182 likely closes BUG-15 against sockerless without real-cloud Track A.
- **[#183](https://github.com/e6qu/sockerless/issues/183)** GCP Secret Manager `ListSecrets` returns GCS-shaped 404. Root cause is a routing leak: any unhandled `GET /v1/{...}` request falls through to the GCS handler which interprets the path as `{bucket=v1}/{object=...}`. The same shape also breaks `/v1/operations` (noted in a comment on the issue).
- **[#184](https://github.com/e6qu/sockerless/issues/184)** Azure Key Vault response `id` and `kid` URLs have a duplicated host segment + `http://` scheme: `http://kv.vault.kv.vault.azure.net/...`. Real Key Vault uses `https://{vault}.vault.azure.net/...`.
- **[#185](https://github.com/e6qu/sockerless/issues/185)** Azure Key Vault key creation returns a placeholder modulus literal `"n":"sim-generated-modulus"` instead of a base64url-encoded RSA modulus. Breaks any JWKS / signature-verification integration test against the sim.
- **[#186](https://github.com/e6qu/sockerless/issues/186)** AWS SQS `CreateQueue` accepts user-set queue attributes but `GetQueueAttributes` echoes only `VisibilityTimeout` — `MessageRetentionPeriod`, `DelaySeconds`, etc. are silently dropped. Same shape as #182, different protocol.
- **[#187](https://github.com/e6qu/sockerless/issues/187)** GCP Cloud SQL `selfLink` is a relative URL (`/v1/projects/.../instances/...`). Real GCP returns `https://sqladmin.googleapis.com/v1/...`.
- **[#188](https://github.com/e6qu/sockerless/issues/188)** GCP Secret Manager `versions/latest:access` echoes the literal alias `latest` in the response `name` instead of resolving to the concrete version number. Version-tracking flows break.

14.B's current validation lane is green after the later sockerless fixes. 14.C remains pending; additional 14.B service lanes are optional follow-on work.

**14.B initial lane expansion (post sockerless PR #180).** With the round-2 fidelity gaps closed in sockerless PR #180, 4 new shim lanes landed on top of the round-1 set:

- **GCP Pub/Sub pubsub** — `services/pubsub/conformance/sockerless_test.go::TestSockerless_GCP_Pubsub_RoundTrip`. Topic + Subscription + Publish + Receive + Ack against the shim's `pubsub/backends/gcp`.
- **GCP Pub/Sub queue** — now `services/queue/conformance/sockerless_test.go::TestSockerless_GCP_Queue_RetentionRoundTrip`. The final form includes CreateQueue → SetQueueAttributes → HeadQueue and asserts `MessageRetentionSeconds = 604800`.
- **AWS SQS queue** — same file `::TestSockerless_AWS_Queue_AttributeRoundTrip`. Asserts `VisibilityTimeout` + `MessageRetentionPeriod` round-trip via `CreateQueue` Attributes → `HeadQueue`.
- **GCP API Gateway** — `services/apigateway/conformance/sockerless_test.go::TestSockerless_GCP_APIGateway_CRUD`. Exercises `CreateGateway` (with routes → triggers `DeployGateway` → Api + ApiConfig + Gateway materialize) → `DescribeGateway` → `ListGateways` → `DeleteGateway`. **The SDK leg of BUG-8 is now cleared** — the TF-provider angle remains Phase 14.D residual.

**Round-3 audit, 3 more sockerless issues filed** (all closed later):

- **[e6qu/sockerless#189](https://github.com/e6qu/sockerless/issues/189)** — GCP Pub/Sub `projects.subscriptions.patch` returns 404 (only PUT is wired). Blocks shim's `SetQueueAttributes` and the BUG-15 retention round-trip.
- **[e6qu/sockerless#190](https://github.com/e6qu/sockerless/issues/190)** — Azure Blob data plane only supports host-based dispatch; Azurite-compatible path-style URLs (the Azure SDK + azurerm provider default) return 404. Blocks the Azure Blob lane.
- **[e6qu/sockerless#191](https://github.com/e6qu/sockerless/issues/191)** — Azure Key Vault secret `id` uses request scheme; partial #184 regression. Real Azure KV always uses `https://`. Lane works under TLS sim but not HTTP.

The later audit rounds closed those blockers and added the Azure Blob + Azure KV lanes, bringing `make sockerless-storage` to 9 passing tests.

## Phase 13 — closed (PR #20 merged 2026-05-24)

13.A, 13.B, 13.C all landed on PR #20 and are covered in their per-track sections of [PLAN.md § Phase 13](PLAN.md#phase-13--full-adapter-migration--production-auth--real-cloud-track-a). The notes here cover what was surprising in the 13.D sockerless slice.

**13.D.1 sockerless storage lane.** The user redirected Track A through `github.com/e6qu/sockerless` simulators before standing up real cloud accounts — same goal (catch translation defects in the AWS / GCP / Azure backend layers), no real-cloud cost.

Six issues filed upstream as fully self-contained reports (no shim references; sockerless maintainers can pick up the repros without reading our repo) — three fidelity bugs and three missing-service rollups:

- **[e6qu/sockerless#173](https://github.com/e6qu/sockerless/issues/173) — S3 mounted under `/s3/` URL prefix.** The AWS sim registers S3 ops at `GET /s3`, `PUT /s3/{bucket}`, etc. instead of the wire-protocol root. AWS SDK / CLI / TF-provider clients with `--endpoint-url=http://localhost:4566` hit `405 Method Not Allowed` on every S3 op. Workaround: append `/s3` to the endpoint URL. The simulator's own SDK tests use this workaround (`o.BaseEndpoint = aws.String(baseURL + "/s3")`).
- **[e6qu/sockerless#174](https://github.com/e6qu/sockerless/issues/174) — `aws-chunked` envelope stored verbatim.** When the aws-sdk-go-v2 `PutObject` is called with a non-seekable body, it uses `Transfer-Encoding: aws-chunked` framing. Real S3 unwraps that server-side. The sim writes the framed bytes (chunk-size hex line + chunk body + zero-size chunk + trailing checksum header) into its object store, so subsequent `GetObject` returns the framed envelope literally instead of the payload. Reproduces with an 11-byte string ending up as a 52-byte stored object. Blocks AWS PutObject/GetObject in the storage lane; doesn't affect bucket lifecycle.
- **[e6qu/sockerless#175](https://github.com/e6qu/sockerless/issues/175) — AWS Secrets Manager missing `ListSecretVersionIds`.** The sim wires 10 SM operations but not the version-listing one. The shim's `GetSecretValue` + `HeadSecret` both call into the version-ID mapping path (the shim translates monotonic uint64 ↔ Secrets Manager's UUID `VersionId` per-request, since the shim is stateless), so both surface a 400 `UnknownOperationException` against the sim. `CreateSecret` + `ListSecrets` + `DeleteSecret` work — exercised in the lane.

And three missing-service rollups — one per cloud, each listing the services we'd want to translate against but that aren't simulated today. Each issue lists per-service suggested yield-per-LOC ordering for the maintainers:

- **[e6qu/sockerless#176](https://github.com/e6qu/sockerless/issues/176) — AWS sim:** SQS, SNS, API Gateway v1 + v2, RDS / Aurora, ElastiCache.
- **[e6qu/sockerless#177](https://github.com/e6qu/sockerless/issues/177) — GCP sim:** Pub/Sub, Secret Manager, Cloud SQL Admin, Memorystore, API Gateway.
- **[e6qu/sockerless#178](https://github.com/e6qu/sockerless/issues/178) — Azure sim:** Blob data plane (URL is advertised in ARM responses but no handlers exist), Key Vault data plane, Service Bus (ARM + simplified data plane), Database for PostgreSQL FlexibleServer, Cache for Redis, API Management.

Two additional shim-side observations:

- The SDK refuses streaming-signed payloads over plain HTTP. Sockerless ships HTTP-by-default with optional TLS via `SIM_TLS_CERT` / `SIM_TLS_KEY`. The `make sockerless-storage` script generates an ephemeral self-signed cert in `/tmp/sockerless-tls/` so the AWS sim runs under TLS; the shim's S3 client trusts it via `AWS_S3_CONFORMANCE_INSECURE_TLS=1` → `InsecureSkipVerify`.
- Azure Blob is not implemented by sockerless — only Azure Files. The Azure sim advertises blob endpoint URLs in storage-account ARM responses (`https://{accountName}.blob.localhost:4568/`), but only the `file` service type has actual data-plane handlers; everything else falls through to a mock service-properties response.

GCS was the clean lane — sockerless implements the full `/storage/v1/b/...` REST surface and the SDK's `STORAGE_EMULATOR_HOST` env driver does the right thing (`option.WithEndpoint` doesn't, because it doesn't reroute every API surface the SDK touches). Full CreateBucket → PutObject → GetObject → DeleteObject → DeleteBucket round-trip passes.

The lane is opt-in via `make sockerless-storage`; CI's existing storage matrix stays inmem/minio. See [docs/sockerless-validation.md](docs/sockerless-validation.md) for the operational doc.

**Explicit list of work deferred to follow-on PRs — bundled into Phase 14.** What this PR did *not* finish should be just as visible as what it did. All of these have been hoisted into a new Phase 14 in [PLAN.md](PLAN.md#phase-14--sockerless-verified-validation-lane--deferred-follow-ons) with explicit upstream-sockerless-issue dependencies per item.

The Phase 14 framing matters because it makes the *consumer* of sockerless's evolution explicit: sockerless's simulator implementations are what give us cross-cloud shim verification + Terraform-provider round-trip testing at CI tempo, without real-cloud cost. Every shim translation layer becomes exercisable end-to-end against a deterministic in-process sim instead of (or in addition to) a real cloud account.

| Deferred item | Lands in | Sockerless dep |
|---|---|---|
| Azure Blob full handler migration (13.A.6) | 14.C | — |
| Azure APIM full handler migration (13.A.7) | 14.C | — (waits on upstream APIM spec broadening) |
| 7 GCP frontends full migration (13.B.2-8) | 14.C | — |
| AWS S3 PutObject/GetObject round-trip in sockerless lane | 14.A.2 | [#174](https://github.com/e6qu/sockerless/issues/174) close |
| AWS Secrets Manager HeadSecret + GetSecretValue in sockerless lane | 14.A.3 | [#175](https://github.com/e6qu/sockerless/issues/175) close |
| Drop the `/s3` URL workaround | 14.A.1 | [#173](https://github.com/e6qu/sockerless/issues/173) close |
| Azure Blob sockerless lane | 14.B.3 | [#178](https://github.com/e6qu/sockerless/issues/178) close |
| Sockerless functions / queue / pubsub / rdbms / cache / apigateway lanes | 14.B.1-3 | [#176](https://github.com/e6qu/sockerless/issues/176) / [#177](https://github.com/e6qu/sockerless/issues/177) / [#178](https://github.com/e6qu/sockerless/issues/178) close |
| BUG-8 + BUG-15 closure / reclassification | 14.B.2 (preferred) or 14.D fallback | sockerless#177 (GCP APIGW + Pub/Sub portions) |
| Real-signed sigv4 / Workload Identity / Entra ID conformance | 14.D | (sockerless can't fully substitute — requires real-cloud identity issuers) |
| Cross-cloud Apply matrix expansion | 14.E | 14.B in progress |

## Phase 12 — Spec-driven toolchain landing (PR #19, at exit)

The toolchain phase. Phase 11 spec-drove all 8 AWS frontends + 24/24 frontends got signature verification. Phase 12 took the Azure + GCP lanes to the same place, with 82+ granular commits.

**Track 2.A — Azure (8/8 specs codegen end-to-end).** `cmd/azure-codegen` runs an 8-stage preprocessor before `kin-openapi/openapi2conv.ToV3` + `oapi-codegen` (see [docs/codegen-pipelines.md](docs/codegen-pipelines.md) for the stage-by-stage table). Each preprocessor stage was driven by a real spec quirk:

- **Common-types inliner** (12.A.7/8/9/10) — Azure ARM specs `$ref` shared definitions in `common-types/resource-management/v<N>/<file>.json` by relative path; `kin-openapi`'s loader refuses external refs. The inliner merges every reachable common-types file's `definitions`/`parameters` into the main spec at the v2 layer. Vendored v1–v6 common-types, taught the inliner three relative-ref forms (full path, same-version sibling, cross-version sibling), the multi-file sibling case (`./<file>.json` resolving against the spec's own dir), and the `./examples/<file>` skip.
- **`promoteXMsEnumName`** (12.A.12) — Azure spec authors use `x-ms-enum.name` to say "this inline enum IS the top-level enum of the same name." oapi-codegen ignores the extension; the inline schema gets a Go name from the property path which collides with the standalone definition. The preprocessor rewrites the inline to a `$ref` to the top-level.
- **Parameter/header depth-gate refinement** (12.A.20) — Azure Blob's `parameters.AccessTierOptional` has `x-ms-enum.name = "AccessTier"` matching `definitions.AccessTier`; rewriting the parameter to a `$ref`-to-schema breaks v2→v3 (parameter refs must point at parameter objects). Same trap with response headers (`x-ms-copy-status` with `x-ms-enum.name = "CopyStatusType"` matching `definitions.CopyStatus`). The walker now tracks "non-schema depth" — suppressed under `parameters`/`headers` containers but reset on `schema`/`items` so body-parameter schemas still get rewritten.
- **`dedupeParameterDefNameCollisions`** (12.A.20.ii) — Blob ships both `definitions.LeaseDuration` (string enum) AND `parameters.LeaseDuration` (integer header). oapi-codegen emitted two `type LeaseDuration` declarations and failed `duplicate typename`. Preprocessor stamps `x-go-name: <N>Parameter` on the parameter.
- **`flattenARMAllOf` — BUG-20** (12.A.24) — ARM resource definitions use `{ allOf: [{$ref: TrackedResource}], properties: {own props} }`; oapi-codegen sees the 1-element allOf and emits `type X = TrackedResource` — a Go type alias that silently discards the schema's own properties. The new stage walks every top-level definition; when it has both allOf (with local $refs) AND own properties, inlines the referenced schemas' properties + required + additionalProperties into the local def, then drops the allOf. Local properties win on key collision. **Iterates until no definition changes** to handle chained inheritance (`X → allOf [Y]; Y → allOf [Z]`); the direct unit test in 12.A.31 caught + fixed the original implementation's premature `allOf`-clear that lost Z's properties through X.
- **`flattenXMSPaths`** (12.A.15) — Azure data-plane specs use `x-ms-paths` to disambiguate same-URL operations by query parameter (e.g. `/?restype=service&comp=properties` vs `/?comp=list`). OpenAPI doesn't model that; preprocessor moves entries into `paths` with the same key (path keys are opaque strings in OpenAPI; distinct query strings → distinct entries).
- **`normalizeAllOf`** (legacy from 11.4) — `kin-openapi`'s v2→v3 converter attaches empty `AllOf: []` to scalar enum schemas; oapi-codegen panics on `allOf[0]`. Nil the empty slice everywhere.
- **Deterministic walk** (12.A.12) — `sortedKeys()` everywhere; multi-version common-types merges produce byte-identical output across runs.

Every preprocessor stage has direct unit tests (`cmd/azure-codegen/main_test.go`); per-service `azure_gen_test.go` asserts ServerInterface kind + a method-count minimum; BUG-20 regression tests in each service decode + JSON-round-trip a realistic ARM body through `ContainerApp` / `RedisResource` / `Server`.

`azure_keyvault` ships fully migrated through `gen.HandlerWithOptions` as the reference impl (Phase 12.A.1/2; Phase 13.A migrates the other 7).

**Track 2.B — GCP routing emitter (8/8 services).** `cmd/gcp-codegen` reads vendored Discovery JSON and emits `Routes []Route` triples + a compiled `*regexp.Regexp` per route + `BasePath` constant + `Match(method, path)` / `MatchAll(method, path)` helpers. Per AGENTS.md #11 the wire types reuse `google.golang.org/api/<svc>/v1`; the emitter is routing-only. Per-service tests assert inventory non-empty + sorted + cross-cloud-intersection ops covered + `BasePath` sane + every route's compiled Pattern matches a template-derived sample path (413 routes total).

**Vendored-spec provenance (12.0.4–12.0.24).** JSON has no comment syntax; closest analogue is a top-level field. `cmd/inject-provenance` writes `_provenance` as the FIRST top-level key of every spec JSON (24 service + 26 common-types = 50 files), preserving source-file key ordering for everything else (encoding/json's map iteration would re-sort lexicographically and diff massively against verbatim upstream). The three fetch scripts (`scripts/fetch-{aws,azure,gcp}-*.sh`) run the injector after download. CI guards: `TestEveryVendoredSpecCarriesProvenance`, `TestProvenanceMatchesSOURCES`, `TestGenHeadersCarryProvenance` (the last surfaced a real Makefile bug — pubsub's Azure gen had a placeholder zero-SHA because the commit lookup used the manifest dir instead of the spec dir; fixed 12.A.36). `make codegen-check` runs `inject-provenance` too, so a SOURCES.md edit without re-injection trips the pre-push lane.

**Spec freshness lane (12.0.1/2/15).** `make spec-freshness` walks every SOURCES.md and reports drift against upstream HEAD. Weekly workflow (Mondays 14:00 UTC). Renovate custom manager tracks vendored-spec SHAs and opens issues when one falls behind.

**Per-service Terraform walkthroughs (12.0.12/13/14).** Every MIGRATION.md gained a copy-pasteable HCL walkthrough — `provider "aws" { endpoints { <svc> = ... } }` against a non-AWS backend; storage also got the symmetric `provider "google" { storage_custom_endpoint = ... }` against a non-GCS backend. HCL pulled from each service's `cross_cloud_import_test.go` fixture so the example is real, not invented.

**Exit criteria all met.** (1) `TestCrossCloudApply_Roundtrip_<svc>_<cell>` exists + passes for all 8 services. (2) 8/8 Azure + 8/8 GCP frontends have gen files compiling + imported by per-service smoke tests. (3) Every MIGRATION.md carries a copy-pasteable Terraform walkthrough. Adapter migration of the 7 remaining Azure + 8 GCP frontends → Phase 13.A/B.

## Phase 11 — Tighten the wire boundary (PR #18, merged 2026-05-22 at `bcd72e5`)

Replace hand-written wire layers with spec-driven generated stubs across every AWS-shaped frontend, and wire real signature verification at the new decode boundary. Codex review of the initial plan corrected several wrong-library assumptions (SigV4 in `signer/v4` is signer-only, not verifier; `golang.org/x/oauth2` is acquisition not verification; Azure Key Vault is Bearer-only not SharedKey; the existing Smithy emitter was REST-XML only — extending to awsJson / awsQuery / restJson1 is new emitter work, not a routing-table addition).

**Four wire-protocol emitter paths.** Pre-Phase 11 the emitter only spoke REST-XML (S3). Phase 11 added three more templates + protocol detection: **awsJson1_x** (POST `/` dispatched by `X-Amz-Target`; JSON request + response; `__type` + `X-Amzn-Errortype` envelope — powers Secrets Manager 1_1 + SQS 1_0); **restJson1** (HTTP-route dispatched same as REST-XML; JSON bodies + awsJson-shaped error envelope — powers Lambda + APIGW v2); **awsQuery** (POST `/` dispatched by `Action` form parameter; form-encoded request; XML response wrapped in `<OpResponse><OpResult>...</OpResult><ResponseMetadata>...</ResponseMetadata></OpResponse>` — powers SNS, RDS, ElastiCache). Three runtime helper packages: `internal/awsjson/`, `internal/awsquery/`, plus `internal/restxml/` shared by REST-XML and restJson1.

**8/8 AWS frontends migrated.** 3,853 LOC of hand-written wire deleted. Each migration follows the same pattern: per-service adapter implementing the generated `<Service>Backend` interface, translating each generated request type into the existing domain layer.

**Signature verification — BUG-18 closed end-to-end.** Four verifier packages wrap all 24 frontends; per-cloud detail in [docs/verifiers.md](docs/verifiers.md). Each cloud needed a specific fix to make end-to-end signed conformance work: manual SigV4 in `canonical.go` accepting both Go-SDK and boto3 signing shapes (the SDK auto-includes `Content-Length` in `SignedHeaders`, boto3 doesn't — divergence broke verification); test JWT helpers per cloud emitting well-formed HS256 tokens the verifiers accept; `azuresharedkey` uses `EscapedPath()` to match azblob SDK canonicalisation. Also surfaced + fixed during the closer: awsQuery map-shape XML marshalling (`MarshalXML` per Smithy map type emitting `<entry><key>...</key><value>...</value></entry>`); SNS GetTopicAttributes empty Policy field rejected by hashicorp/aws's IAM-policy parser (now emits canonical default policy); SNS SetTopicAttributes unconditionally called for feedback-rate/role-ARN attributes terraform-provider-aws sets on every apply (now no-ops AWS-only attributes via explicit allowlist).

**Azure oapi-codegen pilot (11.4).** Toolchain proof — vendor Azure Swagger 2.0, convert v2→v3 via `kin-openapi/openapi2conv`, run `oapi-codegen` as a library. `azure_keyvault`'s `SetSecret` decodes via `gen.SecretSetParameters`. Two upstream-tooling defects worked around inside the driver: `kin-openapi` attaches empty `AllOf: []` to scalar enum schemas (`normalizeAllOf` walks + nils); host-template `$ref` preservation (documented; not a blocker).

**Surprises (knowledge artifacts).**

- **Smithy emitter is REST-XML-shaped under the hood.** Generated handlers `import restxml`, route from `smithy.api#http` traits. awsJson / awsQuery / restJson1 each need new protocol serde at the emitter level, not "routing-table addition".
- **awsJson1_x timestamps are epoch-seconds floats, not RFC3339.** Go's default MarshalJSON emits RFC3339; AWS SDKs reject. New `awsjson.EpochTime` type with custom MarshalJSON.
- **awsQuery list element names differ per spec.** SNS uses `<member>` (protocol default); RDS / ElastiCache use `<DBInstance>` / `<CacheCluster>` (via `@xmlName` traits). Emitter respects trait when set; falls back to `<member>` for awsQuery.
- **awsQuery error envelope's outer element is awkward.** `xml.Marshal(result)` produces `<ResultType>...</ResultType>` by default; wrapping that inside `<OpResult>` produced double-nested output. `WriteResult` strips the result struct's own outer element so its fields inline cleanly.
- **APIGateway v2's multi-step deploy flow carries per-process state.** AWS splits gateway creation into Api + Routes + Integrations + Deployment; the domain has a single atomic DeployGateway. Adapter retains the pending-routes / integration-IDs map in per-process memory — documented compromise of stateless-shim (deployed routing table itself still lives in backend; only in-flight accumulation is per-process).
- **Lambda's required-Role makes cross-cloud Create-via-Lambda-SDK intersection-out.** Non-AWS backends honestly reject non-empty Role with `InvalidParameterValueException`.

**Deferrals → Phase 12 (now closed in PR #19).** Broader Azure spec-driven migration (11.4 continuation), GCP routing emitter (11.5), production RS256 JWKS (now Phase 13.C). Track-A bugs (BUG-8, BUG-15) carried to Phase 13.D.

## Phase 10 — cross-cloud `terraform apply` through the shim (PR #17, merged 2026-05-21 at `ebc30f7`)

The write-side proof, symmetric to Phase 9's read-side. A user writes AWS-shape Terraform; `terraform apply` creates / updates / destroys the resource in cloud B through shimanism, with the source-cloud provider unaware of the translation. Eight BUGs closed; all 8 services have active drift assertions; full developer + contributing docs under `docs/`.

### What closing BUG-2 actually required

BUG-2 (queue `SetQueueAttributes`) had carried through 5 phases. The cycle of failed closes followed a recurring shape: someone added `SetQueueAttributes` to a backend, the provider's `WaitForStateEqual` after CreateQueue still timed out, the work got shelved. The Phase 10 close took 4 distinct moves:

1. **Domain extension.** `domain.Queues` gained `SetQueueAttributes(name, QueueAttributes)`. Zero-valued fields = "leave unchanged" (AWS-merge semantics; same shape as `UpdateSecretOptions` from BUG-17).
2. **Per-backend honest implementations.** inmem patches in place; AWS calls `SetQueueAttributes`; GCP `subscriptions.patch` (only honors ackDeadline + retention, others ignored as documented); Azure `GetQueue → UpdateQueue` read-modify-write; NATS `UpdateStream` + `UpdateConsumer`.
3. **Read-side attribute surface.** `attributesToAWS` extended to emit all the AWS-specific attribute keys the hashicorp/aws provider sets schema-defaults for (`Policy`, `RedrivePolicy`, `KmsMasterKeyId`, `FifoQueue`, etc). Empty/zero values for out-of-intersection attributes — honest defaults representing "no extra features configured," not fabricated state.
4. **awsQueryCompatible legacy error codes.** The `x-amzn-query-error` header maps the new Smithy error codes to their legacy Query-XML equivalents (notably `AWS.SimpleQueueService.NonExistentQueue` for `QueueDoesNotExist`). hashicorp/aws's wait functions are keyed on the legacy codes; without the header they'd treat a delete-confirmation 404 as an unrecoverable error.

The same shape — domain extension + per-backend impl + read-side surface + error-code compatibility — applied to BUG-13 (functions Role/Publish), BUG-16 (rdbms paths/defaults), and BUG-17 (secrets UpdateSecret/TagResource). Phase 10.3 is the methodical application of this template across the open Apply-blocking BUGs.

### Cross-cloud asymmetries: documented, not faked

Phase 10.7's storage cell (`TestCrossCloudApply_Roundtrip_StorageAWStoGCS`) proves the cross-cloud Apply headline. The other six services document specific cross-cloud asymmetries that make a single-PR close infeasible:

- **secrets AWS→Azure:** AWS Secrets Manager's CreateSecret accepts value-less creates; Azure Key Vault genuinely doesn't (SetSecret is the only create path and requires Value). The provider's separate `aws_secretsmanager_secret` + `aws_secretsmanager_secret_version` resources mean the user can't seed a value at create time through HCL.
- **queue/pubsub AWS→GCP:** hashicorp/aws's `WaitForStateEqual` after CreateQueue + SetQueueAttributes expects all SQS-shape attributes to round-trip exactly. GCP Pub/Sub honors visibility_timeout_seconds + message_retention_seconds; DelaySeconds + MaxMessageSize don't have GCP analogs.
- **cache/rdbms/functions/apigateway:** AWS-shape Apply requires post-create reconcile state (parameter-groups, subnet-groups, LayerVersions, multi-step Create) that GCP's equivalent services don't surface.

Each is honest cross-cloud behavior — not a shim bug. The destinations genuinely don't have the source's concepts. Real migration tools handle these via fixture-side workarounds + identity/networking rebinding on the destination; that's the Track A follow-on.

### Codex review pass

Two codex reviews ran against the PR — one on the docs, one on the code. Five P2 silent-fallback / consistency fixes landed in response (SNS SetTopicAttributes value-aware reject, GCP queue SetQueueAttributes honest-reject for unsupported attrs, CreateQueue tags compensating-delete rollback on tag failure, AWS secret UntagResource diff-based reconciliation, Lambda Role/Publish honest-reject on non-AWS backends + matching matrix test fixture update). The honest-reject of Role on non-AWS backends then forced an alignment of the Knative conformance test — `TestInvokeConnectivity_Knative` now creates the function via the backend directly (AWS Lambda SDK's required-Role contract is intersection-out for non-AWS backends).

### Closing the philosophical loop

Phase 10 makes shimanism a **cross-cloud IaC control-plane migration tool** — not a full migration tool. Data movement, secret value-history transfer, DB snapshots/replication, cache warmup, queued-message drain, function artifact transfer, IAM rebinding, DNS swap — those remain user responsibilities (or other tools' jobs). `docs/migration.md` documents this scope explicitly.

## Phase 10.1 — close BUG-5 (stateless GCP `Operations.Get`)

Carried since Phase 5. Codex's Phase 10 review flagged it as the hard gate for Phase 10 — `terraform apply` against GCP-shape frontends hangs without long-running-operation polling, so it has to land *before* any Apply cell runs. PR #16 closed it across all four GCP-shape frontends in one sweep: rdbms (Cloud SQL Admin) `/v1/projects/{p}/operations/{op}`, cache (Memorystore) and apigateway (API Gateway) `/v1/projects/{p}/locations/{l}/operations/{op}`, functions (Cloud Run) `/v2/projects/{p}/locations/{l}/operations/{op}`.

**Stateless polling via Name-encoded target.** The Operation `Name` encodes `(opType, target)`. A polling client GETs the operation; the shim parses the name, looks up the underlying resource, and maps its current `domain.Status` to RUNNING / DONE. For delete ops, `NoSuchResource` signals DONE. **No shim-side operation table** — every poll re-derives status from the backend's actual state. `Operations.List` returns empty: there's no honest way to enumerate past operations without state, and SDK polling paths only call `Get`. Documented as intentional, not a gap.

## Phase 9 — cross-cloud `terraform import` (PR #13 + PR #16)

The read-side proof. The thesis: if the shim is honest, then `terraform import` against an A-shaped HCL pointing at a backend cloud B should round-trip — `terraform plan` after import sees no drift, because the shim translates the B-side state back into the A-side shape with full fidelity.

### `shimctl env` and the endpoint-override registry

Migration users don't write endpoint-override boilerplate by hand. Sub-phase 9.1 added `shimctl env`, which prints the env-var / SDK / CLI / Terraform overrides needed to route a given (cloud, service) pair through the shim. The registry lives at `internal/clientconfig/overrides.yaml` and enumerates the per-cloud override knobs the official tooling actually exposes. This is what makes the migration story runnable from a README.

### The per-service `INTERSECTION.md` audits

Every wire-level operation each frontend serves got classified into one of three categories: (1) real work — must dispatch to a real backend call; (2) feature genuinely unset — returns the cloud's real "unset" envelope (e.g. `NOT_FOUND` for an absent sub-resource); (3) out of intersection — returns the cloud's real "not supported" envelope. A fourth implicit category — "returns something plausible without doing real work" — is by definition a fake and got filed as a bug or removed.

This audit surfaced **three real fidelity gaps that had been hiding under matrix-test passes**: GCP API Gateway frontend missing the `Apis` + `ApiConfigs` endpoint families entirely (BUG-9), Azure APIM frontend missing the `Operations` subresource (BUG-10), and AWS APIGW v2 frontend's 404 envelope missing the `__type` field (BUG-11). All three got fixed in Phase 9.

### Six real fidelity fixes surfaced by the import tests

Phase 9.5 wasn't just test-writing — every service's import driver found something: XML double-nesting in restxml responses (AWS frontend marshalling); missing Policy JSON sub-resource; missing tag-list handlers (category-2 honest-empty); missing selection-expression defaults (apigateway); missing Lambda subresources; missing RDS ARN. Each got filed as a bug and fixed inline. **No fakes survived** — the import path doesn't pass until every Read the provider issues has an honest answer.

### Docs roll-up lesson

PR #13 squash-merged with all 8 services' cross-cloud import tests on tree, but the closer commit that updated phase docs from "six services" to "all 8 services" was still in flight on the branch and didn't make the squash. PR #16 fixed the doc narrative drift. **Lesson: docs-roll-up commits at the end of a multi-chunk PR are race-prone with the merge fire.** For Phase 10 onward, doc updates happen *with* each granular commit, not as a single tail.

## Phase 8 — API Gateway (PR #13 co-merged)

Control-plane shim for HTTP API gateways. Same shape as Phases 5–7 — provision + return URL, clients HTTP-request the URL — with one new wrinkle: a *set of routes* to translate to backend-native primitives that vary wildly across clouds.

- **Declarative-replace via `DeployGateway(routes)`.** AWS lets you mutate individual routes; GCP atomically deploys an OpenAPI document; Azure replaces APIM operations one at a time; Envoy Gateway swaps the HTTPRoute set. Cross-cloud "patch one route" is impossible. The intersection is **publish a full routing table atomically**.
- **restJson1 with @jsonName traits.** The Smithy spec explicitly declares @jsonName camelCase traits on every field. The aws-sdk-go-v2 client silently drops fields whose JSON tag doesn't match — no error, fields go nil. Phase 7's Lambda spec lacked these overrides, which is why PascalCase tags worked there; Phase 8 forced the issue.
- **Envoy Gateway via dynamic client.** `gateway.networking.k8s.io/v1` via dynamic client + unstructured CRs (`Gateway` + `HTTPRoute`). Each shim Gateway maps to one Envoy Gateway CR plus N HTTPRoutes labeled `shim.apigateway/gateway=<name>` so DeployGateway can atomically wipe-and-replace.
- **Exit criterion: `TestRouteServes_Envoy`.** Stands up an echo Deployment + Service, registers Gateway + Integration + Route via the AWS frontend, then HTTP-GETs the route through Envoy's Service via port-forward. End-to-end chain has no fakes; if any link breaks, the request fails.

## Phase 7 — Functions (PR #12)

Control-plane shim for container-image function deployments. Data plane is HTTP — simpler to test than PG wire protocol or RESP, but with one twist: the URL has to actually route to the deployed container, end-to-end.

- **Container image only.** ZIP-package Lambda is out of intersection. All four backends natively support container images; ZIP is AWS-specific. Cross-cloud function deployment via the shim means shipping a registry image, not a source bundle.
- **restJson1 — the fourth AWS wire protocol family in the shim.** Phases 1+2 used S3 XML / awsJson1_1. Phase 3 used awsJson1_0 (SQS). Phases 4+5+6 used awsQuery (SNS, RDS, ElastiCache). Phase 7's Lambda is **restJson1**: real REST routes with JSON bodies.
- **Events + auth-on-invoke deferred.** Cross-cloud event-source mappings (CloudWatch / EventBridge / Eventarc / Pub/Sub triggers / Event Grid) have completely different shapes; HTTP-trigger only. IAM-gated invocation deferred for the same reason.
- **Knative URL routing nuance.** The exit-criterion test (`TestInvokeConnectivity_Knative`) hits a Knative-deployed container through `kubectl port-forward svc/<name>` plus a `req.Host = <ksvc URL host>` header — bypasses the gateway's Host-based dispatch entirely. Endpoint URLs across backends: Knative `Service.status.url`; Cloud Run `Service.uri`; Container Apps `Ingress.Fqdn`; AWS Lambda emits an `aws-lambda://<arn>` placeholder (Lambda Function URLs require a separate `CreateFunctionUrlConfig` op — out of intersection).

## Phase 6 — Managed Redis (PR #11)

Near-mirror of Phase 5 — control plane only; K8s peer is Redis Operator (OT-CONTAINER-KIT) instead of CloudNativePG; exit criterion is `redis-cli PING → PONG` through the shim-returned Connection block. Mostly mechanical re-application of the Phase 5 architecture.

- **Smaller intersection.** 11-op rdbms collapses to 6 ops for cache (Create/Delete/Describe/List/Modify/Reboot Instance). Snapshot/restore deferred — cross-cloud Redis snapshot semantics are too divergent (AWS → S3, GCP → GCS export, Azure → backup containers, Redis Operator → BackupRestore CRs).
- **awsQuery thrice in a row.** ElastiCache, RDS, and SNS all use awsQuery — by the third instance, a new awsQuery frontend is essentially dispatch + struct shapes. Envelope plumbing carried over verbatim from Phase 4 + Phase 5.
- **GCP Memorystore auth differs from RDBMS.** AUTH is fetched via a separate `GetAuthString` endpoint; the shim deliberately doesn't call that on every Describe to keep the op cheap. Auth surfaces exclusively at create time, matching AWS.

## Phase 5 — Managed RDBMS (PR #10)

Control-plane only — the load-bearing shape change versus Phases 1-4.

- **The shim is invisible to every SQL statement.** Storage / Secrets / Queue / Pubsub all sit on the data path: every wire-protocol message goes through the shim. RDBMS doesn't. The shim provisions a DB instance via the cloud's control-plane API and returns a `Connection` block (host, port, master username, database name). Clients open a *direct* connection. **Exit criterion: `psql` opens a real connection to the cnpg-provisioned cluster through the shim-returned Connection block and runs `SELECT 1`.** It either works or it doesn't — no "the shim faked it" is possible.
- **Async semantics, surfaced explicitly.** DB provisioning takes minutes — the shim can't pretend to be synchronous. Explicit `Status` enum (`Creating`, `Available`, `Modifying`, `Rebooting`, `Deleting`) on every domain `Instance`; clients poll `DescribeInstance` until `Available`.
- **CloudNativePG via dynamic client (not the cnpg-api Go module).** Loose dependency on cnpg's release cadence; the trade-off is hand-coded field paths into unstructured maps. Same pattern repeated for Redis Operator (Phase 6), Knative (Phase 7), Envoy Gateway (Phase 8).
- **Master password handling.** Returned exactly once at `CreateInstance` via `CreateInstanceResult.MasterPassword`; never re-emitted on Describe. cnpg stores the password in a Kubernetes `Secret`; the shim re-reads that Secret on each `DescribeInstance` (no shim-side credential cache).
- **Filed BUG-5.** GCP Cloud SQL Admin frontend returned `PENDING` `Operation` envelopes but didn't implement the polling endpoint. Carried until Phase 10.1's stateless-`Operations.Get` fix.

## Phase 4 — Pub/Sub (PR #9)

Topic-fanout sibling of Phase 3.

- **Topic ≠ Subscription is the load-bearing change.** Phase 3's queue domain collapsed (topic, subscription) onto one Queue. Phase 4 can't: fanout needs subscriptions addressable independently (each has its own ack-deadline + delivery queue). Pubsub domain has two resource types and 12 ops. Receive is per-Subscription; Publish is per-Topic; there's no per-Topic Receive.
- **AWS dual-protocol surface.** Real AWS pub/sub is SNS for publish, SQS for receive — SNS subscriptions deliver to SQS queues. The shim's AWS frontend mirrors this: an SNS handler (awsQuery, XML) for publish, and a *slim* SQS-shaped receive frontend (no CreateQueue, no SendMessage — those don't belong on a fanout-only data plane). The `StartPubsubServerAWS` harness returns two URLs.
- **NATS JetStream throughout, not core.** JetStream streams with `InterestPolicy` retention; durable pull consumers. AWS / GCP / Azure subscriptions are *always* durable — toggling NATS to non-durable just for one knob would diverge the K8s peer from the cloud surfaces.
- **Azure's 4-part receipt handle.** `<topic>|<sub>|<messageID>|<lockToken>` so Ack + RenewLock can reconstruct the URL `/{topic}/Subscriptions/{sub}/messages/{id}/{lock}` with no shim-side state.

## Phase 3 — Queue (PR #8)

Three frontends × five backends × three driver types applied to message queueing. 8-op intersection.

- **Receipt handles are the hard part.** Each cloud emits a different opaque token; the shim is stateless, so the handle has to round-trip without a shim-side index. AWS → passes through unchanged; GCP `AckId` → passes through unchanged; NATS → receipt = reply subject (ack via `+ACK` to that subject); Azure → composite `<messageID>|<lockToken>` reconstructing the URL on ack.
- **Hybrid SDK + REST for Azure.** `azure-sdk-for-go/sdk/messaging/azservicebus` high-level receive returns a `*azservicebus.ReceivedMessage` Go object the caller must hold to call `CompleteMessage(msg)` — violates statelessness. Fix: SDK for stateless ops (Create / Delete / List / Send / Receive); raw HTTP REST + SAS-token signing for Complete + RenewLock, reconstructing the URL from the receipt handle alone.
- **GCP: `google.golang.org/api/pubsub/v1` over the streaming-first `cloud.google.com/go/pubsub`.** The high-level streaming client opens a long-lived gRPC stream and dispatches via callbacks — doesn't fit per-call REST. Discovery-generated synchronous REST SDK matches the shim's request-response shape exactly. Same package supplies wire types for the frontend too.
- **AMQP / ARM cells deferred via documented skip.** Azure SDK drives Service Bus over AMQP; the shim's Azure frontend speaks REST only. `az servicebus` CLI + Terraform `azurerm` are AMQP / ARM, skipped with rationale; raw-HTTP REST is the conformance contract.
- **Cross-cloud caps for uniformity.** VisibilityTimeoutSeconds capped at 600s (GCP max); WaitTimeSeconds capped at 20s (AWS max). Higher values would silently fail on the lower-cap backend.
- **Filed BUG-2** (SetQueueAttributes gap). Carried 5 phases; closed in Phase 10.3.

## Phase 2 — Secrets management (PR #7)

Same N × N discipline as Phase 1, smaller surface (7-op intersection). Three frontends (AWS Secrets Manager, GCP Secret Manager, Azure Key Vault) × five backends (inmem, Vault as K8s peer, AWS / GCP / Azure passthrough) × three driver types.

- **The stateless invariant landed first.** Before any code: **the shim binary holds no state of record**. Locked into AGENTS.md, PLAN.md decision #12, STATUS.md invariants, plus a PHILOSOPHY.md koan (*The Empty Hands*). The Phase 2 design then bent to comply: version-handle translation (AWS UUID ↔ monotonic uint64, Azure GUID ↔ monotonic uint64) is **derived per-request by listing versions and sorting by creation timestamp**. Earlier drafts stored the mapping in a shim-owned sidecar; the rewrite cuts that — the data already lives in the backend, the shim just re-reads.
- **Versions modeled monotonically in the domain.** AWS UUID + stage labels (`AWSCURRENT`, `AWSPREVIOUS`) → resolved inside the AWS frontend via listing. GCP / Vault → 1:1 native. Azure GUID → monotonic derived per-request.
- **Conformance surprises.** AWS ARN normaliser bug (stripped `-XXXXXX` 7-char suffix from any ARN; broke Terraform test secrets with `-`-separated names — fix: only strip on real-AWS-region ARNs, never shim-issued). `GetResourcePolicy` probe handler added (TF refreshes call it on every DescribeSecret; resource policies are IAM-side, out of intersection — probe returns canonical "no policy attached"). GCP's `:enable` / `:disable` treated as no-op probes (no per-version enabled state in the cross-cloud intersection). Azure SDK forced `httptest.NewTLSServer` + 401 + WWW-Authenticate challenge issuance — bearer tokens refused over plain HTTP. `az keyvault secret` data-plane URL is hard-coded to `*.vault.azure.net`; CLI cell skipped with rationale.

## Phase 1 — Object storage (PR #6)

Phase 1 was the foundation phase — codegen pipeline, conformance harness, CI matrix all built alongside the first real user. Largest API surface across all 8 services; richest auth + content semantics.

- **Three spec-format ingestion pipelines.** AWS Smithy → custom Go server-stub emitter at `internal/codegen/` (no official Smithy → Go server generator exists; `smithy-go` is client-side). GCP Discovery + Azure OpenAPI v3 were planned-then-deferred to Phase 11 (only AWS Smithy is spec-driven today).
- **Streaming throughout.** `httpPayload` blob members are `io.Reader` (in) / `io.ReadCloser` (out); object bodies never buffer through the shim. Carried into every subsequent phase as architectural baseline.
- **Neutral domain interface.** `internal/storage/domain/` between wire codec and cloud-specific backend. Every backend (MinIO, AWS S3 passthrough, GCS, Azure Blob) implements `domain.Storage` directly. Frontends adapt wire-protocol shapes to the domain.
- **Cross-cloud CopyObject.** Azure's CopyBlob is asynchronous (returns a copy ID; clients poll). The shim's CopyObject backend translates this to a synchronous-looking call by polling the destination — but fails loud if the poll loop exceeds the deadline rather than silently returning a stale state. Same posture every subsequent async-op phase carries forward.
- **Multipart ETag parity.** Each cloud computes multipart object ETag differently (S3 multipart is `MD5(parts) + "-N"`; GCS is composite-object hash; Azure differs again). `domain.MultipartETag` is a typed string that backends produce and frontends round-trip without reinterpreting.
- **BUG-1 (router `ForbiddenQueries`).** Required-only route matching meant `GET /{Bucket}/{Key+}?tagging=` silently fell through to GetObject. Fix: `restxml.RouteOptions.ForbiddenQueries` rejects routes when any named query is present; codegen emits the S3 feature-query list. GetObjectTagging + GetObjectAcl added as object-level probes (canonical "no tags" / "default ACL") so the TF AWS provider's aws_s3_object Read step gets a faithful response.

## Pre-phase — Repo bootstrap + continuity docs (PR #1, PR #2; merged 2026-05-18)

Repo created with the branch ruleset (linear history, PR-only, no force-push, squash + rebase merge). PHILOSOPHY.md as koans + Bierce-style definitions. AGENTS.md (CLAUDE.md is a symlink) as the operational rules. Continuity files (STATUS, DO_NEXT, PLAN, WHAT_WE_DID, BUGS) and Phase-0 CI checks (branch-rebased, symlinks-resolve, continuity-docs-present) wired into the main-branch ruleset as required status checks.

The continuity-file design is what makes coding-agent sessions survive context compaction: a fresh session reading STATUS + DO_NEXT + AGENTS should be productive without re-deriving anything from older conversations. PLAN.md is the single source of truth for the roadmap; per-phase planning lives inline as a section in PLAN.md, not in separate `PHASE_X_PLAN.md` files.

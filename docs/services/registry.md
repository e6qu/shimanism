# Container registry

Container registry is a two-plane service: cloud-specific repository control planes plus the shared OCI Distribution `/v2/` data plane for image push/pull.

## Frontends

| Frontend | Wire protocol | Notes |
|---|---|---|
| AWS ECR | awsJson1_1 + SigV4; OCI `/v2/` Basic auth | `GetAuthorizationToken` mints the Docker-login credential. |
| GCP Artifact Registry | Discovery REST + Bearer; OCI `/v2/` Bearer auth | Control repository names are full AR resources; Docker paths are OCI paths. |
| Azure ACR | ARM registry host + ACR `/oauth2/*` + OCI `/v2/` | ARM creates the registry host, not a repository. |

## Backends

| Backend | Real destination | Notes |
|---|---|---|
| `aws_ecr` | Real Amazon ECR | ECR SDK control plane + ECR `/v2/` data plane. |
| `gcp_artifactregistry` | Real Artifact Registry | AR REST control/inventory + configured Docker host `/v2/`. |
| `azure_acr` | Real Azure Container Registry | ACR token exchange + `/acr/v1` + `/v2/`. |
| `distribution` | CNCF Distribution registry | K8s peer slot; no empty repository creation. |
| `inmem` | Process-local content-addressable store | Tests + local dev only. |

## Intersection Contracts

- **[`services/registry/INTERSECTION.md`](../../services/registry/INTERSECTION.md)** — per-plane classification and source-shaped naming rules.
- **[`services/registry/APPLY_INTERSECTION.md`](../../services/registry/APPLY_INTERSECTION.md)** — Terraform/apply and cross-cloud naming contract.
- **[`docs/phase-18-scoping.md`](../phase-18-scoping.md)** — phase scoping and normalizations N30-N34.

## Conformance

Per-frontend conformance lives under `services/registry/conformance/`:

- SDK control-plane tests for ECR, Artifact Registry, and ACR ARM.
- CLI/Terraform control-plane tests where official tools expose endpoint overrides.
- go-containerregistry push/pull tests for the OCI data plane.
- Sockerless through-shim lanes for all three registry frontends. They currently skip loudly on simulator gaps: BUG-64 (AWS ECR no `/v2/`), BUG-65 (GCP AR upload `PATCH` 405), and BUG-66 (Azure ACR upload start 404).

## Known Gaps

- Empty repository creation is out of intersection for Azure ACR and CNCF Distribution.
- Registry sockerless data-plane coverage is blocked on simulator `/v2/` gaps (BUG-64/65/66), not on shim fallback logic.

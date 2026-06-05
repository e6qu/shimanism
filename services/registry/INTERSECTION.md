# Container Registry Intersection

Phase 18 covers container registries as two planes:

- control plane: repository lifecycle and image inventory.
- data plane: OCI Distribution `/v2/` blobs, manifests, tags, and uploads.

The shim is stateless. Blobs, manifests, repository inventory, upload sessions, labels/tags, and delete state live in the backend registry.

## In Intersection

| Capability | AWS ECR | GCP Artifact Registry | Azure ACR | K8s / CNCF Distribution |
|---|---|---|---|---|
| Repository create | `CreateRepository` | `repositories.create` | Out of intersection: ACR repositories are implicit on push | Out of intersection: Distribution repositories are implicit on push |
| Repository get/list | `DescribeRepositories` | `repositories.get/list` | `/acr/v1/_catalog` for repositories that contain content | `/v2/_catalog` |
| Repository delete | `DeleteRepository` | `repositories.delete` | `/acr/v1/{repo}` delete | Delete manifests by repo; empty create unsupported |
| Image list | `DescribeImages` / `ListImages` | `dockerImages.list` or `/v2` tag/manifest reads for Docker paths | `/acr/v1/{repo}/_manifests` | tags + manifest heads |
| Image delete | `BatchDeleteImage` | `/v2/{repo}/manifests/{ref}` | `/v2/{repo}/manifests/{ref}` | `/v2/{repo}/manifests/{ref}` |
| Push/pull | ECR Basic token + OCI `/v2/` | Bearer token + OCI `/v2/` | ACR exchange/token + OCI `/v2/` | OCI `/v2/` |

## Source-Shaped Names

Repository names stay source-shaped. The shim does not keep translation tables.

- AWS ECR uses flat repository names such as `team/app`.
- GCP control plane uses full resource names: `projects/{project}/locations/{location}/repositories/{repo}`.
- GCP Docker data plane uses Docker paths: `{project}/{repo}/{image}`.
- Azure ACR and Distribution data planes use OCI repository paths such as `team/app`.

When a backend cannot derive an answer from the source-shaped name and its own backend APIs, it returns a source-shaped unsupported/invalid/not-found error. It must not invent a mapping.

## Out Of Intersection

- Geo-replication, private endpoints, registry networking, and registry host provisioning.
- Vulnerability scanning, provenance/signing, SBOM/referrer policy, lifecycle cleanup policy, and retention policy.
- Tag immutability and per-tag write protections.
- Registry webhooks, pull-through cache rules, upstream remotes, virtual repositories.
- Azure ARM `registries.create` as a repository operation. It creates the registry host, not a repository.
- Empty repository creation on ACR and CNCF Distribution.

## Backend Notes

- `services/registry/backends/aws_ecr` uses ECR SDK control APIs and ECR's own `/v2/` endpoint after `GetAuthorizationToken`.
- `services/registry/backends/gcp_artifactregistry` uses Artifact Registry REST APIs for repository lifecycle and `dockerImages.list`; OCI traffic goes to the configured Docker host with OAuth2 Bearer auth.
- `services/registry/backends/azure_acr` uses ACR `/acr/v1`, `/oauth2/exchange`, `/oauth2/token`, and OCI `/v2/`.
- `services/registry/backends/distribution` uses only the CNCF Distribution `/v2/` API and returns unsupported for empty repository creation.

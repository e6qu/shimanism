# Container Registry Apply Contract

Companion to [INTERSECTION.md](INTERSECTION.md). This file describes what Terraform-style apply flows can honestly round-trip.

## Repository Resources

| Source provider | In-contract fields | Out-of-contract fields |
|---|---|---|
| `hashicorp/aws` `aws_ecr_repository` | repository name, resource tags when the backend exposes tags, force delete | image scanning, encryption configuration, lifecycle policy, replication, pull-through cache, tag mutability exclusions |
| `hashicorp/google` `google_artifact_registry_repository` | project/location/repository ID, format `DOCKER`, labels | cleanup policies, virtual/remote repository mode, KMS key selection, vulnerability scanning, IAM policy |
| `hashicorp/azurerm` `azurerm_container_registry` | registry-host discovery only for the Azure frontend | treating the ARM registry host as a portable repository, network rules, admin user, SKU semantics, private endpoints, geo-replication |

Azure ACR repositories are implicit: a repository exists after the first push. An apply flow that expects to create an empty repository against Azure ACR is out of intersection and must fail loudly.

## Image Push/Pull

OCI push/pull by tag and by digest is in intersection across all frontends and connected backends that expose an OCI `/v2/` data plane.

The shim may validate digests and stream request bodies, but it must not buffer image layers as state of record. Upload-session handles are backend handles:

- AWS ECR: ECR `/v2/` upload location.
- GCP Artifact Registry: configured Docker host `/v2/` upload location.
- Azure ACR: ACR `/v2/` upload location.
- Distribution: Distribution upload `Location`.

## Cross-Cloud Shape Rules

Repository identifiers remain source-shaped. Cross-cloud apply must use identifiers that the selected source frontend and destination backend can honestly interpret.

- AWS-shaped repository names are flat OCI paths, e.g. `team/app`.
- GCP-shaped repository control names are full resources, e.g. `projects/p/locations/us/repositories/r`.
- GCP-shaped Docker paths are OCI paths, e.g. `p/r/app`.
- Azure-shaped repository names are OCI paths, e.g. `team/app`; ARM registry resources are registry hosts, not repositories.

The shim does not maintain a name mapping from GCP full resource names to Docker paths or from Azure registry hosts to repositories. If a client needs both planes, it must supply names that match the plane it is calling.

## Simulator Coverage

Sockerless registry lanes are present and fail loud on simulator gaps:

- BUG-64: AWS ECR simulator has no Docker Registry `/v2/` data plane.
- BUG-65: GCP Artifact Registry simulator creates upload sessions but returns 405 on OCI chunk `PATCH`.
- BUG-66: Azure ACR simulator returns 404 for OCI upload start.

None of these are worked around in the shim. When the simulators add the missing behavior, the same tests should move from BUG skips to full push/pull assertions.

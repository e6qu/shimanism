# Vendored specs

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-apigatewayv2.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/apigatewayv2.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T18:34:00Z |
| `azure-apimanagement.json` | `Azure/azure-rest-api-specs` | `specification/apimanagement/resource-manager/Microsoft.ApiManagement/ApiManagement/stable/2024-05-01/apimanagement.json` | MIT | `8e3e3baa2523e17becb3d7032eb909aba12b7f2c` | 2026-05-22T11:00:00Z |

API Gateway v2 uses **restJson1** (same family as Lambda — REST
routes + JSON bodies). The shim hand-writes the URL dispatcher;
wire-type shapes mirror the vendored Smithy spec.

GCP API Gateway reused via `google.golang.org/api/apigateway/v1`.
Azure API Management reused via `armapimanagement`.

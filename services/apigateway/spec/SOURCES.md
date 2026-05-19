# Vendored specs

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-apigatewayv2.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/apigatewayv2.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T18:34:00Z |

API Gateway v2 uses **restJson1** (same family as Lambda — REST
routes + JSON bodies). The shim hand-writes the URL dispatcher;
wire-type shapes mirror the vendored Smithy spec.

GCP API Gateway reused via `google.golang.org/api/apigateway/v1`.
Azure API Management reused via `armapimanagement`.

# Vendored specs

| Local file | Upstream repo | Upstream path | Upstream license | Pinned at | Fetched (UTC) |
|---|---|---|---|---|---|
| `aws-lambda.smithy.json` | `aws/aws-sdk-go-v2` | `codegen/sdk-codegen/aws-models/lambda.json` | Apache-2.0 | `2517fe9ffa52ed4507b13ccc57efa111b2008750` | 2026-05-19T16:42:00Z |

Lambda uses the **`restJson1` wire protocol** — actual REST routes
(e.g. `POST /2015-03-31/functions/`, `GET /2015-03-31/functions/{Name}/configuration`)
with JSON request + response bodies. Different protocol family from
awsQuery (RDS / SNS / ElastiCache), awsJson1_0 (SQS), and awsJson1_1
(Secrets Manager). The shim's Lambda frontend hand-writes the URL
dispatcher; wire-type shapes mirror the vendored Smithy spec.

GCP Cloud Run is reused via `google.golang.org/api/run/v2`.
Azure Container Apps is reused via `armappcontainers`.

# API Gateway importer-read contract

> Phase 9 sub-phase 9.2 — captured from a `terraform import aws_apigatewayv2_api` run against the shim's AWS APIGW v2 frontend.

## aws_apigatewayv2_api import — observed wire ops

restJson1 — per-op URL routing.

| HTTP method + path | Category | Status |
|---|---|---|
| `GET /v2/apis/shim-imported-api` (GetApi) | 1 | ✅ |
| `GET /v2/apis/shim-imported-api` (refresh after import) | 1 | ✅ |

The full provider Read path also issues GetTags / GetStages / GetIntegrations / GetRoutes, but for an empty Api those endpoints either return empty lists (✅) or aren't in the importer's required path for the resource attributes Terraform persists.

## Fidelity fix surfaced by this test

The initial run showed `terraform plan` proposing a diff:

```
+ api_key_selection_expression = "$request.header.x-api-key"
+ route_selection_expression   = "$request.method $request.path"
```

Real APIGW v2 returns these **selection expressions** with default values even when the user doesn't configure them. Category 2: feature unset → source cloud's canonical default. The shim was omitting them, so the provider treated them as "needs to be added" diffs.

Fixed: `gatewayToAPI()` now always emits the AWS-default selection expressions. After fix: `terraform plan -detailed-exitcode` returns 0.

This is the third category-2 default class of fidelity bug surfaced by the Phase 9 import tests (after the SNS Policy / EffectiveDeliveryPolicy fixes). The pattern: a Terraform-managed resource has many attributes the user doesn't put in their HCL, but real-cloud Read always returns; the shim has to too.

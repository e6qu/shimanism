# API Gateway — migration walkthroughs

> Phase 9 sub-phase 9.2-B. Declarative-replace, intersection of 5 ops. See [INTERSECTION.md](INTERSECTION.md).

## AWS API Gateway v2 → GCP API Gateway

```bash
shim apigateway --addr=:9800 \
  --frontend=aws_apigatewayv2 \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=apigateway --endpoint=http://localhost:9800)"

# 1. Define the API.
aws apigatewayv2 create-api --name prod-api --protocol-type HTTP

# 2. Define an integration (the backend the routes will forward to).
INT_ID=$(aws apigatewayv2 create-integration --api-id prod-api \
  --integration-type HTTP_PROXY \
  --integration-uri https://backend.example.com/ \
  --integration-method GET --query IntegrationId --output text)

# 3. Define a route.
aws apigatewayv2 create-route --api-id prod-api \
  --route-key "GET /healthz" --target "integrations/$INT_ID"

# 4. Deploy — this is the moment the shim invokes DeployGateway on
#    the GCP backend, which translates the route set into an OpenAPI
#    2.0 document, posts an ApiConfig, and updates the Gateway.
aws apigatewayv2 create-deployment --api-id prod-api

# 5. The gateway URL ends up on aws-lambda://... in this phase
#    (placeholder); GCP gateway's real DefaultHostname is on
#    DescribeApi.Endpoint.URL.
aws apigatewayv2 get-api --api-id prod-api
```

**Walkthrough holds for AWS frontend / GCP backend.** Caveats below.

## Cloud → Envoy Gateway (K8s peer)

```bash
shim apigateway --addr=:9800 \
  --frontend=aws_apigatewayv2 \
  --backend=envoy --kubeconfig=$HOME/.kube/config &
```

`AWS API + Route` ↔ `gateway.networking.k8s.io/v1 Gateway + HTTPRoute`. The Phase 8 exit criterion (`TestRouteServes_Envoy`) verified this end-to-end on real kind clusters in CI.

## Driving the GCP frontend natively (gap)

```bash
# This is what a user migrating GCP → AWS would do:
shim apigateway --addr=:9800 \
  --frontend=gcp_apigateway \
  --backend=aws &
gcloud api-gateway api-configs create cfg \
  --api=prod-api --openapi-spec=openapi.yaml
```

**This walkthrough does NOT hold today.** BUG-9: the GCP frontend only implements the `Gateways` surface, not the `Apis` + `ApiConfigs` surfaces. `gcloud api-gateway api-configs create` 404s. Phase 9 must close BUG-9.

## Driving the Azure APIM frontend natively (gap)

```bash
shim apigateway --addr=:9800 \
  --frontend=azure_apim \
  --backend=aws &
az apim api operation create --service-name svc --api-id api --operation-id route1 ...
```

**Does NOT hold.** BUG-10: Operations subresource missing. `az apim api operation create` 404s.

## Coverage

AWS frontend → all backends: ✅ (with Azure backend's Delete deferred to Track A per BUG-6).
GCP frontend: missing route deployment (BUG-9) → can only do `Gateway` CRUD, not full migration.
Azure frontend: missing route deployment (BUG-10) → same shape.

**Phase 9 priority:** close BUG-9 and BUG-10 so all three frontends carry the route-deployment surface honestly. Without these, "migrate APIM-shaped Terraform onto an AWS backend" doesn't work.

# Functions — migration walkthroughs

> Phase 9 sub-phase 9.2-B. Container-image, HTTP-trigger only. See [INTERSECTION.md](INTERSECTION.md).

## AWS Lambda → GCP Cloud Run

```bash
shim functions --addr=:9700 \
  --frontend=aws_lambda \
  --backend=gcp --gcp-project=$GCP_PROJECT &
eval "$(shimctl env --frontend=aws --service=functions --endpoint=http://localhost:9700)"

# Deploy a container image — note PackageType=Image, not Zip.
aws lambda create-function --function-name api \
  --package-type Image \
  --code ImageUri=ghcr.io/example/api:v1 \
  --role arn:aws:iam::000000000000:role/lambda \
  --timeout 60
aws lambda get-function --function-name api
# The HTTP URL is on the returned Endpoint; direct GET works.
aws lambda update-function-code --function-name api --image-uri ghcr.io/example/api:v2
aws lambda delete-function --function-name api
```

**Walkthrough holds.** Container images run on Cloud Run without modification. The HTTP-invoke exit criterion (Phase 7) proved end-to-end reachability.

## Cloud → Knative (K8s peer)

```bash
shim functions --addr=:9700 \
  --frontend=aws_lambda \
  --backend=knative --kubeconfig=$HOME/.kube/config &
```

`AWS Lambda Function` ↔ `Knative Service`. Hostname → kourier-internal Host-header dispatch (see WHAT_WE_DID.md § Phase 7).

## Gaps

- **FunctionUrlConfig CRUD** not wired (only synthesized at DescribeFunction). Migration users who Terraform `aws_lambda_function_url` separately need this. Phase 9 fold-in.
- **Event sources, async invoke, layers, provisioned concurrency** — all out of intersection. Migration users must rewire event triggers on the target cloud; that's the (unavoidable) friction.

## Terraform walkthrough (AWS-shaped provider against a non-AWS backend)

```hcl
provider "aws" {
  region                      = "us-east-1"
  access_key                  = "AKIAIOSFODNN7EXAMPLE"
  secret_key                  = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  endpoints {
    lambda = "http://localhost:9700"
  }
}

resource "aws_lambda_function" "shim_func" {
  function_name = "cross-cloud-func"
  package_type  = "Image"
  image_uri     = "ghcr.io/example/api:v1"
  role          = "arn:aws:iam::000000000000:role/lambda"
  timeout       = 60
}
```

```bash
shim functions --addr=:9700 --frontend=aws_lambda --backend=gcp --gcp-project=$GCP_PROJECT &

terraform init
terraform apply -auto-approve
terraform import aws_lambda_function.existing cross-cloud-func
```

Conformance test reference: `services/functions/conformance/{terraform_import_test.go, cross_cloud_import_test.go}`.

## Coverage

Synchronous HTTP-invoke green for the intersection. Async + event-source surfaces are deliberately out.

# Testing

The conformance contract is enforced. If a shimmed operation lacks tests, the operation doesn't exist for shimanism's purposes.

## The conformance contract

Every shimmed operation must be exercisable via the **matching cloud's official client surfaces — for every frontend × every backend in scope**:

| Frontend | SDK (canonical) | CLI | Terraform provider |
|---|---|---|---|
| AWS | `aws-sdk-go-v2/*` | `aws` | `hashicorp/aws` with `endpoints { ... }` |
| GCP | `cloud.google.com/go/*` | `gcloud` with `--api-endpoint-overrides` | `hashicorp/google` with custom endpoints |
| Azure | `github.com/Azure/azure-sdk-for-go/*` | `az` | `hashicorp/azurerm` |

Go is canonical for the SDK row; Python + Node tests added per-service where relevant.

A frontend's drivers are always the **matching cloud's tooling**, not a cross-cloud substitute. The AWS-shaped frontend is tested by AWS tools; the GCS-shaped frontend by GCP tools; the Azure-shaped frontend by Azure tools. Driving the AWS frontend with `gcloud` isn't a meaningful conformance test — the wire protocols don't match.

## The test pyramid

| Lane | What it covers | Where it lives |
|---|---|---|
| **Matrix tests** | Every (frontend × backend × driver) cell, focused on the core ops the shim covers for that service. | `services/<svc>/conformance/matrix_test.go` |
| **Per-frontend SDK tests** | One file per frontend; exercises the wire format end-to-end via the official cloud SDK. | `services/<svc>/conformance/aws_sdk_test.go`, etc. |
| **Per-frontend CLI tests** | Same as SDK but driving `aws`, `gcloud`, `az` directly. | `services/<svc>/conformance/aws_cli_test.go`, etc. |
| **Per-frontend Terraform tests** | Same as SDK but using the matching `hashicorp/<cloud>` provider. | `services/<svc>/conformance/aws_terraform_test.go`, etc. |
| **Phase-9 import tests** | `terraform import` round-trip per service: import a backend-side resource through the shim, then `terraform plan` should report no diff. | `services/<svc>/conformance/terraform_import_test.go` |
| **Phase-10 apply tests** | `terraform apply` drift audit per service: full Create → Read → Destroy, asserting no drift. | `services/<svc>/conformance/terraform_apply_test.go` |
| **Cross-cloud roundtrip exit criteria** | The headline migration test per service: a resource that *lives in cloud B* is operated by cloud-A-shape Terraform through the shim. | `services/<svc>/conformance/cross_cloud_{import,apply}_test.go` |
| **K8s peer connectivity** | Service-specific data-plane test (HTTP invoke for functions, RESP PING for Redis, psql connect for Postgres, etc.). | `services/<svc>/conformance/<topic>_test.go` |

## Running tests locally

```sh
# Everything — the fast lane.
make test

# Just one service.
go test ./services/storage/...

# Just one test.
go test ./services/storage/conformance/ -run TestTerraform_AWSS3_Apply_Bucket_NoDrift -v

# The full conformance matrix.
make conformance-matrix
```

The CI lane `go vet + test + build` runs `go test -timeout 5m ./...`. The 5-minute cap is intentional: apply tests cost ~20-75s per cell on cold CI runners and we'd rather see slow tests slimmed than have the cap raised.

The conformance lanes per backend (`conformance-minio`, `conformance-vault`, `conformance-nats`, `conformance-cnpg`, `conformance-redisop`, `conformance-knative`, `conformance-envoy`, `conformance-azureblob`, `conformance-gcs`) spin up the matching backend container and run the full matrix against it.

## Adding a new conformance test

For an operation you just shimmed:

1. **SDK test.** Pick the canonical SDK row for the frontend (Go is canonical). Write a focused test that drives the operation via the SDK and asserts on the response. Use `internal/harness/server.go` to spin up the shim in-process.

   ```go
   func TestStorage_AWSS3_PutObject_RoundTrip(t *testing.T) {
       t.Parallel()
       backend := inmem.New()
       srv := harness.StartStorageServer(t, backend)

       client := s3.NewFromConfig(awsTestConfig(srv.URL))
       _, err := client.PutObject(t.Context(), &s3.PutObjectInput{
           Bucket: aws.String("b"),
           Key:    aws.String("k"),
           Body:   strings.NewReader("data"),
       })
       // assertions ...
   }
   ```

2. **CLI test.** Use `os/exec` to invoke `aws`, `gcloud`, or `az`. Skip if the binary isn't on PATH. Set the appropriate endpoint-override env var or flag.

3. **Terraform test.** Same shape: write an HCL fixture with `endpoint { ... } = http://<shim>`; run `terraform init / apply / plan -detailed-exitcode / destroy`; assert exit codes.

4. **Add the test to the matrix runner** if the operation needs to be exercised cross-backend (`services/<svc>/conformance/matrix_test.go`).

## Skip-with-pointer rule

Operations or driver-test cells that are genuinely blocked on an open BUG must skip with a pointer to that BUG, not fail silently:

```go
func TestTerraform_AWSQueue_Apply_NoDrift(t *testing.T) {
    t.Skip("BUG-2: aws_sqs_queue reconciles via SetQueueAttributes (not yet in domain)")
    // ...
}
```

When the BUG closes, the `t.Skip` line goes away in the same commit. No orphan skips.

## The bug-first rule (test-edition)

A test that surfaces a real fidelity defect or behavioral asymmetry **must file a BUG before the fix lands**. The exception is "false positive" — see [BUGS.md § false positives](../BUGS.md#false-positives). A test passing only because the asserted behavior happens to match a *fake* in the shim is itself a bug (the fake).

## Conformance "tests as docs"

A conformance test is the executable form of the per-service contract. When a contract gets a new in-contract attribute (in `APPLY_INTERSECTION.md`), the conformance suite gets a new test that asserts the round-trip. When an attribute is documented out-of-contract, the suite gets an invalid-input test that asserts the shim returns the source cloud's "not supported" envelope.

That's the cross-link:

- `services/<svc>/OPERATIONS.md` — list of operations.
- `services/<svc>/INTERSECTION.md` — per-operation classification.
- `services/<svc>/APPLY_INTERSECTION.md` — Apply-time contract.
- `services/<svc>/conformance/` — executable verification.

Each test should be traceable back to a line in one of those docs.

## CI organization

`.github/workflows/checks.yml` defines the required checks. The `branch rebased on origin/main`, `tracked symlinks resolve`, `continuity docs present`, `go vet + test + build`, `dependency licenses AGPL-compatible` are the universal lanes. Per-backend `conformance-<backend>` lanes spin up Docker containers and run the matrix.

When you add a service that introduces a new backend, add the matching `conformance-<backend>` lane in the same PR.

## Cross-link

- [docs/contributing.md](contributing.md) — branch / PR shape.
- [docs/development.md](development.md) — local setup, adding a new operation.
- [docs/codegen.md](codegen.md) — spec-to-server-stub pipeline.
- [AGENTS.md § conformance contract](../AGENTS.md#the-conformance-contract) — full rules.
- [BUGS.md](../BUGS.md) — the bug-first audit trail.

# Development

Local setup, build, run, debug. The basics for getting a productive feedback loop going.

## Required tools

- **Go ≥ 1.26.** The shim is pure Go; cross-compile target is `darwin/arm64` + `linux/amd64`.
- **terraform** (any recent version; the shim conformance tests vendor whatever's on `PATH`).
- **docker** for backend containers (MinIO, NATS, Vault, etc).
- **kind** for K8s peer conformance lanes (Knative, Envoy Gateway, CloudNativePG, Redis Operator). Optional locally; CI lanes use kind.

## Optional tools

- **`aws` CLI** for AWS frontend driver tests.
- **`gcloud`** for GCP frontend driver tests.
- **`az`** for Azure frontend driver tests.
- **`make`** for the canonical local test runs.

## Cloning + build

```sh
git clone https://github.com/e6qu/shimanism
cd shimanism
go build ./...
```

The main binary is `cmd/shim`. There's also `cmd/shimctl` (CLI for environment / endpoint-override generation) and four codegen tools — `cmd/codegen` (AWS Smithy), `cmd/azure-codegen` (Azure OpenAPI v2), `cmd/gcp-codegen` (GCP Discovery routing), `cmd/inject-provenance` (writes `_provenance` to each vendored spec from SOURCES.md). All three regeneration lanes flow through `make codegen`.

## Running a shim locally

Each service has a subcommand:

```sh
go run ./cmd/shim storage    -backend=inmem -addr=:9001
go run ./cmd/shim secrets    -backend=inmem -addr=:9002
go run ./cmd/shim queue      -backend=inmem -addr=:9003
go run ./cmd/shim pubsub     -backend=inmem -addr=:9004
go run ./cmd/shim rdbms      -backend=inmem -addr=:9005
go run ./cmd/shim cache      -backend=inmem -addr=:9006
go run ./cmd/shim functions  -backend=inmem -addr=:9007
go run ./cmd/shim apigateway -backend=inmem -addr=:9008
```

Frontends + ports are documented in each service's [per-service doc](services.md). The default frontend is AWS-shape; pass the service-specific GCP / Azure frontend name to switch, e.g. `-frontend=gcs` / `-frontend=azure_blob` for storage; `-frontend=gcp_secretmanager` / `-frontend=azure_keyvault` for secrets; etc. Run `./shim <svc> -h` to see the exact frontend / backend names per service.

## `shimctl env`

Generates the env-var / SDK-config / Terraform endpoint-override block for a given service. The flag is `--endpoint`, not `--shim-url`:

```sh
shimctl env --frontend=aws --service=storage --endpoint=http://localhost:9001
```

Prints a copy-paste-ready block for `aws-sdk-go-v2`, `boto3`, the `aws` CLI, and `hashicorp/aws` Terraform.

## `make` targets

| Target | What it does |
|---|---|
| `make test` | `go test ./...` |
| `make lint` | `golangci-lint run` |
| `make license-check` | Verify every linked dependency has an AGPL-compatible license per [`doc/COMPATIBLE_LICENSES.md`](../doc/COMPATIBLE_LICENSES.md). |
| `make codegen` | Regenerate server stubs from `services/<svc>/codegen.json`. |
| `make conformance-matrix` | Run the canonical (frontend × backend × driver) matrix test. |

## Adding a new operation

1. **Confirm semantic equivalence** exists in AWS + GCP + Azure + the K8s peer.
2. **Update the spec** if the operation isn't already in the vendored upstream spec under `services/<svc>/spec/`.
3. **Update `services/<svc>/codegen.json`** to include the new operation.
4. **Run `make codegen`.** The server stubs regenerate; the only hand-written code is the `translate.go` file per backend.
5. **Implement the per-backend translation** in `services/<svc>/backends/<backend>/`. Each backend gets a small `translate.go` mapping the source-API request to the backend's domain operation.
6. **Add SDK + CLI + Terraform conformance tests** under `services/<svc>/conformance/`. SDK is the canonical layer; CLI + TF tests follow the same pattern.
7. **Update the per-service docs** ([docs/services/<svc>.md](services/), `services/<svc>/OPERATIONS.md`, `INTERSECTION.md`, `APPLY_INTERSECTION.md`).
8. **Run the full conformance lane locally** and verify green against every backend in scope.
9. **Open the PR** with continuity-doc updates ([STATUS.md](../STATUS.md), [DO_NEXT.md](../DO_NEXT.md), [WHAT_WE_DID.md](../WHAT_WE_DID.md) if a phase or sub-phase advanced).

There is no "land it and add tests later." If you edit a service, the conformance tests ship with it.

## Adding a new service

Longer recipe. The minimum:

1. **Vendor the upstream spec** under `services/<svc>/spec/`. AWS = Smithy JSON. GCP = protobuf / Discovery. Azure = OpenAPI.
2. **Define the neutral domain interface** in `internal/<svc>/domain/`. Methods + types reflect the cross-cloud intersection. Read [docs/architecture.md § statelessness](architecture.md#statelessness) first.
3. **Write the codegen manifest** `services/<svc>/codegen.json`. Lists the operations the codegen pipeline should emit server stubs for.
4. **Run `make codegen`.** Stubs land in `services/<svc>/gen/`.
5. **Implement per-cloud frontends** in `internal/<svc>/frontends/<cloud>/`. Each one is a thin adapter — decode wire types → call domain → encode response.
6. **Implement per-destination backends** in `services/<svc>/backends/<dest>/`. AWS / GCP / Azure passthroughs + one K8s peer + an `inmem` for tests.
7. **Write the four per-service docs**: `OPERATIONS.md`, `INTERSECTION.md`, `APPLY_INTERSECTION.md`, `MIGRATION.md`.
8. **Write the conformance tests** in `services/<svc>/conformance/`.
9. **Add the service subcommand** to `cmd/shim/main.go`.
10. **Add the CI lane** to `.github/workflows/checks.yml`.
11. **Add the service to [README.md](../README.md#shimmed-services)** and [docs/services.md](services.md).
12. **Open the PR**, expect a multi-week review (new services are big changes).

See [docs/testing.md](testing.md) for the per-frontend × per-backend test matrix the new service needs.

## Debugging

The shim's `httptest`-based harness (`internal/harness/server.go`) is the local test surface. Spinning up a single-process shim + backend + client request flow lets you set Go breakpoints across all three layers.

For CI failures: `gh run view <run-id> --log-failed` shows the failing job's output. The `go vet + test + build` lane has a 5-minute timeout; if a per-test cost exceeds that, make the test faster rather than raising the cap (see [docs/testing.md](testing.md)).

For drift bugs: the canonical pattern is `terraform apply → plan -detailed-exitcode`. Exit code 2 means the next apply would do work — that's a Create-then-Read drift, file as BUG and fix in the same PR.

## Cross-link

- [docs/architecture.md](architecture.md) — the layered model.
- [docs/codegen.md](codegen.md) — spec-to-server-stub pipeline.
- [docs/testing.md](testing.md) — the conformance contract.
- [docs/contributing.md](contributing.md) — branch / PR shape, continuity contract.
- [AGENTS.md](../AGENTS.md) — full rules.

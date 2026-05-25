# Service catalog

The canonical service list lives in [README.md](../README.md#shimmed-services). This page is the cross-link hub: for each shimmed service, the per-service docs (frontends, backends, intersection contracts, conformance tests, and the open BUGs that gate any cell).

For runnable cross-cloud commands, start with the [complete end-to-end examples](end-to-end-examples.md). The [standalone sockerless examples](end-to-end-examples.md#optional-local-simulator-testing-with-sockerless) demonstrate the storage service across AWS -> GCP, GCP -> Azure, and Azure -> AWS without real cloud accounts.

## Per-service detail

| Service | Detailed docs | In-tree |
|---|---|---|
| Object storage | [docs/services/storage.md](services/storage.md) | [services/storage/](../services/storage/) |
| Secrets | [docs/services/secrets.md](services/secrets.md) | [services/secrets/](../services/secrets/) |
| Queue | [docs/services/queue.md](services/queue.md) | [services/queue/](../services/queue/) |
| Pub/sub | [docs/services/pubsub.md](services/pubsub.md) | [services/pubsub/](../services/pubsub/) |
| RDBMS (control plane) | [docs/services/rdbms.md](services/rdbms.md) | [services/rdbms/](../services/rdbms/) |
| Cache (control plane) | [docs/services/cache.md](services/cache.md) | [services/cache/](../services/cache/) |
| Functions (control plane) | [docs/services/functions.md](services/functions.md) | [services/functions/](../services/functions/) |
| API gateway | [docs/services/apigateway.md](services/apigateway.md) | [services/apigateway/](../services/apigateway/) |

Each per-service doc covers:

- **Wire protocols** — what each frontend speaks (XML / awsJson1_0 / awsJson1_1 / awsQuery / restJson1 / GCP JSON / ARM REST).
- **Backends** — the four (or five) destinations: AWS / GCP / Azure / K8s peer / `inmem`.
- **Intersection contracts** — pointers to the per-service `OPERATIONS.md` (operation table), `INTERSECTION.md` (read-side classification), and `APPLY_INTERSECTION.md` (write-side / Apply contract).
- **Conformance tests** — what runs on every PR (SDK / CLI / Terraform per frontend per backend).
- **Known gaps** — open BUGs that affect any cell, with skip-with-pointer rationale.

## How the layers compose

The frontends and backends are independent. The shim binary instantiates one frontend + one backend per running instance — and any pair composes:

```
AWS-shape Terraform → shim (AWS S3 frontend + GCS backend) → real GCS
```

See [docs/architecture.md](architecture.md) for the layered model and [doc/CROSS_CLOUD_ROUTING.md](../doc/CROSS_CLOUD_ROUTING.md) for the migration story.

## Per-service file conventions

Every shimmed service follows the same in-tree layout under `services/<svc>/`:

| File | Purpose |
|---|---|
| `OPERATIONS.md` | Per-cloud op table — what's shimmed, what's not. |
| `INTERSECTION.md` | Phase 9.2-A: classify every wire op (real work / feature-unset / out-of-intersection). |
| `APPLY_INTERSECTION.md` | Phase 10.0-A: write-side contract that Apply matrix tests assert against. |
| `README.md` | Quick orientation for the service. |
| `codegen.json` | Spec → server-stub manifest (see [docs/codegen.md](codegen.md)). |
| `spec/` | Vendored upstream specs (Smithy / OpenAPI / Discovery / proto). |
| `gen/` | Generated server stubs. Don't edit by hand. |
| `backends/<dest>/` | One backend implementation per destination. |
| `conformance/` | SDK + CLI + Terraform driver tests per (frontend × backend). |

The corresponding `internal/<svc>/` tree holds the domain interface + the frontends.

## Adding a new shimmed service

See [docs/development.md § adding a new service](development.md#adding-a-new-service).

# Phase 10 — Cross-cloud `terraform apply` through shimanism

> **Goal:** Phase 9 proved that the shim is a transparent read surface — `terraform import` of a resource that lives in cloud B, driven through the shim's cloud A frontend, produces a state file that round-trips without diffs. Phase 10 extends the proof to the *write* side: a user's `terraform apply` against the shim should provision, update, and destroy resources on the destination backend cloud, with the source-cloud provider unaware of the translation.
>
> Apply is the migration user's actual workflow. Import is for adoption-of-existing-state; apply is for everyday Terraform usage. If apply doesn't work, the shim is a museum piece; if it works, the shim is a migration tool.

State [STATUS.md](STATUS.md) · resume [DO_NEXT.md](DO_NEXT.md) · bugs [BUGS.md](BUGS.md) · roadmap [PLAN.md](PLAN.md) · philosophy [PHILOSOPHY.md](PHILOSOPHY.md) · rules [AGENTS.md](AGENTS.md).

## Status (draft)

This document is a **draft** opened after Phase 9 closed. Scope and sub-phases will be refined in the same review cadence as Phase 9 (write → submit to codex → apply feedback).

## Why apply is harder than import

Import calls `Read` only. Apply calls `Create` → `Read` → optionally `Update` → eventually `Delete`. Each of those is a category-1-real-work operation per the Phase 9.2-A taxonomy. The shim has been exercising all of them at the SDK and CLI level since Phase 1, but **Phase 9 didn't drive them through the Terraform write path** — that's where surprising fidelity gaps live, because Terraform issues Create-then-Read-then-compare cycles that surface every attribute the shim's Create discards or fails to round-trip.

Specifically:

- **Create-then-Read drift.** The provider issues Create, expects to read back the same attributes it sent. The shim's Create may accept a field, the backend may quietly drop it, and the subsequent Read returns the unset value. Terraform sees this as drift and proposes a replace. Phase 9.5 surfaced exactly this for cache (`num_cache_nodes`), functions (`memory_size`, `role`, `publish`), rdbms (`username`), apigateway (`api_key_selection_expression`). Phase 10 forces the same audit for every attribute on every resource.
- **Async-op polling under load.** Create → poll until ready → Read. Phase 9 mostly ran against inmem (synchronous); Phase 10 forces real backends where Create returns a polling marker the provider expects to track. BUG-5 (GCP Operations) is the canonical example; closing it is in Phase 10's critical path.
- **Update semantics.** AWS RDS `ModifyDBInstance` vs GCP `Instances.patch` vs Azure `Servers.Update` differ in which fields are settable in-place vs require replace. The shim's Update has to translate intent — partial vs full replacement — honestly per backend.
- **Destroy semantics.** Soft-delete (Azure KV, AWS Secrets Manager) vs hard-delete (most others) differ. Phase 9.5 worked around with `recovery_window_in_days = 0`; Phase 10 makes soft-delete a first-class part of the intersection.

## Scope

| | |
|---|---|
| Services in scope | All 8. |
| Operation in scope | `terraform apply` per shimmed resource type per frontend. **Write paths** — Create / Update / Delete. |
| Out of scope | New resource types (Phase 10 is about the existing intersection's write surface). Cloud-side replication, vendor-specific upgrade paths, point-in-time recovery. |
| Driver matrix | Same as Phase 9: 3 frontends × 5 backends × 3 driver types, but now Apply instead of Import. |
| Real-cloud lanes | Same Phase 9-A carry. Real-cloud Apply is *the* honest migration test; mock-tier Apply is the precondition. |

## Hard problems & how each is approached

### 1. Create-then-Read drift audit

For each (resource type, source cloud) pair, write a test that:

1. `terraform apply` against the shim — drives Create.
2. `terraform plan -refresh-only -detailed-exitcode` — drives Read against the same shim and asks "is the state still consistent with the cloud?"
3. Assert exit code 0.

If any attribute the user set in HCL isn't returned by the shim's Read after the shim's Create, that's a Phase-10 fidelity bug. File BUG, fix it. Repeat until 0.

This is essentially what Phase 9 was doing via Import, but Apply touches more attributes because the user *set* them rather than just *imported* them.

### 2. Async-op polling — close BUG-5 family

BUG-5 (GCP Operations) has been deferred since Phase 5. Phase 10 closes it across:

- `services/rdbms/frontends/gcp_cloudsql/server.go` — Operations.get endpoint.
- `services/cache/frontends/gcp_memorystore/server.go` — same.
- `services/functions/frontends/gcp_cloudrun/server.go` — same.
- `services/apigateway/frontends/gcp_apigateway/server.go` — same.

The shim's domain layer needs a generic `Operation` type with `Status`, `Done`, `Error` fields; backends produce one at Create time, the frontend serves the polling endpoint by re-reading the underlying resource and synthesizing the operation state at request time (stateless invariant).

### 3. Update intersection

For each service:

- Walk the source-cloud's Update operation signature.
- Identify which attributes the intersection permits in-place updates for.
- Identify which require replace (Terraform `ForceNew`).
- Audit the shim's translate.go to ensure Update dispatches honestly, and that fields-that-require-replace surface the source cloud's "operation not supported in update" error vocabulary rather than silently no-op.

### 4. Soft-delete semantics

AWS Secrets Manager + Azure Key Vault default to soft-delete. AWS S3 has versioning (a form of soft-delete for objects). GCS, Azure Blob have soft-delete tiers. Phase 10's intersection has to:

- Surface soft-delete as a category-1 op (the user wants to recover-after-delete in many migration scenarios).
- Map the per-cloud retention windows to a single domain primitive.
- Translate the Terraform `recovery_window_in_days` (AWS) ↔ `soft_delete_retention_days` (Azure) ↔ default-30-days (GCP).

### 5. Apply exit criterion: `TestCrossCloudApply_Roundtrip`

Symmetric to Phase 9.13's `TestCrossCloudImport_Roundtrip`. Per (source cloud A, backend cloud B) where A ≠ B:

1. `terraform apply` A-shaped HCL through the shim with backend=B.
2. `terraform plan -detailed-exitcode` — no drift.
3. `terraform apply` again with an updated attribute (where in-place update is allowed) — expects 1 update, 0 replace.
4. `terraform plan -detailed-exitcode` — no drift.
5. `terraform destroy` — cleans up via the shim → backend → mock-cloud-B chain.

## Sub-phase plan (draft)

| Sub | Status | Headline |
|---|---|---|
| **10.0** | ◻ | Scope baseline (this doc; codex review). |
| **10.1** | ◻ | Close BUG-5 family — GCP Operations.get across rdbms / cache / functions / apigateway. |
| **10.2** | ◻ | Create-then-Read drift audit per service. Build the `terraform apply` test scaffolding. |
| **10.3** | ◻ | Update intersection audit per service. |
| **10.4** | ◻ | Soft-delete intersection across secrets / storage / queue. |
| **10.5** | ◻ | Per-service `apply_test.go` covering Create → Read-check → Update → Read-check → Destroy. |
| **10.6** | ◻ | Cross-cloud Apply matrix tests — symmetric to Phase 9.13's cross-cloud import matrix. |
| **10.7** | ◻ | Exit criterion: `TestCrossCloudApply_Roundtrip` per service. |
| **10.8** | ◻ | Phase 10 closer. |

## Phase 10-A (real-cloud Apply, Track A)

The mock-tier Apply matrix is a precondition; real-cloud Apply is the *headline* migration test. Once mock-tier Phase 10 closes:

- **10-A.1** Real-cloud `terraform apply` lanes per (frontend, backend) where backend = a real cloud account.
- **10-A.2** Snapshot/backup-based data migration tests for rdbms + cache.
- **10-A.3** Real-cloud cleanup automation (orphan-resource detection + reaper).

## Why this phase is honest

Import is a one-shot read. Apply is the lifetime of a Terraform-managed resource. **If Apply is honest end-to-end, the shim is a migration tool;** if not, it's a partial proof. Phase 10 is what makes shimanism's promise — "keep your AWS-shaped Terraform; point it at any backend" — actually true for users, not just for the import test suite.

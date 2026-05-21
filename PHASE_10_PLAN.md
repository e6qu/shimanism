# Phase 10 — Cross-cloud `terraform apply` through shimanism

> **Goal:** Phase 9 proved the shim is a transparent read surface for `terraform import`. Phase 10 extends the proof to the *write* side: a user's `terraform apply` against the shim provisions, updates, and destroys resources on the destination backend cloud, with the source-cloud provider unaware of the translation.
>
> Apply is the everyday Terraform workflow. Honest Apply makes shimanism **a cross-cloud IaC control-plane migration tool** — *not* a full migration tool. Users still need data movement, secret value/version history transfer, DB snapshots/replication, cache warmup, queued-message drain, pubsub backlog/subscription replication, function artifact transfer, custom domain + cert provisioning, IAM rebinding, DNS swap, validation, rollback, and cleanup. Phase 10-A and follow-on phases address those; Phase 10 itself ships the IaC plumbing.

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

### 1. Create-then-Read drift audit (necessary, not sufficient)

For each (resource type, source cloud) pair, write a test that:

1. `terraform apply` against the shim — drives Create.
2. `terraform plan -refresh-only -detailed-exitcode` — drives Read against the same shim.
3. Assert exit code 0.

**Important caveat (per codex review):** create-then-read will *not* catch **self-consistent wrongness** — Create translates an attribute to the wrong backend behavior, and Read translates that same wrong backend state back into the expected source shape. The user sees no drift but the resource is semantically wrong. It also won't catch:

- Invalid-input error fidelity (does the shim reject malformed input with the source cloud's *real* error envelope, or accept-and-mangle?).
- Data-plane behavior beyond the control-plane CRUD (object reads after Put through a different frontend; secret value-history retrieval after rotation through cloud A).
- Concurrency / idempotency (two Apply runs at once; Create-Then-Retry-Create).
- Delayed async failures (Create returns Pending; backend rejects at apply-completion time, not request time).
- Delete recovery behavior (soft-delete window honored across translations; purge permissions; name reuse).
- Provider-unmodeled fields (attributes the cloud returns that the Terraform provider ignores but other tools care about).

Phase 10 therefore adds **semantic conformance tests beyond create-then-read**: read-through-the-other-frontend (sub-phase 10.2-B), explicit invalid-input fidelity (10.2-C), and a per-service "apply intersection contract" (10.0-A, below) that constrains the matrix to operations whose semantics actually converge across backends.

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

### 4. Soft-delete semantics (narrowed per codex review)

Codex flagged the earlier draft's translation table as too lossy: AWS `recovery_window_in_days`, Azure `soft_delete_retention_days`, and GCS default-30 are *not* equivalent across recoverability, purge permissions, versioning / object-delete markers, name reuse, and force-delete semantics. "Default-30" in particular fabricates intent the source HCL may not express. Reworked policy:

- **Recoverable delete is an opt-in intersection feature**, not a default. The user must declare a retention window in source-cloud HCL; without it, the shim hard-deletes and Terraform's destroy completes synchronously.
- **Cross-cloud retention** is only honored where the destination backend exposes a *first-class* soft-delete primitive that the shim can configure. Where it doesn't, the shim returns the source cloud's `OperationNotSupported` envelope on a destroy with a retention window — *not* a silent hard-delete.
- **Queue soft-delete is dropped from Phase 10 scope.** Queues don't have a peer concept on AWS / GCP / Azure / NATS; the earlier draft was wrong to include it.
- The honest cross-cloud table only covers (secrets + storage) and specifies *which* cells honor retention. Storage cells map AWS S3 versioning ↔ GCS Object Versioning ↔ Azure Blob soft-delete ↔ MinIO versioning. Secrets cells map AWS Secrets Manager ↔ Azure Key Vault soft-delete ↔ GCP Secret Manager (no soft-delete — out of intersection) ↔ Vault (KV destroy is hard).

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
| **10.0-A** | ◻ | **Per-service Apply Intersection Contract** — *new from codex review*. Before any test code, write `services/<svc>/APPLY_INTERSECTION.md` enumerating exactly which Create / Update / Delete ops the shim claims honest semantics for, with the per-cell translation specified. The matrix tests assert against *this contract*, not "everything the provider tries." Stops the matrix-explosion failure mode. |
| **10.1** | ◻ | **Gate: close BUG-5 family** — GCP Operations.get across rdbms / cache / functions / apigateway. No Phase 10 Apply cell may run before this lands; Apply against GCP-shaped frontends will hang on async ops without it. |
| **10.2** | ◻ | Create-then-Read drift audit per service. Build the `terraform apply` test scaffolding. |
| **10.2-B** | ◻ | **Cross-frontend read** — *new*. After Create through frontend A, drive Read through frontend B (same service, same backend). Catches self-consistent-wrongness that single-frontend create-then-read misses. |
| **10.2-C** | ◻ | **Invalid-input fidelity** — *new*. For each Create / Update, exercise known-bad inputs (wrong name format, missing required field, conflicting attributes) and assert the shim returns the source cloud's *real* error envelope rather than fabricating success or passing through a generic 500. |
| **10.3** | ◻ | Update intersection audit per service. In-place vs replace per backend cell, with the source cloud's "operation not supported in update" surfaced when intent doesn't translate. |
| **10.4** | ◻ | Soft-delete intersection across secrets + storage (queue dropped per codex review). Opt-in only; non-supporting backends return source cloud's not-supported envelope on retention-windowed destroy. |
| **10.5** | ◻ | Per-service `apply_test.go` covering Create → Read-check → Update → Read-check → Destroy. Assert against 10.0-A's contract. |
| **10.6** | ◻ | Cross-cloud Apply matrix tests, contract-scoped. |
| **10.7** | ◻ | Exit criterion: `TestCrossCloudApply_Roundtrip` per service. |
| **10.8** | ◻ | Phase 10 closer. |

## Phase 10-A (real-cloud Apply, Track A)

The mock-tier Apply matrix is a precondition; real-cloud Apply is the *headline* migration test. Once mock-tier Phase 10 closes:

- **10-A.1** Real-cloud `terraform apply` lanes per (frontend, backend) where backend = a real cloud account.
- **10-A.2** Snapshot/backup-based data migration tests for rdbms + cache.
- **10-A.3** Real-cloud cleanup automation (orphan-resource detection + reaper).

## Why this phase is honest

Import is a one-shot read. Apply is the lifetime of a Terraform-managed resource. **If Apply is honest end-to-end, the shim is a cross-cloud IaC control-plane migration tool** — the foundation a real migration is built on, not the migration itself.

## Codex review (and what we changed in response)

Submitted to `codex exec` for an independent review. Five critiques returned; each is addressed above.

1. *"'Migration tool' is overclaim — Apply doesn't move data."* **Accepted.** Goal section now reads "cross-cloud IaC control-plane migration tool" and enumerates what's still missing (data, snapshots, IAM, DNS, etc.).
2. *"Create-then-Read misses self-consistent wrongness + several other classes of bug."* **Accepted.** Added sub-phases 10.2-B (cross-frontend read after cross-cloud write) and 10.2-C (invalid-input fidelity). The drift audit is now explicitly necessary-but-not-sufficient.
3. *"Deferring BUG-5 into Phase 10 is right only if it's the gate, not cleanup."* **Accepted.** Sub-phase 10.1 is marked as a hard gate; no Apply cell runs before it lands.
4. *"Soft-delete table is too lossy; queue soft-delete is implausible."* **Accepted.** Queue dropped from soft-delete scope. Retention-window translation is opt-in only; non-supporting cells return source cloud's `OperationNotSupported`.
5. *"Most likely failure mode: matrix explodes before semantics converge."* **Accepted.** Sub-phase 10.0-A inserted as the first deliverable — per-service Apply Intersection Contract before any test code. Matrix tests assert against the contract, not against "whatever the provider tries."

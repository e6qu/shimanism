# FAQ

## Is shimanism an emulator?

No. The shim doesn't store data. It speaks the source cloud's wire protocol on the front and forwards each call to a real backend — another cloud, a Kubernetes operator, or a self-hosted server. The data lives in that real backend.

For local-dev emulation, use [LocalStack](https://localstack.cloud/). shimanism is for production migration.

## Does the shim ever lie?

No. That's the [PHILOSOPHY.md](../PHILOSOPHY.md) "never lie" rule. Concretely:

- If the destination backend can honor the call, the shim forwards it.
- If the destination can't honor the call (out-of-intersection feature, semantic asymmetry), the shim returns the *source cloud's own* "not supported" error envelope. Never a fabricated success. Never a silent fallback.

The per-service `INTERSECTION.md` audits classify every wire operation as real-work / feature-unset / out-of-intersection. A fourth implicit category — "returns something plausible without doing real work" — is by definition a fake and gets filed as a [BUGS.md](../BUGS.md) entry.

## What happens when a feature has no peer across clouds?

It's not shimmed. The intersection-only constraint is the load-bearing design choice.

When a user's code calls an out-of-intersection feature, the shim returns the source cloud's `NotImplemented`, `OperationNotSupported`, or `InvalidParameter` envelope — whatever the cloud's published API specifies for "this feature isn't available." The user's error-handling code sees what it would have seen had the cloud rejected the call directly.

This is testable, not a vibes-based claim. See [docs/testing.md § invalid-input fidelity](testing.md) and the per-service `APPLY_INTERSECTION.md` for the out-of-contract lists.

## Why not just port the application?

You can. shimanism is for the case where:

- Porting is expensive (lots of call sites, multiple teams, multi-year codebase).
- Cutover risk is high (you can't take the application down).
- You want reversible migration that reroutes services one at a time, not big-bang.
- You want to keep using the existing tooling (CLI scripts, Terraform modules, CI/CD).

If your application is small and you have time, port it — that's the lower-overhead long-term answer. shimanism is the *on-ramp* for the cases where porting isn't on the table.

## How does shimanism compare to LocalStack / Crossplane / Dapr / gocloud.dev / Terraform / MinIO?

Detailed comparison in [docs/comparison.md](comparison.md). Quick summary:

- **LocalStack** is an emulator for local dev/test (not production, not real data).
- **MinIO / R2 / B2** are S3-compatible *backends* (shimanism uses them as backends).
- **Crossplane** is multi-cloud IaC via Kubernetes CRDs (different abstraction layer).
- **Dapr / gocloud.dev** require rewriting the application to use their API (different target — greenfield apps).
- **Terraform / Pulumi / CDK** are IaC tools (shimanism works *with* them; the shim is a runtime layer).

## Does the shim hold state?

No. The shim binary is stateless. Every answer comes from the backend that actually owns the data:

- No sidecar storage. No SQLite, no Redis, no shim-managed namespace.
- No in-process cache treated as authoritative.
- Multipart-upload state lives in the destination (GCS multipart parts go in GCS itself, etc.).
- Cross-cloud shape translations (e.g. Azure GUID version IDs ↔ AWS's monotonic integers) are *derived at request time* by listing versions and sorting by creation timestamp.
- Async-operation polling (`Operations.Get`) encodes `(opType, target)` into the Operation Name so the polling client resolves status by re-reading the underlying resource.

See [AGENTS.md § the shim is stateless](../AGENTS.md#the-shim-is-stateless).

## Can the shim scale horizontally?

Yes — that's a direct consequence of statelessness. Any replica answers any request. Restarts are clean. No warm-up, no recovery. No risk of last-writer-wins or split-brain.

## What does the shim *not* do?

- **Doesn't move data.** If you're migrating storage with existing content, run a separate data-copy step (`rclone`, `gsutil`, AWS DataSync, etc.). The shim handles the wire-protocol layer; the bytes movement is your job.
- **Doesn't rebind identity.** AWS IAM roles don't translate to GCP service accounts. Identity is a real migration step the shim can't automate.
- **Doesn't handle DNS / custom domains / certs.** Those are platform-level concerns separate from the wire-protocol shim.
- **Doesn't snapshot / replicate databases.** For RDBMS / cache migration, you need a separate snapshot or replication step. The shim provisions the destination instance + returns the connection metadata; data movement is the user's responsibility.

Per [PHASE_10_PLAN.md](../PHASE_10_PLAN.md): shimanism is a *cross-cloud IaC + control-plane migration tool*, not a full migration tool.

## Does shimanism need cloud credentials?

The shim itself doesn't authenticate to the source cloud — clients sign their requests with whatever the source cloud expects (SigV4 for AWS, OAuth for GCP, SharedKey/AAD for Azure). The plan is to verify those signatures at the wire-decode boundary using the cloud's official signer/verifier libraries (per [AGENTS.md § reuse](../AGENTS.md#reuse-over-reinvention)). **Today, signature verification isn't wired** — the conformance harness uses `skip_credentials_validation`, `option.WithoutAuthentication()`, and stub bearer tokens because the shim accepts unsigned/malformed-signature requests. This is tracked as [BUG-18](../BUGS.md). It must close before the shim is exposed to untrusted traffic.

The shim *does* need credentials for the **destination backend** — the AWS / GCP / Azure SDK auth for whatever cloud is on the receiving side. Those go in the shim's own config (env vars, IAM roles, workload identity, etc.).

## Is the shim safe to put in front of production traffic?

Not yet. Two prerequisites are open:

1. **[BUG-18](../BUGS.md)** — frontends accept unsigned requests today (see above). Untrusted clients can call the shim without authentication. The shim must wire the cloud's official signer/verifier libraries before it's exposed to untrusted traffic.
2. **Track A real-cloud lanes.** Conformance against real AWS / GCP / Azure (vs the inmem + emulator backends) is gated on cloud test accounts; until those exist, production claims are unfounded.

Today's safe-to-use bracket: local dev, internal trusted networks, dev/test environments. Anything beyond that should wait for BUG-18 closure + Track A green.

## How do I know if my application is compatible?

Read the per-service [INTERSECTION.md](services.md) audits for each service your application uses. They classify every wire-level operation as real-work / feature-unset / out-of-intersection. If your code only calls in-intersection operations, the shim will work transparently. If it calls out-of-intersection features, those calls will fail loud at the call site — that's how you find out (not later, not silently).

## Can I run shimanism on Kubernetes?

Yes. The shim is a single Go binary; package it in a container, run as a Deployment, expose with a Service. The K8s-peer backends (CloudNativePG for Postgres, Knative for functions, etc.) are designed to be co-located.

There's no Helm chart yet — that's planned.

## Is shimanism open source?

AGPL-3.0. See [doc/COMPATIBLE_LICENSES.md](../doc/COMPATIBLE_LICENSES.md) for the dependency policy.

## Where do I report bugs?

GitHub issues at the project's repo. Before filing, please check [BUGS.md](../BUGS.md) to see if it's already known. If you're contributing a fix: [docs/contributing.md § the bug-first rule](contributing.md#the-bug-first-rule) — file the BUG in `BUGS.md` *before* the fix commit.

## Cross-link

- [README.md](../README.md) — project overview.
- [docs/architecture.md](architecture.md) — the layered model.
- [docs/comparison.md](comparison.md) — how shimanism differs from related projects.
- [docs/migration.md](migration.md) — the per-service rerouting story.
- [docs/services.md](services.md) — per-service detail.
- [PHILOSOPHY.md](../PHILOSOPHY.md) — the *why*.

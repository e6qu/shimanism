# The migration story

shimanism's primary purpose: **gradual, service-by-service cloud migration**. Move one piece at a time, verify, repeat. No big-bang rewrite, no SDK swap, no Terraform-provider port.

This document walks through what that looks like in practice.

## The classical migration problem

You're on AWS. Your stack uses:

- `aws-sdk-go-v2`, `boto3`, or similar for runtime API calls.
- `hashicorp/aws` Terraform for infrastructure-as-code.
- `aws` CLI for ops scripts and runbooks.

You want to move some piece of it — maybe just object storage — to GCP. The classical options:

1. **Big-bang rewrite.** Port everything to `cloud.google.com/go/storage`, `hashicorp/google`, `gcloud` CLI. Months of work. Risky cutover. Hard to roll back.
2. **Dual-stack the application.** Add a multi-cloud SDK like [gocloud.dev](https://gocloud.dev/) or [Dapr](https://dapr.io/). Refactor every call site that touches storage. Still months. Touches every team that owns code.
3. **IaC-first migration via [Crossplane](https://www.crossplane.io/) or similar.** Re-express your infra as CRDs. Manage cross-cloud control plane separately. Doesn't move the data plane.

None of these are "drop in one binary and the storage SDK now talks to GCS." That's the shimanism on-ramp.

## The shimanism approach

The application keeps using AWS SDK calls, AWS CLI commands, AWS Terraform modules. **You change one thing: the endpoint URL.** That URL points at a shim. The shim translates the AWS-shape calls into GCS calls against real GCS.

The pattern is per-service. Start with one — say, storage. Move it. Confirm everything still works. Then queue. Then secrets. Etc.

### Step 0: where you are

```
┌────────────────────┐                          ┌──────────────────┐
│  Application       │   aws-sdk + S3 wire      │  AWS S3          │
│  (Python / Go /    │ ──────────────────────▶  │  (real)          │
│   TypeScript / …)  │                          └──────────────────┘
│                    │
│  + Terraform AWS   │   aws-sdk + SQS wire     ┌──────────────────┐
│  + aws CLI         │ ──────────────────────▶  │  AWS SQS         │
│                    │                          │  (real)          │
│                    │                          └──────────────────┘
└────────────────────┘
```

### Step 1: pick one service to move (storage)

Decide that storage moves to GCS. Everything else stays on AWS.

### Step 2: deploy the shim

```sh
shim storage \
  -frontend=aws_s3 \
  -backend=gcs \
  -gcs-project=my-target-project \
  -addr=:9001
```

The shim now listens on `:9001` and speaks AWS S3 on the wire. Every call lands on real GCS.

### Step 3: redirect the application's storage endpoint

For the application's SDK / CLI / Terraform — set the endpoint override.

**Go (aws-sdk-go-v2):**

```go
cfg, _ := config.LoadDefaultConfig(ctx)
cfg.BaseEndpoint = aws.String("http://shim.internal:9001")
s3client := s3.NewFromConfig(cfg)
```

**Python (boto3):**

```python
s3 = boto3.client("s3", endpoint_url="http://shim.internal:9001")
```

**AWS CLI:**

```sh
aws --endpoint-url=http://shim.internal:9001 s3 ls
```

**Terraform (`hashicorp/aws`):**

```hcl
provider "aws" {
  region = "us-east-1"
  endpoints {
    s3 = "http://shim.internal:9001"
  }
}
```

The other services (SQS, RDS, etc.) are unchanged — they go straight to real AWS as before. Only storage is shimmed.

### Step 4: verify

The application's data-plane reads + writes now land on GCS. Run integration tests. Watch the GCS bucket fill up. Spot-check that nothing else broke (Terraform plans should be no-op for non-storage resources; runtime traffic to non-storage AWS services should be unchanged).

### Step 5: move the next service

Spin up another shim instance for, say, secrets:

```sh
shim secrets \
  -frontend=aws_secretsmanager \
  -backend=gcp \
  -gcp-project=my-target-project \
  -addr=:9002
```

Add the secrets endpoint override:

```hcl
provider "aws" {
  endpoints {
    s3             = "http://shim.internal:9001"
    secretsmanager = "http://shim.internal:9002"
  }
}
```

Repeat for each service the platform team decides to move.

### Step 6: cut over fully (eventually)

Once enough services are on the destination cloud that the source-cloud bill is mostly cleaned up, the team can decide: keep the shim as a permanent translation layer (multi-cloud forever), or do a code-level cutover for the last few callers. shimanism doesn't force either choice.

## What gets the per-service treatment

Per-service docs under [docs/services/](services/) cover exactly what each shim supports and what's out-of-intersection. The two contracts to read before adopting:

- **`INTERSECTION.md`** — every wire-level operation classified as real-work / feature-unset / out-of-intersection. Helps you know in advance whether your application uses anything that won't translate.
- **`APPLY_INTERSECTION.md`** — the Terraform `apply` contract per service. Same shape: which attributes round-trip, which are out-of-contract.

If your application uses an out-of-intersection feature (e.g. S3 Object Lambda, or AWS-only KMS configurations), the shim returns the source cloud's *real* "not supported" error. That's intentional — you find out at the call site, not after a silent data-correctness bug.

## What about the data?

shimanism is the **control + wire-protocol** layer. It doesn't move data for you. Two patterns:

- **Cutover service-by-service for new state only.** The shim points at the destination; old data on the source stays where it is. Acceptable for stateless services (queues, pub/sub) and stateless-ish ones (functions). For storage with existing data, you need a separate data-migration step.
- **Pre-populate the destination, then cutover.** Run a migration tool (cloud-native ones like AWS DataSync to GCP STS, or rclone, or `gsutil`+`aws s3 sync`) to copy historical data. Then flip the shim's backend pointer to the destination. shimanism stays out of the data-copy step.

Per the codex review in [PHASE_10_PLAN.md](../PHASE_10_PLAN.md): **shimanism is a cross-cloud IaC + control-plane migration tool, not a full migration tool.** Data movement, secret value/version history transfer, DB snapshots/replication, cache warmup, queued-message drain, pubsub backlog/subscription replication, function artifact transfer, custom domain + cert provisioning, IAM rebinding, DNS swap, validation, rollback, and cleanup — those are user responsibilities (or other tools' jobs).

## Rollback

The endpoint override is the only thing that changed. Roll it back, application traffic returns to the original cloud immediately. The shim can stay deployed (no-op) or be removed.

This is the actual value-prop: **the migration is reversible at the URL level**, not at the code level.

## Failure modes shimanism explicitly handles

- **A feature the source cloud has and the destination doesn't:** the shim returns the source cloud's "not supported" error envelope. The application's error-handling code sees what it would have seen if AWS rejected the call. No silent degradation.
- **Async-operation differences:** every cloud's "wait for this operation to complete" path is wired (`Operations.Get` for GCP; ARM `Azure-AsyncOperation` for Azure; status polling for AWS). The application's existing retry / wait code keeps working.
- **Error envelope fidelity:** `<Error><Code>NoSuchBucket</Code></Error>` from real S3 is `<Error><Code>NoSuchBucket</Code></Error>` from the shim. No code change in the application's error-handling.

## Failure modes shimanism does *not* hide

- **Cross-cloud feature mismatch.** If your code depends on an out-of-intersection feature, the shim fails loud. Read the per-service `INTERSECTION.md` before adopting.
- **Identity rebinding.** AWS IAM roles don't translate to GCP service accounts. Re-binding identity is a real migration step the shim can't automate. See [docs/services/functions.md § Role + Publish](services/functions.md#role--publish-aws-only) for one example.
- **Data movement.** Above.

## When shimanism is a good fit

- You're moving from one cloud to another, gradually.
- You want to stay on your existing SDK / CLI / IaC tooling.
- You can tolerate the intersection-only constraint (you'll find out at adoption time which features don't translate).
- You want reversibility at the URL level, not the code level.

## When it isn't

- You're building a new application from scratch — [gocloud.dev](https://gocloud.dev/) or [Dapr](https://dapr.io/) or a cloud-agnostic library are the right choice from day one.
- You only need local dev emulation — use [LocalStack](https://localstack.cloud/).
- You want IaC-level multi-cloud control via CRDs — use [Crossplane](https://www.crossplane.io/).

See [docs/comparison.md](comparison.md) for the longer-form comparison.

## Cross-link

- [docs/architecture.md](architecture.md) — the layered model that makes this approach work.
- [docs/comparison.md](comparison.md) — how shimanism differs from related projects.
- [docs/services.md](services.md) — per-service detail.
- [doc/CROSS_CLOUD_ROUTING.md](../doc/CROSS_CLOUD_ROUTING.md) — wire-level walkthrough.
- [PHASE_10_PLAN.md](../PHASE_10_PLAN.md) — the "control-plane migration tool, not full migration tool" framing.

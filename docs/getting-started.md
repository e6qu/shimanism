# Getting started

A five-minute walkthrough: install the shim, point your AWS CLI at it, watch a bucket created via `aws s3` land on a non-AWS backend.

## Prerequisites

- Go ≥ 1.26 (build only; the shim binary doesn't require a Go runtime to use).
- The AWS CLI installed and on `PATH` (any recent version; the shim targets the v4 SDK surface).
- Optional: `gcloud`, `az`, `terraform` if you want to drive the shim from those.

## Build

```sh
git clone https://github.com/e6qu/shimanism
cd shimanism
go build ./cmd/shim
```

This produces a single `shim` binary in the working directory.

## Run the storage shim against an in-memory backend

```sh
./shim storage -backend=inmem -addr=:9001
```

This starts the storage service with an in-process inmem backend on port 9001. The inmem backend is intended for development and testing; for production use, swap it for `-backend=minio` (or `aws`, `gcs`, `azureblob`).

## Point the AWS CLI at the shim

The AWS CLI needs *some* credentials + region to construct a SigV4 signature, even though the shim doesn't validate signatures today (see [BUG-18](../BUGS.md)). Stub values work — the shim runs in your trust domain:

```sh
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

aws --endpoint-url=http://localhost:9001 s3 mb s3://my-bucket
aws --endpoint-url=http://localhost:9001 s3 cp README.md s3://my-bucket/
aws --endpoint-url=http://localhost:9001 s3 ls s3://my-bucket/
```

That's it. The CLI doesn't know it's not talking to real S3. The bucket lives in the inmem backend — swap to MinIO / GCS / Azure Blob and the same commands work, with the data on a real backend.

## What just happened

```
┌──────────────┐  S3 wire (SigV4, XML)  ┌────────────────┐  inmem call  ┌────────────┐
│  aws CLI     │ ─────────────────────▶ │  shim:9001     │ ───────────▶ │  inmem map │
│  (boto3 too) │ ◀───────────────────── │  (S3 frontend) │ ◀─────────── │  in-process│
└──────────────┘                        └────────────────┘              └────────────┘
```

The CLI's S3 SDK signed the request with SigV4 and posted it to `localhost:9001`. The shim's AWS S3 frontend received the request, decoded the wire format into `domain.Storage.PutObject(...)`, and the inmem backend stored the bytes in a Go map. The response went back as a real S3 XML envelope.

## Try a different backend

Stop the shim and rerun with MinIO:

```sh
# In one terminal: a real MinIO server
docker run --rm -p 9000:9000 -e MINIO_ROOT_USER=minio -e MINIO_ROOT_PASSWORD=miniopass \
  quay.io/minio/minio server /data

# In another: shim pointing at the MinIO server
./shim storage -backend=minio \
  -minio-endpoint=localhost:9000 \
  -minio-access-key=minio \
  -minio-secret-key=miniopass \
  -addr=:9001

# Same client commands as before
aws --endpoint-url=http://localhost:9001 s3 mb s3://migrated-bucket
```

The data is in MinIO. The CLI still thinks it's talking to S3. That's the whole product.

## Try a different frontend

The shim also speaks GCS and Azure Blob on the *front* — same MinIO data on the back, accessed via three different SDKs:

```sh
# GCS frontend on port 9002
./shim storage -backend=minio -minio-endpoint=localhost:9000 \
  -minio-access-key=minio -minio-secret-key=miniopass \
  -frontend=gcs -addr=:9002 &

# Use the gcloud CLI against it. gcloud's storage endpoint override
# is the CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE env var (the
# --api-endpoint-overrides flag isn't supported on `gcloud storage`).
CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE=http://localhost:9002/ \
  gcloud storage cp README.md gs://my-bucket/
```

The same bytes are now accessible via S3, GCS, and Azure Blob SDKs against three frontend ports, all backed by the same MinIO instance.

## Next

- **[Architecture](architecture.md)** — the front / domain / back layers, statelessness, intersection-only scope.
- **[Service catalog](services.md)** — the other shimmed services and what each covers (per-service detail under [`docs/services/`](services/)).
- **[Cross-cloud routing](../doc/CROSS_CLOUD_ROUTING.md)** — the migration story: how user A's AWS Terraform points at the shim and the bytes land on cloud B.
- **[FAQ](faq.md)** — "but doesn't this break X?" — answered.

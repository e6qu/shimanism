# Object storage — migration walkthroughs

> Phase 9 sub-phase 9.2-B. Each walkthrough is a runnable sequence that exercises the intersection end-to-end. If a step fails or requires a fake, the intersection has a gap — file a BUG.

## AWS S3 → GCS (cross-cloud)

**User goal:** migrate a bucket from AWS S3 to GCS without rewriting AWS-shaped Terraform / tooling.

```bash
# 1. Start the shim with the GCS backend.
shim storage --addr=:9000 \
  --frontend=aws_s3 \
  --backend=gcs --gcs-project=$GCP_PROJECT &

# 2. Point AWS-shaped tooling at the shim.
eval "$(shimctl env --frontend=aws --service=storage --endpoint=http://localhost:9000)"

# 3. Migration ops — every one of these is in the intersection.
aws s3 ls                                              # ListBuckets
aws s3 mb s3://migrating-bucket                        # CreateBucket
aws s3 cp ./data s3://migrating-bucket/ --recursive    # PutObject + multipart
aws s3 ls s3://migrating-bucket/                       # ListObjectsV2
aws s3 cp s3://migrating-bucket/file ./out             # GetObject
aws s3 rm s3://migrating-bucket/file                   # DeleteObject

# 4. Verify the objects landed in GCS directly (no shim involved).
gsutil ls gs://migrating-bucket/
```

**Intersection coverage:** ListBuckets, CreateBucket, PutObject (incl. multipart), ListObjectsV2, GetObject, DeleteObject. All ✅ from [INTERSECTION.md](INTERSECTION.md). Walkthrough holds.

## AWS S3 → MinIO (cloud → K8s peer)

**User goal:** repatriate workload data into a self-hosted MinIO inside Kubernetes.

```bash
shim storage --addr=:9000 \
  --frontend=aws_s3 \
  --backend=minio --minio-endpoint=$MINIO_HOST &
eval "$(shimctl env --frontend=aws --service=storage --endpoint=http://localhost:9000)"

# Same six ops above. The shim translates SigV4-signed S3 requests into
# the MinIO client's API; MinIO is itself S3-compatible so this is a
# thin translation.
```

**Walkthrough holds.** This is the lift-and-shift cloud-to-K8s path.

## GCS → Azure Blob (cross-cloud, both directions)

**User goal:** GCP user moving to Azure but keeping their `gsutil` and `cloud.google.com/go/storage` code unchanged.

```bash
shim storage --addr=:9000 \
  --frontend=gcs \
  --backend=azureblob --azure-connection-string=$AZURE_CONN &
eval "$(shimctl env --frontend=gcp --service=storage --endpoint=http://localhost:9000)"

gcloud storage ls
gcloud storage buckets create gs://migrating-bucket --location=us
gcloud storage cp ./data gs://migrating-bucket/ --recursive
gcloud storage ls gs://migrating-bucket/
```

**Walkthrough holds.** Symmetric with the AWS → GCS path.

## What the walkthroughs reveal

- All four backend cells (AWS / GCS / Azure / MinIO) support the migration-critical ops with no per-cell exceptions.
- No fake responses are produced anywhere on the migration path.
- Failure modes (e.g. bucket-already-exists, object-not-found) propagate the source cloud's real error envelope.

Storage is the cleanest example — Phase 1 set the pattern, Phases 2–8 followed it.

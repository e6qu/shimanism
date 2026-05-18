# K8s peer (Phase 1.8)

The "leave the cloud entirely" path. The shim binary runs as a Deployment
inside a Kubernetes cluster alongside a MinIO StatefulSet. AWS-shaped
clients (SDK / CLI / Terraform provider) configured with the shim's
ClusterIP as their endpoint drive S3 operations into the shim, which
translates to MinIO via the neutral `domain.Storage` interface.

```
SDK / CLI / TF  --[S3 wire]-->  shim (this Deployment)  --[domain.Storage]-->  MinIO (StatefulSet)
```

## Topology

| Object | Kind | Role |
|---|---|---|
| `minio` | StatefulSet | Single-replica MinIO with a 10 GiB PVC. |
| `minio` | Service | ClusterIP `minio.<ns>.svc:9000` for in-cluster access. |
| `shim` | Deployment | 2 replicas of `cmd/shim` configured with `-backend=minio -minio-endpoint=minio:9000`. |
| `shim` | Service | ClusterIP `shim.<ns>.svc:9000` — the S3 endpoint clients point at. |
| `shim-credentials` | Secret | MinIO root user + password the shim authenticates with. |

## Quickstart

```sh
kubectl create namespace shimanism
kubectl -n shimanism apply -k deploy/k8s/peer/

# Forward the shim's port locally to drive it with the AWS CLI.
kubectl -n shimanism port-forward svc/shim 9000:9000 &

aws --endpoint-url http://localhost:9000 \
    --region us-east-1 \
    s3 mb s3://hello
aws --endpoint-url http://localhost:9000 s3 ls
```

## Production notes

- **Replicas.** The shim Deployment scales horizontally because the
  shim is stateless — all object state lives in MinIO.
- **MinIO replication.** The single-replica StatefulSet here is for
  the simplest "shim demonstrably works in K8s" deployment. For
  durability, use the official MinIO Operator
  (https://github.com/minio/operator) and point `-minio-endpoint` at
  its service.
- **TLS.** The shim talks plain HTTP between itself and MinIO. Add an
  ingress with cert-manager in front of the shim Service for client
  traffic; mTLS to MinIO is a future improvement.
- **Credentials rotation.** Update the Secret and roll the Deployment.
  No state migration is needed because state lives in MinIO.
- **Image pinning.** The manifests reference container image tags;
  Renovate is configured to bump them weekly. For air-gapped or
  strict environments, pin to a digest (`@sha256:…`) and disable
  Renovate's "pin" target for these files.

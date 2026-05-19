# Multi-stage Dockerfile for the shim binary. The image is consumed by
# deploy/k8s/peer/ (the K8s peer deployment) and any downstream
# operator who wants to run the shim alongside their MinIO / cloud
# backend.
#
# Build:  docker build -t ghcr.io/e6qu/shimanism:<version> .
# Run:    docker run -p 9000:9000 ghcr.io/e6qu/shimanism:<version> \
#             storage -backend=inmem

# Stage 1 — build. The Go version is pinned to match go.mod; bump
# both together. Renovate watches the digest.
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache modules independently of source.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Stripped binary, no cgo, deterministic.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/shim ./cmd/shim

# Stage 2 — runtime. Distroless static keeps the surface area small.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/shim /shim

EXPOSE 9000
USER nonroot:nonroot
ENTRYPOINT ["/shim"]
CMD ["storage"]

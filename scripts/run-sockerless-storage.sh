#!/usr/bin/env bash
#
# Drives the sockerless validation lane.
#
# Prerequisites:
#   - A local clone of github.com/e6qu/sockerless. Default location
#     /tmp/sockerless; override via SOCKERLESS_DIR.
#   - openssl for self-signed cert generation (AWS sim TLS).
#
# What it does:
#   1. Builds the AWS + GCP simulator binaries (-tags noui, no UI dist
#      required) inside the sockerless clone if they don't exist yet.
#   2. Generates an ephemeral self-signed TLS cert for the AWS sim.
#      (Sockerless serves HTTP by default; the aws-sdk-go-v2 SDK
#      refuses streaming-signed payloads over plain HTTP.)
#   3. Starts the sims on test-only ports (:14566 AWS over TLS,
#      :14567 GCP, :14569 Azure over TLS).
#   4. Runs the sockerless-tagged Go tests with env vars that point
#      the shim's frontends and backends at the sims.
#   5. Tears the sims down on exit.
#
# The AWS sim runs under TLS so the SDK's streaming-signed payload
# path works; the test clients trust the self-signed cert via
# AWS_S3_CONFORMANCE_INSECURE_TLS=1.

set -euo pipefail

SOCKERLESS_DIR=${SOCKERLESS_DIR:-/tmp/sockerless}
AWS_PORT=${AWS_PORT:-14566}
GCP_PORT=${GCP_PORT:-14567}
AZURE_PORT=${AZURE_PORT:-14569}
# Service Bus raw AMQP/TLS lives on its own listener (sockerless PR
# #231). Used by the SB queue / pubsub Send-Receive lanes via the
# azservicebus SDK's CustomEndpoint + TLSConfig knobs.
AZURE_SB_AMQP_PORT=${AZURE_SB_AMQP_PORT:-14570}
# Fixed port where the shim's Azure Blob data-plane frontend binds
# when running the through-shim `azurerm` Terraform Apply test
# (`TestSockerless_E2E_AzureBlob_Through_Shim_ApplyTF`). sockerless's
# Azure ARM is configured below to emit this URL in
# `Microsoft.Storage/storageAccounts` `primaryEndpoints.blob` so
# `azurerm` follows it for data-plane operations. The shim binds the
# port in-test rather than the harness using a random httptest port,
# because the URL has to be fixed before sockerless starts.
SHIM_AZUREBLOB_PORT=${SHIM_AZUREBLOB_PORT:-14581}
# Fixed port where the shim's Azure Key Vault data-plane frontend
# binds when running the through-shim `azurerm` KV Apply test
# (`TestSockerless_E2E_AzureKV_Through_Shim_ApplyTF`). Mirror of the
# blob slot: sockerless emits this URL in
# `Microsoft.KeyVault/vaults` `properties.vaultUri` so `azurerm`
# follows it for data-plane secret PUT, the shim verifies the
# RS256 Bearer token against sockerless's published JWKS, then
# translates onto the chosen secrets backend.
SHIM_AZUREKV_PORT=${SHIM_AZUREKV_PORT:-14582}
CERT_DIR=${CERT_DIR:-/tmp/sockerless-tls}

if [[ ! -d $SOCKERLESS_DIR ]]; then
    echo "ERR: $SOCKERLESS_DIR not present. Clone github.com/e6qu/sockerless first:" >&2
    echo "       git clone --depth=1 https://github.com/e6qu/sockerless.git $SOCKERLESS_DIR" >&2
    echo "     or set SOCKERLESS_DIR to an existing clone." >&2
    exit 2
fi

# Sockerless's sims require a container runtime (podman/docker) — fail
# fast with a clear message rather than letting the sim start and
# crash on the first request with FATAL: Docker/Podman not available.
CONTAINER_RUNTIME=""
require_container_runtime() {
    if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
        CONTAINER_RUNTIME=docker
        return 0
    fi
    if command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
        CONTAINER_RUNTIME=podman
        return 0
    fi
    echo "ERR: sockerless sims require a running container runtime (podman or docker)." >&2
    echo "     On macOS: 'podman machine start' or open Docker Desktop." >&2
    echo "     On Linux: ensure the docker daemon is running." >&2
    exit 3
}
require_container_runtime

# Container Apps + Cloud Run sockerless handlers do real container
# execution: each `POST /containerApps` or `POST /services` boots a
# real local replica via the resolved container runtime, matching
# real Azure / GCP behaviour (the simulators chose real execution,
# not control-plane mocks). Pre-pull the reference images pinned to
# the host arch via `go env GOARCH` so the lanes don't pay first-
# request pull latency.
#
# Both default to `docker.io/library/nginx:alpine` — tiny (~20 MB),
# runs without args, reliably reachable from public registries. Both
# sockerless handlers derive the container platform from the resolved
# image manifest, so the same image works on arm64 and amd64 hosts.
# Override either via:
#
#   SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE
#   SOCKERLESS_GCP_CLOUDRUN_IMAGE
GO_ARCH=$(go env GOARCH 2>/dev/null || echo amd64)
PULL_PLATFORM="linux/${GO_ARCH}"
: "${SOCKERLESS_GCP_CLOUDRUN_IMAGE:=docker.io/library/nginx:alpine}"
: "${SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE:=docker.io/library/nginx:alpine}"
for image in "$SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE" "$SOCKERLESS_GCP_CLOUDRUN_IMAGE"; do
    if [[ -z "$image" ]]; then continue; fi
    echo "pre-pull: $image (--platform=$PULL_PLATFORM) via $CONTAINER_RUNTIME"
    "$CONTAINER_RUNTIME" pull --platform="$PULL_PLATFORM" "$image" >/dev/null 2>&1 || echo "WARN: pre-pull of $image failed — affected lane will skip." >&2
done

cleanup() {
    if [[ -n ${AWS_PID:-} ]]; then kill "$AWS_PID" 2>/dev/null || true; fi
    if [[ -n ${GCP_PID:-} ]]; then kill "$GCP_PID" 2>/dev/null || true; fi
    if [[ -n ${AZURE_PID:-} ]]; then kill "$AZURE_PID" 2>/dev/null || true; fi
    wait 2>/dev/null || true
}
trap cleanup EXIT INT TERM

build_sim() {
    local provider=$1
    local out=$2
    if [[ -x $out ]]; then return; fi
    echo "build: simulator-$provider → $out"
    (cd "$SOCKERLESS_DIR/simulators/$provider" && \
        GOWORK=off CGO_ENABLED=0 go build -tags noui -o "$out" .)
}

ensure_cert() {
    mkdir -p "$CERT_DIR"
    # Cached cert is reused only if it already carries the SAN list
    # the test needs (covers `*.vault.localhost` for the KV cell).
    # Older cached certs from prior runs without that SAN must be
    # regenerated, else the shim's KV TLS handshake fails.
    if [[ -s "$CERT_DIR/sim.crt" && -s "$CERT_DIR/sim.key" ]] \
        && openssl x509 -in "$CERT_DIR/sim.crt" -noout -ext subjectAltName 2>/dev/null \
            | grep -q "DNS:\*\.vault\.localhost"; then
        return
    fi
    echo "cert: generating self-signed RSA-2048 cert in $CERT_DIR"
    # Go's TLS stack rejects certs that rely on the deprecated CN
    # field for hostname matching — modern verifiers require Subject
    # Alternative Names. Cover every host the test process talks to:
    #   - `localhost` + 127.0.0.1 — sockerless itself.
    #   - `*.vault.localhost` — the shim's KV data-plane endpoint
    #     when sockerless emits `https://{vault}.vault.localhost:.../`
    #     as `properties.vaultUri`. (Blob doesn't need the cert: it
    #     runs over plain HTTP since SharedKey is the auth path.)
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
        -subj "/CN=localhost" \
        -addext "subjectAltName=DNS:localhost,DNS:*.vault.localhost,IP:127.0.0.1" \
        -keyout "$CERT_DIR/sim.key" \
        -out "$CERT_DIR/sim.crt" \
        >/dev/null 2>&1
}

AWS_BIN="$SOCKERLESS_DIR/simulators/aws/simulator-aws"
GCP_BIN="$SOCKERLESS_DIR/simulators/gcp/simulator-gcp"
AZURE_BIN="$SOCKERLESS_DIR/simulators/azure/simulator-azure"

build_sim aws "$AWS_BIN"
build_sim gcp "$GCP_BIN"
build_sim azure "$AZURE_BIN"
ensure_cert

echo "start: AWS sim → https://localhost:$AWS_PORT (TLS, self-signed)"
SIM_LISTEN_ADDR=":$AWS_PORT" \
SIM_TLS_CERT="$CERT_DIR/sim.crt" \
SIM_TLS_KEY="$CERT_DIR/sim.key" \
    "$AWS_BIN" >/tmp/sockerless-aws.log 2>&1 &
AWS_PID=$!

echo "start: GCP sim → http://localhost:$GCP_PORT"
SIM_LISTEN_ADDR=":$GCP_PORT" \
    "$GCP_BIN" >/tmp/sockerless-gcp.log 2>&1 &
GCP_PID=$!

echo "start: Azure sim → https://localhost:$AZURE_PORT + Service Bus AMQP/TLS on :$AZURE_SB_AMQP_PORT (self-signed cert)"
# Configure sockerless's Azure ARM (sockerless#259) to emit the
# shim's blob frontend URL in storage-account
# `primaryEndpoints.blob`. The `{account}` placeholder is
# interpolated per-storage-account by sockerless#269/#271, which
# also derives `suffixes.storage` in `/metadata/endpoints` from the
# emitted URL's suffix. `azurerm`'s endpoint parser accepts the
# resulting `https://<account>.blob.<suffix>/...` shape; the
# `.localhost` TLD resolves to 127.0.0.1 per RFC 6761, so the
# data-plane PUTs land on whatever the shim binds at this port
# without DNS or /etc/hosts edits. sockerless's `listKeys` returns
# a deterministic 64-byte key (sockerless#260) the shim's verifier
# derives the same way from the resource ID.
SIM_LISTEN_ADDR=":$AZURE_PORT" \
SIM_TLS_CERT="$CERT_DIR/sim.crt" \
SIM_TLS_KEY="$CERT_DIR/sim.key" \
SIM_SERVICEBUS_AMQP_LISTEN_ADDR=":$AZURE_SB_AMQP_PORT" \
SIM_AZURE_ARM_EXTERNAL_DATA_PLANE_URLS_JSON='{"storage":{"blob":"http://{account}.blob.localhost:'"$SHIM_AZUREBLOB_PORT"'/"},"keyVault":"https://{vault}.vault.localhost:'"$SHIM_AZUREKV_PORT"'/"}' \
    "$AZURE_BIN" >/tmp/sockerless-azure.log 2>&1 &
AZURE_PID=$!

# Give the sims a beat to bind their listeners. They're fast — 1s
# is plenty on local; CI can override via SOCKERLESS_STARTUP_DELAY.
sleep "${SOCKERLESS_STARTUP_DELAY:-1}"

echo "run: shim sockerless lane (storage + secrets + queue + pubsub + rdbms + cache + functions + apigateway)"
SOCKERLESS_AWS_ENDPOINT="https://localhost:$AWS_PORT" \
SOCKERLESS_GCP_ENDPOINT="localhost:$GCP_PORT" \
SOCKERLESS_AWS_SM_ENDPOINT="https://localhost:$AWS_PORT" \
SOCKERLESS_AZURE_KV_URL="https://testvault.vault.azure.net" \
SOCKERLESS_AZURE_TLS_PORT="$AZURE_PORT" \
SOCKERLESS_AZURE_SB_AMQP_PORT="$AZURE_SB_AMQP_PORT" \
SOCKERLESS_AZURE_BLOB_ACCOUNT="testacct" \
SOCKERLESS_AZURE_TLS_CERT="$CERT_DIR/sim.crt" \
SOCKERLESS_AZURE_TLS_KEY="$CERT_DIR/sim.key" \
SHIM_AZUREBLOB_PORT="$SHIM_AZUREBLOB_PORT" \
SHIM_AZUREKV_PORT="$SHIM_AZUREKV_PORT" \
SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE="$SOCKERLESS_AZURE_CONTAINERAPPS_IMAGE" \
SOCKERLESS_GCP_CLOUDRUN_IMAGE="$SOCKERLESS_GCP_CLOUDRUN_IMAGE" \
AWS_S3_CONFORMANCE_INSECURE_TLS=1 \
go test -run '^TestSockerless_' -count=1 -v \
    ./services/storage/conformance/... \
    ./services/secrets/conformance/... \
    ./services/queue/conformance/... \
    ./services/pubsub/conformance/... \
    ./services/rdbms/conformance/... \
    ./services/cache/conformance/... \
    ./services/functions/conformance/... \
    ./services/apigateway/conformance/... \
    ./services/dns/conformance/...

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
require_container_runtime() {
    if command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
        return 0
    fi
    if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
        return 0
    fi
    echo "ERR: sockerless sims require a running container runtime (podman or docker)." >&2
    echo "     On macOS: 'podman machine start' or open Docker Desktop." >&2
    echo "     On Linux: ensure the docker daemon is running." >&2
    exit 3
}
require_container_runtime

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
    if [[ -s "$CERT_DIR/sim.crt" && -s "$CERT_DIR/sim.key" ]]; then return; fi
    echo "cert: generating self-signed RSA-2048 cert in $CERT_DIR"
    openssl req -x509 -newkey rsa:2048 -nodes -days 1 \
        -subj "/CN=localhost" \
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

echo "start: Azure sim → https://localhost:$AZURE_PORT (TLS, self-signed; reuses the AWS sim's cert)"
SIM_LISTEN_ADDR=":$AZURE_PORT" \
SIM_TLS_CERT="$CERT_DIR/sim.crt" \
SIM_TLS_KEY="$CERT_DIR/sim.key" \
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
SOCKERLESS_AZURE_BLOB_ACCOUNT="testacct" \
AWS_S3_CONFORMANCE_INSECURE_TLS=1 \
go test -run '^TestSockerless_' -count=1 -v \
    ./services/storage/conformance/... \
    ./services/secrets/conformance/... \
    ./services/queue/conformance/... \
    ./services/pubsub/conformance/... \
    ./services/rdbms/conformance/... \
    ./services/cache/conformance/... \
    ./services/functions/conformance/... \
    ./services/apigateway/conformance/...

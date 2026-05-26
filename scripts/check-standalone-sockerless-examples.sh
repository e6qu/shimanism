#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=${WORK:-$(mktemp -d)}
SOCKERLESS_DIR=${SOCKERLESS_DIR:-"$WORK/sockerless"}

AWS_SIM_PORT=${AWS_SIM_PORT:-14566}
GCP_SIM_PORT=${GCP_SIM_PORT:-14567}
AZURE_SIM_PORT=${AZURE_SIM_PORT:-14569}
AWS_GCP_PORT=${AWS_GCP_PORT:-19001}
GCP_AZURE_PORT=${GCP_AZURE_PORT:-19002}
AZURE_AWS_PORT=${AZURE_AWS_PORT:-19003}

PIDS=()

cleanup() {
	for pid in "${PIDS[@]:-}"; do
		kill "$pid" 2>/dev/null || true
	done
	for pid in "${PIDS[@]:-}"; do
		wait "$pid" 2>/dev/null || true
	done
	if [[ ${WORK_OWNED:-0} == 1 ]]; then
		rm -rf "$WORK"
	fi
}

dump_logs() {
	for log in "$WORK"/*.log; do
		[[ -f "$log" ]] || continue
		echo "===== $log =====" >&2
		tail -200 "$log" >&2 || true
	done
}

finish() {
	status=$?
	if [[ $status -ne 0 ]]; then
		dump_logs
	fi
	cleanup
	exit "$status"
}
trap finish EXIT INT TERM

if [[ ! -d "$WORK" ]]; then
	mkdir -p "$WORK"
	WORK_OWNED=1
elif [[ ${WORK:-} == /tmp/* || ${WORK:-} == /private/tmp/* ]]; then
	WORK_OWNED=0
fi

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required command not found: $1" >&2
		exit 1
	fi
}

run_with_timeout() {
	if command -v timeout >/dev/null 2>&1; then
		timeout 90 "$@"
	else
		"$@"
	fi
}

start_process() {
	"$@" &
	PIDS+=("$!")
}

wait_tcp() {
	local port=$1
	for _ in $(seq 1 60); do
		if (echo >"/dev/tcp/127.0.0.1/$port") >/dev/null 2>&1; then
			return 0
		fi
		sleep 0.5
	done
	echo "timed out waiting for localhost:$port" >&2
	return 1
}

for cmd in aws az gcloud git go; do
	require_cmd "$cmd"
done

mkdir -p "$WORK/bin"

if [[ ! -d "$SOCKERLESS_DIR/.git" ]]; then
	git clone --depth=1 https://github.com/e6qu/sockerless.git "$SOCKERLESS_DIR"
fi

(cd "$ROOT" && go build -o "$WORK/bin/shim" ./cmd/shim)
(cd "$SOCKERLESS_DIR/simulators/aws" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o "$WORK/bin/simulator-aws" .)
(cd "$SOCKERLESS_DIR/simulators/gcp" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o "$WORK/bin/simulator-gcp" .)
(cd "$SOCKERLESS_DIR/simulators/azure" && GOWORK=off CGO_ENABLED=0 go build -tags noui -o "$WORK/bin/simulator-azure" .)

start_process env SIM_LISTEN_ADDR=":$AWS_SIM_PORT" "$WORK/bin/simulator-aws"
start_process env SIM_LISTEN_ADDR=":$GCP_SIM_PORT" "$WORK/bin/simulator-gcp"
start_process env SIM_LISTEN_ADDR=":$AZURE_SIM_PORT" "$WORK/bin/simulator-azure"

wait_tcp "$AWS_SIM_PORT"
wait_tcp "$GCP_SIM_PORT"
wait_tcp "$AZURE_SIM_PORT"

suffix="$(date +%s)-$$"
src="$WORK/payload.txt"
printf 'hello from standalone shimanism and sockerless\n' >"$src"

export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1

aws_gcp_bucket="e2e-aws-gcp-$suffix"
start_process env STORAGE_EMULATOR_HOST="localhost:$GCP_SIM_PORT" \
	"$WORK/bin/shim" storage -frontend=aws_s3 -backend=gcs -gcs-project=shim-sockerless -addr=":$AWS_GCP_PORT"
sleep 1
run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 mb "s3://$aws_gcp_bucket"
run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 cp "$src" "s3://$aws_gcp_bucket/hello.txt"
run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 cp "s3://$aws_gcp_bucket/hello.txt" "$WORK/aws-gcp.txt"
cmp "$src" "$WORK/aws-gcp.txt"
# Wire-fidelity assertion: ListObjectsV2 must surface the key
# we just uploaded. Pre-fix (issue #32), the response used
# <Object> instead of <Contents>, so botocore parsed zero
# rows and `s3 ls` was silently empty.
ls_out=$(run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 ls "s3://$aws_gcp_bucket/")
echo "$ls_out" | grep -q "hello.txt" || { echo "FAIL: aws s3 ls missed hello.txt — got: $ls_out" >&2; exit 1; }
run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 rm "s3://$aws_gcp_bucket/hello.txt"
run_with_timeout aws --endpoint-url="http://localhost:$AWS_GCP_PORT" s3 rb "s3://$aws_gcp_bucket"

azure_account_key="a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s="
azure_conn="DefaultEndpointsProtocol=http;AccountName=testacct;AccountKey=$azure_account_key;BlobEndpoint=http://localhost:$AZURE_SIM_PORT/testacct/;"

gcp_azure_bucket="e2e-gcp-azure-$suffix"
start_process env AZURE_STORAGE_CONNECTION_STRING="$azure_conn" \
	"$WORK/bin/shim" storage -frontend=gcs -backend=azureblob -azure-connection-string="$azure_conn" -azure-region=eastus -addr=":$GCP_AZURE_PORT"
sleep 1
export CLOUDSDK_AUTH_DISABLE_CREDENTIALS=true
export CLOUDSDK_CORE_PROJECT=shim-sockerless
export CLOUDSDK_CORE_DISABLE_PROMPTS=1
export CLOUDSDK_API_ENDPOINT_OVERRIDES_STORAGE="http://localhost:$GCP_AZURE_PORT/"
run_with_timeout gcloud --quiet storage buckets create "gs://$gcp_azure_bucket" --location=US
run_with_timeout gcloud --quiet storage cp "$src" "gs://$gcp_azure_bucket/hello.txt"
run_with_timeout gcloud --quiet storage cp "gs://$gcp_azure_bucket/hello.txt" "$WORK/gcp-azure.txt"
cmp "$src" "$WORK/gcp-azure.txt"
# Wire-fidelity assertion: the GCS-shaped `location` field must
# be a GCS location (US / EU / ASIA / regional), never the
# backend's Azure region. Pre-fix (issue #33) the response
# leaked "EASTUS".
gcs_loc=$(run_with_timeout gcloud --quiet storage buckets describe "gs://$gcp_azure_bucket" --format='value(location)')
case "$gcs_loc" in
	US|EU|ASIA|ASIA1|EUR4|EUR5|EUR7|EUR8|NAM4|*-*) : ;;
	*) echo "FAIL: GCS frontend leaked non-GCS location: '$gcs_loc'" >&2; exit 1 ;;
esac
run_with_timeout gcloud --quiet storage rm "gs://$gcp_azure_bucket/hello.txt"
run_with_timeout gcloud --quiet storage rm --recursive "gs://$gcp_azure_bucket"

azure_aws_container="e2e-azure-aws-$suffix"
start_process env AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 AWS_REQUEST_CHECKSUM_CALCULATION=when_required \
	"$WORK/bin/shim" storage -frontend=azure_blob -backend=aws -aws-endpoint="http://localhost:$AWS_SIM_PORT" -addr=":$AZURE_AWS_PORT"
sleep 1
run_with_timeout az storage container create --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --name "$azure_aws_container" --only-show-errors
run_with_timeout az storage blob upload --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --container-name "$azure_aws_container" --name hello.txt --file "$src" --overwrite --only-show-errors
run_with_timeout az storage blob download --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --container-name "$azure_aws_container" --name hello.txt --file "$WORK/azure-aws.txt" --only-show-errors
cmp "$src" "$WORK/azure-aws.txt"
# Wire-fidelity assertion: blob list must return a quoted ETag,
# matching the upload/download response shape. Pre-fix (issue
# #34) the list path returned an unquoted hex value, breaking
# `If-Match` round-trips.
list_etag=$(run_with_timeout az storage blob list --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --container-name "$azure_aws_container" --query '[0].properties.etag' -o tsv)
case "$list_etag" in
	\"*\") : ;;
	*) echo "FAIL: blob-list ETag not quoted: '$list_etag'" >&2; exit 1 ;;
esac
run_with_timeout az storage blob delete --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --container-name "$azure_aws_container" --name hello.txt --only-show-errors
run_with_timeout az storage container delete --auth-mode key --account-name testacct --account-key "$azure_account_key" --blob-endpoint "http://localhost:$AZURE_AWS_PORT/testacct/" --name "$azure_aws_container" --only-show-errors

echo "standalone sockerless examples passed"

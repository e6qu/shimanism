#!/usr/bin/env bash
# Pull a Docker image with exponential backoff, mitigating transient
# registry errors (quay.io 502s, Docker Hub rate limits, etc.) that
# would otherwise red-fail an entire CI job. The retry surface is
# bounded — failure to pull after the max attempts still exits
# non-zero, so a sustained registry outage is reported honestly.
#
# Usage:
#   scripts/docker-pull-retry.sh <image-ref> [max-attempts] [base-delay-seconds]
#
# Example:
#   scripts/docker-pull-retry.sh quay.io/minio/minio@sha256:14cea493...
#
# Each failed attempt sleeps base-delay * 2^(attempt-1) seconds.
# Defaults: 5 attempts, 5-second base delay (so 5+10+20+40 = 75s of
# backoff total before the final failure).

set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
  echo "usage: $0 <image-ref> [max-attempts] [base-delay-seconds]" >&2
  exit 2
fi

image="$1"
max="${2:-5}"
delay="${3:-5}"

for attempt in $(seq 1 "$max"); do
  if docker pull "$image"; then
    exit 0
  fi
  if [ "$attempt" -lt "$max" ]; then
    echo "::warning::docker pull attempt $attempt/$max failed for $image; sleeping ${delay}s"
    sleep "$delay"
    delay=$((delay * 2))
  fi
done

echo "::error::docker pull failed after $max attempts for $image" >&2
exit 1

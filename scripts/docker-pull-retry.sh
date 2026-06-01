#!/usr/bin/env bash
# Pull a Docker image with multi-registry fallback + exponential
# backoff, mitigating sustained registry outages (quay.io 502/504s,
# Docker Hub rate limits, etc.) that would otherwise red-fail an
# entire CI job.
#
# Usage:
#   scripts/docker-pull-retry.sh <ref-1>[,<ref-2>[,...]] [max-attempts] [base-delay-seconds]
#
# Refs are tried in order; the FIRST ref that pulls successfully wins
# and the script `docker tag`s every other ref to that pulled image
# so callers can reference any ref interchangeably in subsequent
# `docker run` commands. This means a single image SHA can be
# mirrored across multiple registries and the script picks
# whichever is healthy right now.
#
# Each ref gets `max` attempts with exponential backoff
# (base-delay * 2^(attempt-1)). Defaults: 3 attempts per ref,
# 5-second base delay (so 5+10 = 15s of in-ref backoff before
# falling through to the next mirror). With three mirrors that's
# up to 3×3 = 9 pulls before exit.
#
# Examples:
#   scripts/docker-pull-retry.sh \
#     quay.io/minio/minio@sha256:14cea493...,docker.io/minio/minio:RELEASE.2024-XX
#
# If only one ref is given the script behaves as a single-registry
# pull-with-retry (no fallback).

set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
  echo "usage: $0 <ref-1>[,<ref-2>[,...]] [max-attempts] [base-delay-seconds]" >&2
  exit 2
fi

refs="$1"
max="${2:-3}"
base="${3:-5}"

IFS=',' read -r -a ref_array <<< "$refs"

tried=()
for ref in "${ref_array[@]}"; do
  delay="$base"
  pulled=""
  for attempt in $(seq 1 "$max"); do
    if docker pull "$ref"; then
      pulled="$ref"
      break
    fi
    if [ "$attempt" -lt "$max" ]; then
      echo "::warning::docker pull attempt $attempt/$max failed for $ref; sleeping ${delay}s"
      sleep "$delay"
      delay=$((delay * 2))
    fi
  done
  tried+=("$ref")
  if [ -n "$pulled" ]; then
    # Tag every other ref to the pulled image so callers can
    # reference any ref interchangeably.
    for other in "${ref_array[@]}"; do
      if [ "$other" != "$pulled" ]; then
        if ! docker tag "$pulled" "$other"; then
          echo "::warning::docker tag $pulled -> $other failed; callers must use $pulled"
        fi
      fi
    done
    exit 0
  fi
  echo "::warning::registry exhausted for $ref after $max attempts; falling back to next mirror"
done

echo "::error::docker pull failed across all mirrors: ${tried[*]}" >&2
exit 1

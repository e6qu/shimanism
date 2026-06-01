#!/usr/bin/env bash
# Pull a Docker image with multi-registry fallback + exponential
# backoff, mitigating sustained registry outages (quay.io 502/504s,
# Docker Hub rate limits, etc.) that would otherwise red-fail an
# entire CI job. The pulled image is also tagged under the
# caller-supplied $LOCAL_TAG so the workflow's `docker run` can
# reference it regardless of which mirror was healthy.
#
# Usage:
#   LOCAL_TAG=<local-tag> scripts/docker-pull-retry.sh \
#       <ref-1>[,<ref-2>[,...]] [max-attempts] [base-delay-seconds]
#
# Refs are tried in order; the FIRST ref that pulls successfully
# wins. The script then `docker tag`s the pulled image to:
#   - $LOCAL_TAG (when set) — the stable name callers use in
#     `docker run`. Must be a valid tag-form ref (`repo:tag`),
#     NOT a digest-form ref (`repo@sha256:...`) — digests can't be
#     tag targets and `docker tag` silently rejects them.
#   - every other ref in the comma-separated list IF it's tag-form,
#     so callers can still pin SHAs in the primary ref without
#     losing the fallback's effect.
#
# Each ref gets `max` attempts with exponential backoff
# (base-delay * 2^(attempt-1)). Defaults: 3 attempts per ref,
# 5-second base delay (so 5+10 = 15s of in-ref backoff before
# falling through to the next mirror).
#
# Example:
#   LOCAL_TAG=shim-local/minio:run scripts/docker-pull-retry.sh \
#     quay.io/minio/minio@sha256:14cea493...,docker.io/minio/minio:RELEASE.2024-XX
#   docker run -d shim-local/minio:run server /data

set -euo pipefail

if [ $# -lt 1 ] || [ $# -gt 3 ]; then
  echo "usage: LOCAL_TAG=<tag> $0 <ref-1>[,<ref-2>[,...]] [max-attempts] [base-delay-seconds]" >&2
  exit 2
fi

refs="$1"
max="${2:-3}"
base="${3:-5}"
local_tag="${LOCAL_TAG:-}"

# is_digest_ref tests whether $1 contains "@sha256:" — a digest
# reference, which CANNOT be used as a `docker tag` target.
is_digest_ref() { [[ "$1" == *"@sha256:"* ]]; }

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
    # Tag to LOCAL_TAG so the workflow's docker run uses a stable
    # name regardless of which mirror won.
    if [ -n "$local_tag" ]; then
      if is_digest_ref "$local_tag"; then
        echo "::error::LOCAL_TAG '$local_tag' is digest-form; must be tag-form (repo:tag)" >&2
        exit 1
      fi
      if ! docker tag "$pulled" "$local_tag"; then
        echo "::error::docker tag $pulled -> $local_tag failed" >&2
        exit 1
      fi
    fi
    # Best-effort: tag every tag-form ref in the list to the pulled
    # image so callers that didn't supply $LOCAL_TAG can still use
    # any of the listed tag-form refs. Digest-form refs are skipped
    # (docker tag rejects them).
    for other in "${ref_array[@]}"; do
      if [ "$other" != "$pulled" ] && ! is_digest_ref "$other"; then
        if ! docker tag "$pulled" "$other"; then
          echo "::warning::docker tag $pulled -> $other failed"
        fi
      fi
    done
    exit 0
  fi
  echo "::warning::registry exhausted for $ref after $max attempts; falling back to next mirror"
done

echo "::error::docker pull failed across all mirrors: ${tried[*]}" >&2
exit 1

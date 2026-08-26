#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <image> <digest-repository> <jq-filter> <amd64-digest-file> <arm64-digest-file>" >&2
  exit 2
fi

image=$1
digest_repository=$2
filter=$3
amd64_digest_file=$4
arm64_digest_file=$5

if [[ ! -f "$filter" ]]; then
  echo "release manifest filter not found: $filter" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

attempts=${RELEASE_IMAGE_CHECK_ATTEMPTS:-5}
retry_delay=${RELEASE_IMAGE_CHECK_RETRY_DELAY:-3}
if [[ ! $attempts =~ ^[1-9][0-9]*$ || ! $retry_delay =~ ^[0-9]+$ ]]; then
  echo "release image check retry settings must be non-negative integers and attempts must be positive" >&2
  exit 2
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
manifest_file="$tmp_dir/manifest.json"
error_file="$tmp_dir/inspect.err"

inspect_existing_tag() {
  local attempt
  for ((attempt = 1; attempt <= attempts; attempt++)); do
    if docker buildx imagetools inspect --raw "$image" >"$manifest_file" 2>"$error_file"; then
      if jq -e -f "$filter" "$manifest_file" >/dev/null; then
        return 0
      fi
      echo "[publish] immutable tag exists but is missing linux/amd64 or linux/arm64: $image" >&2
      return 1
    fi

    if grep -Eiq '(^|[^[:alpha:]])(404|not found)([^[:alpha:]]|$)' "$error_file"; then
      return 3
    fi
    if ((attempt < attempts)); then
      echo "[publish] inspect attempt $attempt failed; retrying $image" >&2
      sleep "$retry_delay"
    fi
  done

  cat "$error_file" >&2
  echo "[publish] unable to determine immutable tag state: $image" >&2
  return 1
}

if inspect_existing_tag; then
  echo "[publish] $image already contains linux/amd64 and linux/arm64; skipping manifest merge"
  exit 0
else
  inspect_status=$?
  if [[ $inspect_status -ne 3 ]]; then
    exit "$inspect_status"
  fi
fi

read_digest() {
  local platform=$1
  local file=$2
  local digest

  if [[ ! -s "$file" ]]; then
    echo "[publish] missing $platform digest file: $file" >&2
    return 1
  fi
  digest=$(tr -d '[:space:]' <"$file")
  if [[ ! $digest =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "[publish] invalid $platform digest in $file: $digest" >&2
    return 1
  fi
  printf '%s\n' "$digest"
}

amd64_digest=$(read_digest linux/amd64 "$amd64_digest_file")
arm64_digest=$(read_digest linux/arm64 "$arm64_digest_file")

docker buildx imagetools create \
  --tag "$image" \
  "$digest_repository@$amd64_digest" \
  "$digest_repository@$arm64_digest"

for ((attempt = 1; attempt <= attempts; attempt++)); do
  if docker buildx imagetools inspect --raw "$image" \
    | jq -e -f "$filter" >/dev/null; then
    echo "[publish] merged linux/amd64 and linux/arm64 manifest: $image"
    exit 0
  fi
  if ((attempt < attempts)); then
    echo "[publish] manifest verification attempt $attempt failed; retrying $image" >&2
    sleep "$retry_delay"
  fi
done

echo "[publish] merged manifest does not contain linux/amd64 and linux/arm64: $image" >&2
exit 1

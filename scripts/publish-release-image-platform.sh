#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 6 || $5 != -- ]]; then
  echo "usage: $0 <image> <jq-filter> <metadata-file> <digest-file> -- <build-command>..." >&2
  exit 2
fi

image=$1
filter=$2
metadata_file=$3
digest_file=$4
shift 5
build_command=("$@")

if [[ ! -f "$filter" ]]; then
  echo "release manifest filter not found: $filter" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi
if [[ -z "$metadata_file" || -z "$digest_file" ]]; then
  echo "release metadata and digest file paths are required" >&2
  exit 2
fi

attempts=${RELEASE_IMAGE_CHECK_ATTEMPTS:-5}
retry_delay=${RELEASE_IMAGE_CHECK_RETRY_DELAY:-3}
if [[ ! $attempts =~ ^[1-9][0-9]*$ || ! $retry_delay =~ ^[0-9]+$ ]]; then
  echo "release image check retry settings must be non-negative integers and attempts must be positive" >&2
  exit 2
fi

mkdir -p "$(dirname "$metadata_file")" "$(dirname "$digest_file")"
rm -f -- "$metadata_file" "$digest_file"

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
manifest_file="$tmp_dir/manifest.json"
error_file="$tmp_dir/inspect.err"

for ((attempt = 1; attempt <= attempts; attempt++)); do
  if docker buildx imagetools inspect --raw "$image" >"$manifest_file" 2>"$error_file"; then
    if jq -e -f "$filter" "$manifest_file" >/dev/null; then
      printf '%s\n' already-published >"$digest_file"
      echo "[publish] $image already contains linux/amd64 and linux/arm64; skipping platform build"
      exit 0
    fi
    echo "[publish] immutable tag exists but is missing linux/amd64 or linux/arm64: $image" >&2
    exit 1
  fi

  if grep -Eiq '(^|[^[:alpha:]])(404|not found)([^[:alpha:]]|$)' "$error_file"; then
    echo "[publish] $image does not exist; building platform digest"
    "${build_command[@]}"

    digest=$(jq -er '."containerimage.digest" | strings | select(test("^sha256:[0-9a-f]{64}$"))' "$metadata_file") \
      || { echo "[publish] build metadata does not contain a valid image digest: $metadata_file" >&2; exit 1; }
    printf '%s\n' "$digest" >"$digest_file"
    echo "[publish] pushed platform digest for $image: $digest"
    exit 0
  fi

  if ((attempt < attempts)); then
    echo "[publish] inspect attempt $attempt failed; retrying $image" >&2
    sleep "$retry_delay"
  fi
done

cat "$error_file" >&2
echo "[publish] unable to determine immutable tag state: $image" >&2
exit 1

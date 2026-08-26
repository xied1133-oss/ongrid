#!/usr/bin/env bash
# Verify immutable CNB Release attachments against their checksum sidecars.

set -euo pipefail

BASE_URL=${1:?usage: verify-cnb-release-attachments.sh <base-url> <tag> [--output-dir dir] <filename...>}
TAG=${2:?release tag}
shift 2
OUTPUT_DIR=""
if [[ "${1:-}" == "--output-dir" ]]; then
    OUTPUT_DIR=${2:?--output-dir requires a directory}
    shift 2
fi
(( $# > 0 )) || { echo "verify-cnb-attachments: no filenames supplied" >&2; exit 2; }

BASE_URL=${BASE_URL%/}
[[ "$TAG" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || {
    echo "verify-cnb-attachments: invalid release tag: $TAG" >&2
    exit 2
}

command -v curl >/dev/null 2>&1 || { echo "verify-cnb-attachments: curl is required" >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { echo "verify-cnb-attachments: sha256sum is required" >&2; exit 1; }

if [[ -n "$OUTPUT_DIR" ]]; then
    work=$OUTPUT_DIR
    mkdir -p "$work"
    [[ -d "$work" && ! -L "$work" ]] || {
        echo "verify-cnb-attachments: output directory is not a regular directory: $work" >&2
        exit 2
    }
    if find "$work" -mindepth 1 -print -quit | grep -q .; then
        echo "verify-cnb-attachments: output directory must be empty: $work" >&2
        exit 2
    fi
else
    work=$(mktemp -d "${TMPDIR:-/tmp}/verify-cnb-attachments.XXXXXX")
    trap 'rm -rf "$work"' EXIT
fi

CURL_FLAGS=(
    --fail --location --silent --show-error
    --retry 3 --retry-all-errors --retry-delay 3
    --connect-timeout 15 --speed-time 60 --speed-limit 1024
)

verify_pair() {
    local filename=$1 file="$work/$filename" sidecar="$work/$filename.sha256"
    local line expected_sha recorded_name extra actual_sha

    curl "${CURL_FLAGS[@]}" -o "$file" "$BASE_URL/$TAG/$filename"
    curl "${CURL_FLAGS[@]}" -o "$sidecar" "$BASE_URL/$TAG/$filename.sha256"
    [[ -s "$file" && -s "$sidecar" ]] || {
        echo "verify-cnb-attachments: empty attachment or sidecar for $TAG/$filename" >&2
        return 1
    }
    if ! line=$(awk 'NF {line=$0; count++} END {if (count != 1) exit 1; print line}' "$sidecar"); then
        echo "verify-cnb-attachments: malformed checksum sidecar for $TAG/$filename" >&2
        return 1
    fi
    IFS=' ' read -r expected_sha recorded_name extra <<<"$line"
    recorded_name=${recorded_name#\*}
    if [[ ! "$expected_sha" =~ ^[0-9a-f]{64}$ \
        || "$recorded_name" != "$filename" \
        || -n "${extra:-}" ]]; then
        echo "verify-cnb-attachments: checksum sidecar does not describe $filename" >&2
        return 1
    fi
    actual_sha=$(sha256sum "$file" | awk 'NR == 1 {print $1}')
    if [[ "$actual_sha" != "$expected_sha" ]]; then
        echo "verify-cnb-attachments: checksum mismatch for $TAG/$filename" >&2
        return 1
    fi
}

for filename in "$@"; do
    [[ "$filename" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || {
        echo "verify-cnb-attachments: invalid attachment filename: $filename" >&2
        exit 2
    }
    verify_pair "$filename"
done

echo "verified ${#} immutable attachment(s) on CNB release $TAG"

#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
verify_script="$repo_root/scripts/verify-cnb-release-attachments.sh"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

fail() {
    printf 'CNB release verification test failed: %s\n' "$*" >&2
    exit 1
}

release_root="$tmp_dir/releases"
mkdir -p "$release_root/valid" "$release_root/empty" \
    "$release_root/tampered" "$release_root/wrong-name"

printf 'verified payload\n' > "$release_root/valid/asset"
(cd "$release_root/valid" && sha256sum asset > asset.sha256)
bash "$verify_script" "file://$release_root" valid asset >/dev/null \
    || fail "a valid attachment and sidecar were rejected"
download_dir="$tmp_dir/downloaded"
bash "$verify_script" "file://$release_root" valid --output-dir "$download_dir" asset \
    >/dev/null || fail "a caller-owned output directory was rejected"
cmp -s "$release_root/valid/asset" "$download_dir/asset" \
    || fail "verified attachment was not retained in the caller-owned output directory"

: > "$release_root/empty/asset"
printf '%064d  asset\n' 0 > "$release_root/empty/asset.sha256"
if bash "$verify_script" "file://$release_root" empty asset >/dev/null 2>&1; then
    fail "zero-byte release attachments passed integrity verification"
fi

printf 'original payload\n' > "$release_root/tampered/asset"
(cd "$release_root/tampered" && sha256sum asset > asset.sha256)
printf 'modified payload\n' > "$release_root/tampered/asset"
if bash "$verify_script" "file://$release_root" tampered asset >/dev/null 2>&1; then
    fail "an attachment that differs from its sidecar passed verification"
fi

printf 'verified payload\n' > "$release_root/wrong-name/asset"
wrong_sha=$(sha256sum "$release_root/wrong-name/asset" | awk 'NR == 1 {print $1}')
printf '%s  another-asset\n' "$wrong_sha" > "$release_root/wrong-name/asset.sha256"
if bash "$verify_script" "file://$release_root" wrong-name asset >/dev/null 2>&1; then
    fail "a sidecar naming another attachment passed verification"
fi

printf 'CNB release verification tests passed\n'

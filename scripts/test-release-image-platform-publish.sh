#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
publisher="$repo_root/scripts/publish-release-image-platform.sh"
merger="$repo_root/scripts/merge-release-image-manifest.sh"
filter="$repo_root/scripts/release-manifest-platforms.jq"
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/bin"
export PATH="$tmp_dir/bin:$PATH"
export DOCKER_FAKE_COUNT="$tmp_dir/docker-count"
export DOCKER_FAKE_CREATED="$tmp_dir/docker-created"
export BUILD_MARKER="$tmp_dir/build-marker"
export BUILD_METADATA="$tmp_dir/build-metadata.json"
export DIGEST_FILE="$tmp_dir/platform.digest"
export RELEASE_IMAGE_CHECK_ATTEMPTS=2
export RELEASE_IMAGE_CHECK_RETRY_DELAY=0

amd64_digest="sha256:$(printf 'a%.0s' {1..64})"
arm64_digest="sha256:$(printf 'b%.0s' {1..64})"
export TEST_PLATFORM_DIGEST="$amd64_digest"

cat >"$tmp_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ $1 == buildx && $2 == imagetools && $3 == inspect && $4 == --raw && $# -eq 5 ]]; then
  count=0
  if [[ -f "$DOCKER_FAKE_COUNT" ]]; then
    count=$(<"$DOCKER_FAKE_COUNT")
  fi
  count=$((count + 1))
  printf '%s\n' "$count" >"$DOCKER_FAKE_COUNT"

  if [[ -f "$DOCKER_FAKE_CREATED" ]]; then
    printf '%s\n' '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}'
    exit 0
  fi

  case "$DOCKER_FAKE_MODE" in
    present)
      printf '%s\n' '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}'
      ;;
    partial)
      printf '%s\n' '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}}]}'
      ;;
    missing)
      echo 'ERROR: example.invalid/release:v1.0.0: not found' >&2
      exit 1
      ;;
    transient-then-present)
      if [[ $count -eq 1 ]]; then
        echo 'ERROR: registry request failed: EOF' >&2
        exit 1
      fi
      printf '%s\n' '{"manifests":[{"platform":{"os":"linux","architecture":"amd64"}},{"platform":{"os":"linux","architecture":"arm64"}}]}'
      ;;
    transient)
      echo 'ERROR: registry request failed: EOF' >&2
      exit 1
      ;;
    *)
      echo "unknown DOCKER_FAKE_MODE: $DOCKER_FAKE_MODE" >&2
      exit 2
      ;;
  esac
  exit 0
fi

if [[ $1 == buildx && $2 == imagetools && $3 == create && $4 == --tag && $# -eq 7 ]]; then
  printf '%s\n' "$*" >"$DOCKER_FAKE_CREATED"
  exit 0
fi

echo "unexpected fake docker invocation: $*" >&2
exit 2
EOF
chmod +x "$tmp_dir/bin/docker"

run_platform_publisher() {
  bash "$publisher" \
    example.invalid/release:v1.0.0 \
    "$filter" \
    "$BUILD_METADATA" \
    "$DIGEST_FILE" \
    -- bash -c 'printf built >"$BUILD_MARKER"; printf "{\"containerimage.digest\":\"%s\"}\n" "$TEST_PLATFORM_DIGEST" >"$BUILD_METADATA"'
}

reset_case() {
  rm -f "$DOCKER_FAKE_COUNT" "$DOCKER_FAKE_CREATED" "$BUILD_MARKER" "$BUILD_METADATA" "$DIGEST_FILE"
}

reset_case
export DOCKER_FAKE_MODE=present
run_platform_publisher
test ! -e "$BUILD_MARKER"
test "$(<"$DIGEST_FILE")" = already-published

reset_case
export DOCKER_FAKE_MODE=missing
run_platform_publisher
test -s "$BUILD_MARKER"
test "$(<"$DIGEST_FILE")" = "$amd64_digest"

reset_case
export DOCKER_FAKE_MODE=partial
if run_platform_publisher; then
  echo "partial immutable manifest unexpectedly passed platform publisher" >&2
  exit 1
fi
test ! -e "$BUILD_MARKER"

reset_case
export DOCKER_FAKE_MODE=transient-then-present
run_platform_publisher
test "$(<"$DOCKER_FAKE_COUNT")" -eq 2
test ! -e "$BUILD_MARKER"

reset_case
export DOCKER_FAKE_MODE=transient
if run_platform_publisher; then
  echo "persistent registry error unexpectedly triggered a platform build" >&2
  exit 1
fi
test ! -e "$BUILD_MARKER"

printf '%s\n' "$amd64_digest" >"$tmp_dir/amd64.digest"
printf '%s\n' "$arm64_digest" >"$tmp_dir/arm64.digest"

reset_case
export DOCKER_FAKE_MODE=present
bash "$merger" example.invalid/release:v1.0.0 example.invalid/release "$filter" "$tmp_dir/amd64.digest" "$tmp_dir/arm64.digest"
test ! -e "$DOCKER_FAKE_CREATED"

reset_case
export DOCKER_FAKE_MODE=missing
bash "$merger" example.invalid/release:v1.0.0 example.invalid/release "$filter" "$tmp_dir/amd64.digest" "$tmp_dir/arm64.digest"
test -s "$DOCKER_FAKE_CREATED"
grep -Fq "example.invalid/release@$amd64_digest" "$DOCKER_FAKE_CREATED"
grep -Fq "example.invalid/release@$arm64_digest" "$DOCKER_FAKE_CREATED"

reset_case
export DOCKER_FAKE_MODE=partial
if bash "$merger" example.invalid/release:v1.0.0 example.invalid/release "$filter" "$tmp_dir/amd64.digest" "$tmp_dir/arm64.digest"; then
  echo "partial immutable manifest unexpectedly passed manifest merger" >&2
  exit 1
fi
test ! -e "$DOCKER_FAKE_CREATED"

printf '%s\n' invalid >"$tmp_dir/amd64.digest"
reset_case
export DOCKER_FAKE_MODE=missing
if bash "$merger" example.invalid/release:v1.0.0 example.invalid/release "$filter" "$tmp_dir/amd64.digest" "$tmp_dir/arm64.digest"; then
  echo "invalid platform digest unexpectedly passed manifest merger" >&2
  exit 1
fi
test ! -e "$DOCKER_FAKE_CREATED"

echo "release platform image publish tests passed"

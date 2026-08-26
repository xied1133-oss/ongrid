#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
publisher="$repo_root/scripts/publish-cnb-release-attachments.sh"
plugin_image=cnbcool/attachments@sha256:37c2d53fed9accee6ea0a509a05a4d05e4b36af37d5319451c2284e287b9e935
tmp_dir=$(mktemp -d "$repo_root/.tmp-test-cnb-attachments.XXXXXX")
trap 'rm -rf "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/bin" "$tmp_dir/files" "$tmp_dir/remote"
printf 'one\n' > "$tmp_dir/files/one"
(cd "$tmp_dir/files" && sha256sum one > one.sha256)

cat > "$tmp_dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
out=""
write_status=0
url=${!#}
name=${url##*/}
printf '%s\n' "$url" >> "$FAKE_CURL_LOG"
if [[ -n ${FAKE_PROBE_STATUS:-} ]]; then
    [[ " $* " != *" -w "* ]] || printf '%s' "$FAKE_PROBE_STATUS"
    exit 0
fi
while (( $# > 0 )); do
    case "$1" in
        -o) out=$2; shift 2 ;;
        -w) write_status=1; shift 2 ;;
        *) shift ;;
    esac
done
remote_file="$FAKE_REMOTE_ROOT/$name"
if (( write_status == 1 )); then
    [[ -f "$remote_file" ]] && printf '200' || printf '404'
    exit 0
fi
[[ -f "$remote_file" ]] || exit 22
if [[ -n "$out" ]]; then
    cp "$remote_file" "$out"
else
    command cat "$remote_file"
fi
EOF
cat > "$tmp_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$FAKE_DOCKER_LOG"
printf '%s\n' '##[set-output FILES=one%2Cone.sha256]'
[[ ${FAKE_DOCKER_FAIL:-} != 1 ]] || exit 42
: > "$FAKE_UPLOAD_STATE"
cp "$FAKE_LOCAL_ROOT/one" "$FAKE_LOCAL_ROOT/one.sha256" "$FAKE_REMOTE_ROOT/"
EOF
chmod 0755 "$tmp_dir/bin/curl" "$tmp_dir/bin/docker"

run_publisher() {
    FAKE_CURL_LOG="$tmp_dir/curl.log" \
    FAKE_DOCKER_LOG="$tmp_dir/docker.log" \
    FAKE_UPLOAD_STATE="$tmp_dir/uploaded" \
    FAKE_LOCAL_ROOT="$tmp_dir/files" \
    FAKE_REMOTE_ROOT="$tmp_dir/remote" \
    FAKE_DOCKER_FAIL="${FAKE_DOCKER_FAIL:-}" \
    PATH="$tmp_dir/bin:$PATH" \
    CNB_TOKEN=test-token \
        bash "$publisher" vtest ongridio/ongrid-edge \
        https://cnb.test/ongridio/ongrid-edge/-/releases/download \
        "$plugin_image" "$tmp_dir/files/one" "$tmp_dir/files/one.sha256"
}

if CNB_TOKEN=test-token PATH="$tmp_dir/bin:$PATH" \
    bash "$publisher" vtest ongridio/ongrid-edge \
    https://cnb.test/ongridio/ongrid-edge/-/releases/download \
    cnbcool/attachments:latest "$tmp_dir/files/one" >/dev/null 2>&1; then
    echo "mutable attachment uploader image was accepted" >&2
    exit 1
fi

# A complete immutable release is reused without invoking the uploader.
: > "$tmp_dir/curl.log"
: > "$tmp_dir/docker.log"
rm -f "$tmp_dir/remote/one" "$tmp_dir/remote/one.sha256"
cp "$tmp_dir/files/one" "$tmp_dir/files/one.sha256" "$tmp_dir/remote/"
run_publisher
[[ ! -s "$tmp_dir/docker.log" ]] || { echo "complete release was uploaded again" >&2; exit 1; }
env -u CNB_TOKEN \
    FAKE_CURL_LOG="$tmp_dir/curl.log" \
    FAKE_REMOTE_ROOT="$tmp_dir/remote" \
    PATH="$tmp_dir/bin:$PATH" \
    bash "$publisher" vtest ongridio/ongrid-edge \
        https://cnb.test/ongridio/ongrid-edge/-/releases/download \
        "$plugin_image" "$tmp_dir/files/one" "$tmp_dir/files/one.sha256" >/dev/null \
    || { echo "complete release unnecessarily required upload credentials" >&2; exit 1; }

# Matching sidecar text is not enough: a corrupt remote payload must fail
# actual content verification and must never be reported as an immutable hit.
printf 'tampered remote payload\n' > "$tmp_dir/remote/one"
if run_publisher >/dev/null 2>&1; then
    echo "corrupt remote payload was accepted because its sidecar existed" >&2
    exit 1
fi
[[ ! -s "$tmp_dir/docker.log" ]] || { echo "corrupt complete release invoked uploader" >&2; exit 1; }

# A partial immutable release must fail closed instead of overwriting it.
: > "$tmp_dir/curl.log"
: > "$tmp_dir/docker.log"
rm -f "$tmp_dir/remote/one" "$tmp_dir/remote/one.sha256"
cp "$tmp_dir/files/one" "$tmp_dir/remote/"
if run_publisher >/dev/null 2>&1; then
    echo "partial release was accepted" >&2
    exit 1
fi
[[ ! -s "$tmp_dir/docker.log" ]] || { echo "partial release invoked uploader" >&2; exit 1; }

# A transient/remote HTTP error must fail closed instead of being treated as
# an empty Release that is safe to populate.
: > "$tmp_dir/curl.log"
: > "$tmp_dir/docker.log"
rm -f "$tmp_dir/remote/one" "$tmp_dir/remote/one.sha256"
if FAKE_PROBE_STATUS=503 run_publisher >/dev/null 2>&1; then
    echo "CNB probe error was treated as a missing attachment" >&2
    exit 1
fi
[[ ! -s "$tmp_dir/docker.log" ]] || { echo "probe error invoked uploader" >&2; exit 1; }

# An empty release uploads once and verifies both resulting direct URLs.
: > "$tmp_dir/curl.log"
: > "$tmp_dir/docker.log"
rm -f "$tmp_dir/uploaded"
rm -f "$tmp_dir/remote/one" "$tmp_dir/remote/one.sha256"
if FAKE_DOCKER_FAIL=1 run_publisher >/dev/null 2>&1; then
    echo "uploader failure was hidden by output sanitization" >&2
    exit 1
fi
[[ ! -e "$tmp_dir/remote/one" ]] \
    || { echo "failed uploader unexpectedly populated the release" >&2; exit 1; }
publisher_output=$(run_publisher)
grep -Fq 'PLUGIN_TAG=vtest' "$tmp_dir/docker.log"
grep -Fq "$plugin_image" "$tmp_dir/docker.log"
grep -Fq '[cnb-attachments] [runner-command set-output FILES=one%2Cone.sha256]' \
    <<<"$publisher_output" \
    || { echo "uploader output was not escaped from GitHub runner command parsing" >&2; exit 1; }
if grep -Fq '##[' <<<"$publisher_output"; then
    echo "uploader output can still be parsed as a GitHub runner command" >&2
    exit 1
fi
cmp -s "$tmp_dir/files/one" "$tmp_dir/remote/one" \
    || { echo "uploaded payload was not verified from the remote release" >&2; exit 1; }

echo "CNB attachment publisher tests passed"

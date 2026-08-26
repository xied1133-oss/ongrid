#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
ensurer="$repo_root/scripts/ensure-cnb-release.sh"
tmp_dir=$(mktemp -d "$repo_root/.tmp-test-ensure-cnb-release.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT

mkdir -p "$tmp_dir/bin"
cat >"$tmp_dir/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

method=GET
output=
request=
url=
while (( $# > 0 )); do
    case "$1" in
        -X) method=$2; shift 2 ;;
        -o) output=$2; shift 2 ;;
        -w|-H) shift 2 ;;
        --data-binary) request=${2#@}; shift 2 ;;
        -sS) shift ;;
        *) url=$1; shift ;;
    esac
done

printf '%s %s\n' "$method" "$url" >>"$FAKE_CURL_LOG"
case "${FAKE_CURL_SCENARIO:?}:$method" in
    existing:GET)
        printf '{"tag_name":"vtest","prerelease":false}\n' >"$output"
        printf '200'
        ;;
    missing:GET)
        printf '{"errcode":404}\n' >"$output"
        printf '404'
        ;;
    missing:POST)
        cp "$request" "$FAKE_REQUEST_LOG"
        jq -c '{tag_name, prerelease}' "$request" >"$output"
        printf '201'
        ;;
    forbidden:GET)
        printf '{"errcode":403}\n' >"$output"
        printf '403'
        ;;
    *)
        echo "unexpected fake curl call: $FAKE_CURL_SCENARIO $method $url" >&2
        exit 1
        ;;
esac
EOF
chmod 0755 "$tmp_dir/bin/curl"

run_ensurer() {
    local scenario=$1 prerelease=${2:-false}
    FAKE_CURL_SCENARIO=$scenario \
    FAKE_CURL_LOG="$tmp_dir/curl.log" \
    FAKE_REQUEST_LOG="$tmp_dir/request.json" \
    PATH="$tmp_dir/bin:$PATH" \
    CNB_TOKEN=test-token \
    CNB_API_ENDPOINT=https://cnb.test \
        bash "$ensurer" vtest ongridio/ongrid-edge 'Edge assets vtest' 'test release' "$prerelease"
}

: >"$tmp_dir/curl.log"
output=$(run_ensurer existing)
[[ "$output" == *'already exists; reuse'* ]]
[[ $(wc -l <"$tmp_dir/curl.log" | tr -d ' ') == 1 ]]

: >"$tmp_dir/curl.log"
if output=$(run_ensurer existing true 2>&1); then
    echo "existing release with mismatched prerelease status was reused" >&2
    exit 1
fi

: >"$tmp_dir/curl.log"
output=$(run_ensurer missing)
[[ "$output" == *'created CNB release vtest'* ]]
[[ $(wc -l <"$tmp_dir/curl.log" | tr -d ' ') == 2 ]]
jq -e '
    .tag_name == "vtest" and
    .target_commitish == "main" and
    .draft == false and
    .prerelease == false and
    .make_latest == "false"
' "$tmp_dir/request.json" >/dev/null

: >"$tmp_dir/curl.log"
output=$(run_ensurer missing true)
[[ "$output" == *'created CNB release vtest'* ]]
jq -e '.prerelease == true' "$tmp_dir/request.json" >/dev/null

: >"$tmp_dir/curl.log"
if output=$(run_ensurer forbidden 2>&1); then
    echo "forbidden CNB API response was accepted" >&2
    exit 1
fi
[[ "$output" != *test-token* ]] || { echo "CNB token leaked into output" >&2; exit 1; }

if CNB_TOKEN=test-token bash "$ensurer" '../invalid' ongridio/ongrid-edge title description >/dev/null 2>&1; then
    echo "invalid release tag was accepted" >&2
    exit 1
fi
if CNB_TOKEN=test-token bash "$ensurer" vtest ongridio/ongrid-edge title description invalid >/dev/null 2>&1; then
    echo "invalid prerelease value was accepted" >&2
    exit 1
fi

echo "CNB release ensure tests passed"

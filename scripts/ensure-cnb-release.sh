#!/usr/bin/env bash
# Idempotently create the CNB Release required by attachment uploads.

set -euo pipefail

TAG=${1:?usage: ensure-cnb-release.sh <tag> <repo-slug> <title> <description> [prerelease]}
REPO_SLUG=${2:?repository slug}
TITLE=${3:?release title}
DESCRIPTION=${4:?release description}
PRERELEASE=${5:-false}

: "${CNB_TOKEN:?CNB_TOKEN with repo-release read/write permission is required}"

API_ENDPOINT=${CNB_API_ENDPOINT:-https://api.cnb.cool}
TARGET_COMMITISH=${CNB_RELEASE_TARGET_COMMITISH:-main}

[[ "$TAG" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || {
    echo "ensure-cnb-release: invalid tag: $TAG" >&2
    exit 2
}
[[ "$REPO_SLUG" =~ ^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)+$ ]] || {
    echo "ensure-cnb-release: invalid repository slug: $REPO_SLUG" >&2
    exit 2
}
[[ "$PRERELEASE" == true || "$PRERELEASE" == false ]] || {
    echo "ensure-cnb-release: prerelease must be true or false" >&2
    exit 2
}

command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "jq is required" >&2; exit 1; }

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/ensure-cnb-release.XXXXXX")
trap 'rm -rf -- "$tmp_dir"' EXIT

releases_url="$API_ENDPOINT/$REPO_SLUG/-/releases"
response_file="$tmp_dir/response.json"

get_release() {
    curl -sS -o "$response_file" -w '%{http_code}' \
        -H 'Accept: application/vnd.cnb.api+json' \
        -H "Authorization: Bearer $CNB_TOKEN" \
        "$releases_url/tags/$TAG"
}

verify_release_tag() {
    jq -e --arg tag "$TAG" --argjson prerelease "$PRERELEASE" \
        '.tag_name == $tag and .prerelease == $prerelease' "$response_file" >/dev/null || {
        echo "ensure-cnb-release: CNB returned mismatched metadata for $TAG" >&2
        exit 1
    }
}

status=$(get_release)
case "$status" in
    200)
        verify_release_tag
        echo "CNB release $TAG already exists; reuse"
        exit 0
        ;;
    404) ;;
    *)
        echo "ensure-cnb-release: cannot inspect $REPO_SLUG release $TAG (HTTP $status)" >&2
        exit 1
        ;;
esac

jq -nc \
    --arg tag "$TAG" \
    --arg target "$TARGET_COMMITISH" \
    --arg title "$TITLE" \
    --arg description "$DESCRIPTION" \
    --argjson prerelease "$PRERELEASE" \
    '{
        tag_name: $tag,
        target_commitish: $target,
        name: $title,
        body: $description,
        draft: false,
        prerelease: $prerelease,
        make_latest: "false"
    }' >"$tmp_dir/request.json"

create_status=$(curl -sS -o "$response_file" -w '%{http_code}' \
    -X POST \
    -H 'Accept: application/vnd.cnb.api+json' \
    -H 'Content-Type: application/json' \
    -H "Authorization: Bearer $CNB_TOKEN" \
    --data-binary "@$tmp_dir/request.json" \
    "$releases_url")

if [[ "$create_status" == "201" ]]; then
    verify_release_tag
    echo "created CNB release $TAG"
    exit 0
fi

# A concurrent rerun may have created the Release after the first lookup.
status=$(get_release)
if [[ "$status" == "200" ]]; then
    verify_release_tag
    echo "CNB release $TAG was created concurrently; reuse"
    exit 0
fi

echo "ensure-cnb-release: cannot create $REPO_SLUG release $TAG (HTTP $create_status)" >&2
exit 1

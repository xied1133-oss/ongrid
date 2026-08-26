#!/usr/bin/env bash
# Idempotently publish immutable files to an existing CNB Release.

set -euo pipefail

TAG=${1:?usage: publish-cnb-release-attachments.sh <tag> <repo-slug> <base-url> <plugin-image> <file...>}
REPO_SLUG=${2:?repository slug}
BASE_URL=${3:?release download base URL}
PLUGIN_IMAGE=${4:?attachments plugin image}
shift 4
(( $# > 0 )) || { echo "publish-cnb-attachments: no files supplied" >&2; exit 2; }
[[ "$PLUGIN_IMAGE" =~ @sha256:[0-9a-f]{64}$ ]] || {
    echo "publish-cnb-attachments: plugin image must be pinned by sha256 digest" >&2
    exit 2
}

API_ENDPOINT=${CNB_API_ENDPOINT:-https://api.cnb.cool}
WEB_ENDPOINT=${CNB_WEB_ENDPOINT:-https://cnb.cool}
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
VERIFY_SCRIPT="$SCRIPT_DIR/verify-cnb-release-attachments.sh"
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
command -v cmp >/dev/null 2>&1 || { echo "cmp is required" >&2; exit 1; }
command -v sed >/dev/null 2>&1 || { echo "sed is required" >&2; exit 1; }
[[ -x "$VERIFY_SCRIPT" ]] || { echo "publish-cnb-attachments: missing $VERIFY_SCRIPT" >&2; exit 1; }

files=()
payload_files=()
payload_names=()
present=0
for file in "$@"; do
    if [[ "$file" != /* ]]; then
        file="$(pwd)/${file#./}"
    fi
    [[ -s "$file" ]] || { echo "publish-cnb-attachments: missing $file" >&2; exit 1; }
    files+=("$file")
    case "$file" in
        *.sha256) ;;
        *)
            [[ -s "$file.sha256" ]] || {
                echo "publish-cnb-attachments: missing checksum sidecar $file.sha256" >&2
                exit 1
            }
            payload_files+=("$file")
            payload_names+=("$(basename "$file")")
            ;;
    esac
    asset_url="$BASE_URL/$TAG/$(basename "$file")"
    if ! probe_status=$(curl -sSIL -o /dev/null -w '%{http_code}' "$asset_url"); then
        echo "publish-cnb-attachments: cannot inspect $asset_url" >&2
        exit 1
    fi
    case "$probe_status" in
        200) present=$((present + 1)) ;;
        404) ;;
        *)
            echo "publish-cnb-attachments: cannot inspect $asset_url (HTTP $probe_status)" >&2
            exit 1
            ;;
    esac
done
(( ${#payload_names[@]} > 0 )) || {
    echo "publish-cnb-attachments: no payload attachments supplied" >&2
    exit 2
}
for payload_file in "${payload_files[@]}"; do
    sidecar_supplied=0
    for file in "${files[@]}"; do
        if [[ "$file" == "$payload_file.sha256" ]]; then
            sidecar_supplied=1
            break
        fi
    done
    (( sidecar_supplied == 1 )) || {
        echo "publish-cnb-attachments: checksum sidecar was not supplied for $payload_file" >&2
        exit 1
    }
done

verify_remote_release() {
    local file
    for file in "${files[@]}"; do
        case "$file" in
            *.sha256)
                curl -fsSL "$BASE_URL/$TAG/$(basename "$file")" | cmp -s - "$file" || {
                    echo "publish-cnb-attachments: remote checksum differs for $(basename "$file"); refusing to overwrite immutable content (publish a new version or remove the incorrect Release manually)" >&2
                    return 1
                }
                ;;
        esac
    done
    bash "$VERIFY_SCRIPT" "$BASE_URL" "$TAG" "${payload_names[@]}" >/dev/null
}

if (( present == ${#files[@]} )); then
    verify_remote_release || exit 1
    echo "CNB release $TAG already has all ${#files[@]} immutable attachment(s); skip"
    exit 0
fi
if (( present != 0 )); then
    echo "publish-cnb-attachments: release $TAG is only partially populated; refusing to overwrite immutable attachments" >&2
    exit 1
fi

: "${CNB_TOKEN:?CNB_TOKEN with repo-contents read/write permission is required}"
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }

repo_root=$(pwd)
attachment_list=""
for file in "${files[@]}"; do
    case "$file" in
        "$repo_root"/*) rel=".${file#$repo_root}" ;;
        *) echo "publish-cnb-attachments: $file must be under $repo_root" >&2; exit 1 ;;
    esac
    attachment_list="${attachment_list:+$attachment_list,}$rel"
done

# Rewrite legacy command markers anywhere in plugin output before forwarding it
# to the log. GitHub Runner recognizes `##[...]` even after a line prefix.
# pipefail preserves docker errors through the sanitizing pipeline.
docker run --rm \
    -e CNB_TOKEN \
    -e CNB_API_ENDPOINT="$API_ENDPOINT" \
    -e CNB_WEB_ENDPOINT="$WEB_ENDPOINT" \
    -e CNB_REPO_SLUG="$REPO_SLUG" \
    -e CNB_IS_TAG=true \
    -e PLUGIN_TAG="$TAG" \
    -e PLUGIN_ATTACHMENTS="$attachment_list" \
    -v "$repo_root:$repo_root" \
    -w "$repo_root" \
    "$PLUGIN_IMAGE" 2>&1 \
    | sed -e 's/##\[/[runner-command /g' -e 's/^/[cnb-attachments] /'

verify_remote_release || {
    echo "publish-cnb-attachments: uploaded attachment verification failed" >&2
    exit 1
}
echo "published and verified ${#files[@]} attachment(s) on CNB release $TAG"

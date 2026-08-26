#!/usr/bin/env bash
# fetch-embedding-model.sh — pre-cache the BGE-small-zh-v1.5 ONNX model
# into .cache/embedding-models/ so dist/package.sh can bundle it into
# the install tarball. Run once on a host with HuggingFace reach;
# subsequent `make package` runs pick up the cache.
#
# Why a separate script: the model is ~55MB download + ~97MB on disk
# after extraction, slow over CN networks; pinning a build step on
# network is brittle. dist/package.sh warns + skips if not pre-cached.

set -euo pipefail

SCRIPT_DIR=$(cd -- "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd)
DEST="$REPO_ROOT/.cache/embedding-models"
mkdir -p "$DEST"

log()  { printf '[fetch-emb] %s\n' "$*"; }
warn() { printf '[fetch-emb] warn: %s\n' "$*" >&2; }
die()  { printf '[fetch-emb] error: %s\n' "$*" >&2; exit 1; }

# The bundle layout fastembed-go expects under CacheDir/<EmbeddingModel>/
# is the extracted qdrant-fastembed tarball. Keep this URL aligned with
# github.com/anush008/fastembed-go@v1.0.0's downloadFromGcs implementation.
MODEL=fast-bge-small-zh-v1.5
FASTEMBED_BASE=${ONGRID_FASTEMBED_BASE:-https://storage.googleapis.com/qdrant-fastembed}
TARGET="$DEST/$MODEL"

FILES=(model_optimized.onnx tokenizer_config.json special_tokens_map.json
       config.json tokenizer.json vocab.txt ort_config.json)

mkdir -p "$TARGET"
missing=0
for f in "${FILES[@]}"; do
    if [[ ! -s "$TARGET/$f" ]]; then
        missing=1
        break
    fi
done

if [[ "$missing" -eq 0 ]]; then
    log "$MODEL already present — skipping"
else
    tmp=$(mktemp)
    trap 'rm -f "$tmp"' EXIT
    url="${FASTEMBED_BASE%/}/${MODEL}.tar.gz"
    log "fetching $url"
    curl -fL --retry 3 --connect-timeout 15 -o "$tmp" "$url" || die "failed to fetch $url"
    rm -rf "$TARGET"
    tar -xzf "$tmp" -C "$DEST"
    for f in "${FILES[@]}"; do
        [[ -s "$TARGET/$f" ]] || die "downloaded model missing $f"
    done
fi

log "cached $TARGET ($(du -sh "$TARGET" | awk '{print $1}'))"
log "next \`make package\` will bundle this under embeddings/$MODEL/"

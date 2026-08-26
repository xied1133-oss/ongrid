#!/usr/bin/env bash
# Re-vendor the built-in knowledge vault into the ongrid binary.
#
# The vault content lives upstream in github.com/ongridio/vault. It is
# embedded (go:embed) into the manager binary so a fresh install populates
# its knowledge base with no network access — see
# internal/manager/biz/knowledge/builtin_vault.go.
#
# Run this after the upstream vault changes to refresh the vendored copy,
# then commit the diff under internal/manager/biz/knowledge/builtin_vault/.
#
# Usage:
#   scripts/sync-builtin-vault.sh [path-to-vault-checkout]
#
# With no arg it clones a shallow copy of the upstream repo to a temp dir
# (needs git access to github.com/ongridio/vault). Pass a local checkout
# path to vendor from that instead (offline / pinned).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DST="$REPO_ROOT/internal/manager/biz/knowledge/builtin_vault"
UPSTREAM="git@github.com:ongridio/vault.git"

cleanup_tmp=""
trap '[ -n "$cleanup_tmp" ] && rm -rf "$cleanup_tmp"' EXIT

if [ "$#" -ge 1 ]; then
  SRC="$1"
  [ -d "$SRC" ] || { echo "error: source dir not found: $SRC" >&2; exit 1; }
else
  cleanup_tmp="$(mktemp -d)"
  SRC="$cleanup_tmp"
  echo "[sync-builtin-vault] cloning $UPSTREAM (shallow)..."
  git clone --depth=1 "$UPSTREAM" "$SRC"
fi

echo "[sync-builtin-vault] vendoring .md from $SRC → $DST"
rm -rf "$DST"
mkdir -p "$DST"
# Copy only first-party indexable prose (.md), preserving the directory
# tree; skip .git and `reference/external/`.
#
# WHY exclude reference/external/: that subtree is third-party scraped
# article content (LWN / brendangregg / et al) — useful for an internal
# knowledge graph but redistributing other people's articles inside the
# binary is a license headache, and the first-party docs already cover
# the same ground from our angle. The 38 first-party files give the
# operator a topical starter pack; external content stays on the
# upstream git repo for operators who explicitly opt-in by registering
# the github URL alongside builtin://vault.
(cd "$SRC" && find . \
    -path ./.git -prune -o \
    -path './reference/external' -prune -o \
    -type f -name '*.md' -print) | while read -r f; do
  mkdir -p "$DST/$(dirname "$f")"
  cp "$SRC/$f" "$DST/$f"
done

count="$(find "$DST" -type f -name '*.md' | wc -l | tr -d ' ')"
echo "[sync-builtin-vault] done — $count markdown files vendored."
echo "[sync-builtin-vault] review & commit: git add internal/manager/biz/knowledge/builtin_vault"

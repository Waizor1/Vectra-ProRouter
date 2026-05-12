#!/usr/bin/env bash
set -euo pipefail

export COPYFILE_DISABLE=1

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

STAMP="$(date -u +%Y%m%d-%H%M%S)"
DEFAULT_STAGE="/tmp/vectra-web-release-$STAMP"
DEFAULT_OUTPUT="/tmp/vectra-web-release-$STAMP.tar.gz"

STAGE_DIR="${1:-$DEFAULT_STAGE}"
OUTPUT_TAR="${2:-$DEFAULT_OUTPUT}"

rm -rf "$STAGE_DIR" "$OUTPUT_TAR"
mkdir -p "$STAGE_DIR"

cleanup_copyfile_noise() {
  find "$1" -name '._*' -delete
}

sync_dir() {
  local source_rel="$1"
  local dest_rel="$2"
  mkdir -p "$STAGE_DIR/$dest_rel"
  rsync -a --delete "$REPO_ROOT/$source_rel/" "$STAGE_DIR/$dest_rel/"
  cleanup_copyfile_noise "$STAGE_DIR/$dest_rel"
}

sync_dir_with_excludes() {
  local source_rel="$1"
  local dest_rel="$2"
  shift 2

  mkdir -p "$STAGE_DIR/$dest_rel"

  local rsync_args=( -a --delete )
  local exclude
  for exclude in "$@"; do
    rsync_args+=( --exclude "$exclude" )
  done

  rsync "${rsync_args[@]}" "$REPO_ROOT/$source_rel/" "$STAGE_DIR/$dest_rel/"
  cleanup_copyfile_noise "$STAGE_DIR/$dest_rel"
}

cd "$REPO_ROOT"

sync_dir_with_excludes "apps" "apps" ".env" "web/.env" "web/.next" "web/node_modules"
sync_dir_with_excludes "packages" "packages" "node_modules" "*/node_modules"
sync_dir_with_excludes "deploy" "deploy" "runtime"

cp \
  "$REPO_ROOT/docker-compose.yml" \
  "$REPO_ROOT/Caddyfile" \
  "$REPO_ROOT/Dockerfile.web" \
  "$REPO_ROOT/package.json" \
  "$REPO_ROOT/pnpm-lock.yaml" \
  "$REPO_ROOT/pnpm-workspace.yaml" \
  "$REPO_ROOT/tsconfig.base.json" \
  "$STAGE_DIR/"

cleanup_copyfile_noise "$STAGE_DIR"

tar --format ustar --exclude='._*' --no-mac-metadata -czf "$OUTPUT_TAR" -C "$STAGE_DIR" .

printf 'Created stage: %s\n' "$STAGE_DIR"
printf 'Created tarball: %s\n' "$OUTPUT_TAR"

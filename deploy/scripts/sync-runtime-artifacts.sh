#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: deploy/scripts/sync-runtime-artifacts.sh --source <artifact-dir> --channel <relative-runtime-artifacts-path> [--target <deploy-root>] [--dry-run]

Safely syncs published artifacts into deploy/runtime/artifacts only.

Examples:
  bash deploy/scripts/sync-runtime-artifacts.sh \
    --source ./dist/openwrt-feed/stable/aarch64_cortex-a53 \
    --channel openwrt/stable/aarch64_cortex-a53

  bash deploy/scripts/sync-runtime-artifacts.sh \
    --source ./dist/bootstrap/passwall2/<tag>/aarch64_cortex-a53 \
    --channel bootstrap/passwall2/<tag>/aarch64_cortex-a53

This script refuses to touch deploy/runtime itself and only permits syncs into
subpaths under deploy/runtime/artifacts.
EOF
}

SOURCE_DIR=""
CHANNEL_PATH=""
TARGET_DIR="/opt/vectra-prorouter"
DRY_RUN=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --source)
      SOURCE_DIR="${2:-}"
      shift 2
      ;;
    --channel)
      CHANNEL_PATH="${2:-}"
      shift 2
      ;;
    --target)
      TARGET_DIR="${2:-}"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [ -z "$SOURCE_DIR" ] || [ -z "$CHANNEL_PATH" ]; then
  echo "--source and --channel are required" >&2
  usage >&2
  exit 1
fi

if [ ! -d "$SOURCE_DIR" ]; then
  echo "Source artifact dir not found: $SOURCE_DIR" >&2
  exit 1
fi

SOURCE_DIR="$(cd "$SOURCE_DIR" && pwd)"
TARGET_DIR="$(cd "$TARGET_DIR" 2>/dev/null && pwd || printf '%s' "$TARGET_DIR")"

case "$CHANNEL_PATH" in
  ""|/*|.|..|*".."*)
    echo "Unsafe channel path: $CHANNEL_PATH" >&2
    exit 1
    ;;
esac

TARGET_ARTIFACT_ROOT="$TARGET_DIR/deploy/runtime/artifacts"
TARGET_PATH="$TARGET_ARTIFACT_ROOT/$CHANNEL_PATH"

if [ ! -d "$TARGET_ARTIFACT_ROOT" ]; then
  echo "Missing target artifact root: $TARGET_ARTIFACT_ROOT" >&2
  exit 1
fi

echo "Safe runtime artifact sync"
echo "  source: $SOURCE_DIR"
echo "  target artifact path: $TARGET_PATH"
echo "  dry-run: $DRY_RUN"

mkdir -p "$TARGET_PATH"

RSYNC_ARGS="-a --delete"
if [ "$DRY_RUN" -eq 1 ]; then
  RSYNC_ARGS="$RSYNC_ARGS --dry-run"
fi

rsync $RSYNC_ARGS "$SOURCE_DIR/" "$TARGET_PATH/"

echo "Artifact sync completed safely."

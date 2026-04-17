#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
Usage: deploy/scripts/deploy-web-release.sh --source <staging-dir> [--target <deploy-root>] [--backup-root <backup-root>] [--dry-run]

Safely syncs a prepared web release slice into a non-git VPS deploy root.

This script is intentionally allowlist-only and will refuse to touch live runtime state.
Protected paths:
  - deploy/runtime/**
  - .env
  - apps/web/.env

Expected source contents are a release slice with only approved releasable files,
for example: apps/web, packages/contracts, packages/db, deploy/, docker-compose.yml,
Caddyfile, Dockerfile.web, package.json, pnpm-lock.yaml, pnpm-workspace.yaml,
tsconfig.base.json.
EOF
}

SOURCE_DIR=""
TARGET_DIR="/opt/vectra-prorouter"
BACKUP_ROOT="/opt/vectra-prorouter-backups"
DRY_RUN=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    --source)
      SOURCE_DIR="${2:-}"
      shift 2
      ;;
    --target)
      TARGET_DIR="${2:-}"
      shift 2
      ;;
    --backup-root)
      BACKUP_ROOT="${2:-}"
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

if [ -z "$SOURCE_DIR" ]; then
  echo "--source is required" >&2
  usage >&2
  exit 1
fi

if [ ! -d "$SOURCE_DIR" ]; then
  echo "Source dir not found: $SOURCE_DIR" >&2
  exit 1
fi

TARGET_DIR="$(cd "$TARGET_DIR" 2>/dev/null && pwd || printf '%s' "$TARGET_DIR")"
SOURCE_DIR="$(cd "$SOURCE_DIR" && pwd)"

case "$TARGET_DIR" in
  /|"")
    echo "Refusing to use unsafe target dir: $TARGET_DIR" >&2
    exit 1
    ;;
esac

for required in "$TARGET_DIR/.env" "$TARGET_DIR/deploy/runtime/postgres/PG_VERSION"; do
  if [ ! -e "$required" ]; then
    echo "Missing required protected runtime path: $required" >&2
    echo "Refusing to sync into a target that does not look like the live deploy root." >&2
    exit 1
  fi
done

if [ -e "$SOURCE_DIR/deploy/runtime" ]; then
  echo "Refusing source slice: deploy/runtime must never be part of a web release sync." >&2
  exit 1
fi

if [ -e "$SOURCE_DIR/.env" ] || [ -e "$SOURCE_DIR/apps/web/.env" ]; then
  echo "Refusing source slice: env files must not be embedded in the release payload." >&2
  exit 1
fi

TIMESTAMP="$(date -u +%Y%m%d-%H%M%S)"
BACKUP_DIR="$BACKUP_ROOT/web-release-$TIMESTAMP"

mkdir -p "$BACKUP_DIR"

echo "Safe web release sync"
echo "  source: $SOURCE_DIR"
echo "  target: $TARGET_DIR"
echo "  backup: $BACKUP_DIR"
echo "  dry-run: $DRY_RUN"

copy_if_present() {
  rel="$1"
  if [ -e "$TARGET_DIR/$rel" ]; then
    dest_dir="$BACKUP_DIR/$(dirname "$rel")"
    mkdir -p "$dest_dir"
    cp -a "$TARGET_DIR/$rel" "$dest_dir/"
  fi
}

copy_dir_without_runtime_if_present() {
  rel="$1"
  if [ -d "$TARGET_DIR/$rel" ]; then
    mkdir -p "$BACKUP_DIR/$rel"
    rsync -a \
      --exclude 'runtime' \
      "$TARGET_DIR/$rel/" "$BACKUP_DIR/$rel/"
  fi
}

copy_if_present apps
copy_if_present packages
copy_dir_without_runtime_if_present deploy
copy_if_present docker-compose.yml
copy_if_present Caddyfile
copy_if_present Dockerfile.web
copy_if_present package.json
copy_if_present pnpm-lock.yaml
copy_if_present pnpm-workspace.yaml
copy_if_present tsconfig.base.json

if [ -e "$BACKUP_DIR/deploy/runtime" ]; then
  echo "Backup guard failed: deploy/runtime leaked into the backup payload." >&2
  exit 1
fi

sync_dir_if_present() {
  rel="$1"
  if [ -d "$SOURCE_DIR/$rel" ]; then
    mkdir -p "$TARGET_DIR/$rel"
    rsync_args="-a --delete"
    if [ "$DRY_RUN" -eq 1 ]; then
      rsync_args="$rsync_args --dry-run"
    fi
    case "$rel" in
      deploy)
        rsync $rsync_args \
          --exclude 'runtime' \
          "$SOURCE_DIR/$rel/" "$TARGET_DIR/$rel/"
        ;;
      apps)
        rsync $rsync_args \
          --exclude 'web/.env' \
          "$SOURCE_DIR/$rel/" "$TARGET_DIR/$rel/"
        ;;
      *)
        rsync $rsync_args "$SOURCE_DIR/$rel/" "$TARGET_DIR/$rel/"
        ;;
    esac
  fi
}

sync_file_if_present() {
  rel="$1"
  if [ -f "$SOURCE_DIR/$rel" ]; then
    if [ "$DRY_RUN" -eq 1 ]; then
      printf 'Would copy %s -> %s\n' "$SOURCE_DIR/$rel" "$TARGET_DIR/$rel"
    else
      cp -a "$SOURCE_DIR/$rel" "$TARGET_DIR/$rel"
    fi
  fi
}

sync_dir_if_present apps
sync_dir_if_present packages
sync_dir_if_present deploy

sync_file_if_present docker-compose.yml
sync_file_if_present Caddyfile
sync_file_if_present Dockerfile.web
sync_file_if_present package.json
sync_file_if_present pnpm-lock.yaml
sync_file_if_present pnpm-workspace.yaml
sync_file_if_present tsconfig.base.json

if [ "$DRY_RUN" -eq 0 ]; then
  if [ ! -f "$TARGET_DIR/deploy/runtime/postgres/PG_VERSION" ]; then
    echo "Post-sync guard failed: deploy/runtime/postgres/PG_VERSION is missing." >&2
    echo "Runtime preservation failed. Do not rebuild containers." >&2
    exit 1
  fi

  if [ ! -d "$TARGET_DIR/deploy/runtime/artifacts" ]; then
    echo "Post-sync guard failed: deploy/runtime/artifacts is missing." >&2
    echo "Runtime preservation failed. Do not rebuild containers." >&2
    exit 1
  fi
fi

echo "Release sync completed safely."
echo "Backup available at: $BACKUP_DIR"

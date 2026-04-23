#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: vps-disk-cleanup.sh [--dry-run] [--aggressive]

Default Vectra VPS cleanup:
- prune Docker builder cache older than 7 days
- prune unused Docker images older than 7 days
- run apt clean
- remove stale /tmp release and staging artifacts older than 2 days

Aggressive mode:
- prune all unused Docker builder cache
- prune all unused Docker images
- remove stale /tmp release and staging artifacts older than 12 hours

This script intentionally does NOT touch:
- running containers
- Docker volumes
- PostgreSQL data
- /opt/vectra-prorouter-backups
- journal, /root/go, /root/.cache
EOF
}

DRY_RUN=0
AGGRESSIVE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --aggressive)
      AGGRESSIVE=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

log() {
  printf '[%s] %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$*"
}

run_cmd() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    log "[dry-run] $*"
    return 0
  fi
  "$@"
}

show_root_usage() {
  df -h / | sed -n '1,2p'
}

show_docker_df() {
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    docker system df
  else
    log "Docker daemon unavailable; skipping docker disk report."
  fi
}

cleanup_docker() {
  if ! command -v docker >/dev/null 2>&1; then
    log "Docker not installed; skipping docker cleanup."
    return 0
  fi

  if ! docker info >/dev/null 2>&1; then
    log "Docker daemon unavailable; skipping docker cleanup."
    return 0
  fi

  if [[ "$AGGRESSIVE" -eq 1 ]]; then
    log "Aggressive mode: pruning all unused Docker builder cache."
    run_cmd docker builder prune -af || true

    log "Aggressive mode: pruning all unused Docker images."
    run_cmd docker image prune -af || true
    return 0
  fi

  log "Pruning Docker builder cache older than 7 days."
  run_cmd docker builder prune -af --filter "until=168h" || true

  log "Pruning unused Docker images older than 7 days."
  run_cmd docker image prune -af --filter "until=168h" || true
}

cleanup_apt() {
  if ! command -v apt >/dev/null 2>&1; then
    log "apt not installed; skipping apt cache cleanup."
    return 0
  fi

  log "Cleaning apt cache."
  run_cmd apt clean
}

cleanup_tmp_artifacts() {
  local min_age_minutes=2880
  if [[ "$AGGRESSIVE" -eq 1 ]]; then
    min_age_minutes=720
  fi

  local -a stale_paths=()
  while IFS= read -r path; do
    stale_paths+=("$path")
  done < <(
    find /tmp -maxdepth 1 \
      \( -name 'vectra*' -o -name 'passwall-bootstrap*' -o -name 'release-*' -o -name 'ustar-test*' \) \
      -mmin "+${min_age_minutes}" \
      -print 2>/dev/null | sort
  )

  if [[ "${#stale_paths[@]}" -eq 0 ]]; then
    if [[ "$AGGRESSIVE" -eq 1 ]]; then
      log "No stale /tmp release artifacts older than 12 hours."
    else
      log "No stale /tmp release artifacts older than 2 days."
    fi
    return 0
  fi

  if [[ "$AGGRESSIVE" -eq 1 ]]; then
    log "Removing stale /tmp release artifacts older than 12 hours:"
  else
    log "Removing stale /tmp release artifacts older than 2 days:"
  fi
  printf '%s\n' "${stale_paths[@]}"

  if [[ "$DRY_RUN" -eq 1 ]]; then
    return 0
  fi

  rm -rf -- "${stale_paths[@]}"
}

main() {
  local lock_file="/run/lock/vectra-vps-disk-cleanup.lock"
  mkdir -p "$(dirname "$lock_file")"
  exec 9>"$lock_file"
  if ! flock -n 9; then
    log "Another cleanup run is already active; exiting."
    exit 0
  fi

  if [[ "$AGGRESSIVE" -eq 1 ]]; then
    log "Starting aggressive VPS disk cleanup."
  else
    log "Starting conservative VPS disk cleanup."
  fi
  log "Before cleanup:"
  show_root_usage
  show_docker_df

  cleanup_docker
  cleanup_apt
  cleanup_tmp_artifacts

  log "After cleanup:"
  show_root_usage
  show_docker_df
  log "Cleanup completed."
}

main

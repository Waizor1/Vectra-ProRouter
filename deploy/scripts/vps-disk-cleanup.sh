#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: vps-disk-cleanup.sh [--dry-run]

Conservative Vectra VPS cleanup:
- prune Docker builder cache older than 7 days
- prune unused Docker images older than 7 days
- run apt clean
- remove stale /tmp/vectra* and /tmp/passwall-bootstrap-mirror* artifacts older than 2 days

This script intentionally does NOT touch:
- running containers
- Docker volumes
- PostgreSQL data
- /opt/vectra-prorouter-backups
- journal, /root/go, /root/.cache
EOF
}

DRY_RUN=0
if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
elif [[ $# -gt 0 ]]; then
  usage >&2
  exit 2
fi

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
  local -a stale_paths=()
  while IFS= read -r path; do
    stale_paths+=("$path")
  done < <(
    find /tmp -maxdepth 1 \
      \( -name 'vectra*' -o -name 'passwall-bootstrap-mirror*' \) \
      -mtime +2 \
      -print 2>/dev/null | sort
  )

  if [[ "${#stale_paths[@]}" -eq 0 ]]; then
    log "No stale /tmp Vectra artifacts older than 2 days."
    return 0
  fi

  log "Removing stale /tmp Vectra artifacts older than 2 days:"
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

  log "Starting conservative VPS disk cleanup."
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

#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: vps-disk-guard.sh [options]

Disk guard for the Vectra VPS:
- always records a compact hotspot report to journald/stdout
- if root usage is above the warn threshold, runs the existing conservative cleanup
- if root usage is still very high afterwards, runs the cleanup helper in aggressive mode
- if root usage is still above the warn threshold, prunes old deploy rollback backups
- exits non-zero only if root usage remains above the critical threshold afterwards

Options:
  --dry-run                 Show what would happen without deleting anything
  --warn-use-percent N      Start cleanup when root usage is >= N (default: 75)
  --backup-prune-use-percent N
                            Allow pruning aged rollback backups only when root usage is >= N (default: 82)
  --critical-use-percent N  Fail after cleanup if root usage is >= N (default: 85)
  --backup-root PATH        Rollback-backup root (default: /opt/vectra-prorouter-backups)
  --backup-max-age-days N   Remove deploy rollback backups older than N days (default: 7)
  --backup-keep-recent N    Always keep the newest N deploy rollback backups (default: 6)

Notes:
- Only web deploy rollback directories matching web-release-* or web-deploy-ready-* are pruned.
- SDK trees, PostgreSQL data, Docker volumes, and special backup directories are only reported.
EOF
}

DRY_RUN=0
WARN_USE_PERCENT=75
BACKUP_PRUNE_USE_PERCENT=82
CRITICAL_USE_PERCENT=85
BACKUP_ROOT="/opt/vectra-prorouter-backups"
BACKUP_MAX_AGE_DAYS=7
BACKUP_KEEP_RECENT=6

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --warn-use-percent)
      WARN_USE_PERCENT="${2:-}"
      shift 2
      ;;
    --backup-prune-use-percent)
      BACKUP_PRUNE_USE_PERCENT="${2:-}"
      shift 2
      ;;
    --critical-use-percent)
      CRITICAL_USE_PERCENT="${2:-}"
      shift 2
      ;;
    --backup-root)
      BACKUP_ROOT="${2:-}"
      shift 2
      ;;
    --backup-max-age-days)
      BACKUP_MAX_AGE_DAYS="${2:-}"
      shift 2
      ;;
    --backup-keep-recent)
      BACKUP_KEEP_RECENT="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      echo "Unknown argument: $1" >&2
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

root_use_percent() {
  df -P / | awk 'NR==2 {gsub(/%/, "", $5); print $5}'
}

report_root_usage() {
  df -h / /var /opt /tmp
}

report_hot_paths() {
  shopt -s nullglob
  local -a hot_paths=(
    "$BACKUP_ROOT"
    /opt/vectra-prorouter
    /var/lib/docker
    /var/log
    /root/go
    /root/.cache
    /tmp
    /opt/openwrt-sdk-*
    /opt/*.tar.zst
  )
  shopt -u nullglob

  if [[ "${#hot_paths[@]}" -eq 0 ]]; then
    log "No hot paths matched the configured report set."
    return 0
  fi

  du -sh "${hot_paths[@]}" 2>/dev/null | sort -h
}

report_recent_backups() {
  if [[ ! -d "$BACKUP_ROOT" ]]; then
    log "Backup root is missing: $BACKUP_ROOT"
    return 0
  fi

  find "$BACKUP_ROOT" -maxdepth 1 -mindepth 1 -type d \
    -printf '%TY-%Tm-%Td %TH:%TM %12k KB %f\n' 2>/dev/null | sort | tail -n 15
}

report_docker_summary() {
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    docker system df
  else
    log "Docker daemon unavailable; skipping docker disk summary."
  fi
}

run_conservative_cleanup() {
  local cleanup_script="/opt/vectra-prorouter/deploy/scripts/vps-disk-cleanup.sh"
  if [[ ! -x "$cleanup_script" && ! -f "$cleanup_script" ]]; then
    log "Cleanup script not found: $cleanup_script"
    return 0
  fi

  log "Running conservative cleanup helper."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    bash "$cleanup_script" --dry-run
  else
    bash "$cleanup_script"
  fi
}

run_aggressive_cleanup() {
  local cleanup_script="/opt/vectra-prorouter/deploy/scripts/vps-disk-cleanup.sh"
  if [[ ! -x "$cleanup_script" && ! -f "$cleanup_script" ]]; then
    log "Cleanup script not found: $cleanup_script"
    return 0
  fi

  log "Running aggressive cleanup helper."
  if [[ "$DRY_RUN" -eq 1 ]]; then
    bash "$cleanup_script" --dry-run --aggressive
  else
    bash "$cleanup_script" --aggressive
  fi
}

cleanup_old_release_backups() {
  if [[ ! -d "$BACKUP_ROOT" ]]; then
    log "Backup root is missing; deploy-backup pruning skipped."
    return 0
  fi

  local now_ts
  now_ts="$(date +%s)"
  local -a candidates=()

  while IFS= read -r entry; do
    candidates+=("$entry")
  done < <(
    find "$BACKUP_ROOT" -maxdepth 1 -mindepth 1 -type d \
      \( -name 'web-release-*' -o -name 'web-deploy-ready-*' \) \
      -printf '%T@ %p\n' 2>/dev/null | sort -nr
  )

  if [[ "${#candidates[@]}" -eq 0 ]]; then
    log "No deploy rollback backup directories matched the prune policy."
    return 0
  fi

  local index=0
  local removed=0
  local entry path size mtime age_days
  for entry in "${candidates[@]}"; do
    index=$((index + 1))
    mtime="${entry%% *}"
    path="${entry#* }"

    if (( index <= BACKUP_KEEP_RECENT )); then
      continue
    fi

    age_days=$(( (now_ts - ${mtime%.*}) / 86400 ))
    if (( age_days <= BACKUP_MAX_AGE_DAYS )); then
      continue
    fi

    size="$(du -sh "$path" 2>/dev/null | awk '{print $1}')"
    log "Pruning aged deploy rollback backup: $path (size=${size:-unknown}, age=${age_days}d)"
    if [[ "$DRY_RUN" -eq 0 ]]; then
      rm -rf -- "$path"
    fi
    removed=$((removed + 1))
  done

  if (( removed == 0 )); then
    log "No deploy rollback backups exceeded the retention policy."
  fi
}

report_state() {
  log "Root usage snapshot:"
  report_root_usage
  log "Hot paths:"
  report_hot_paths
  log "Recent backup directories:"
  report_recent_backups
  log "Docker disk summary:"
  report_docker_summary
}

main() {
  local lock_file="/run/lock/vectra-vps-disk-guard.lock"
  mkdir -p "$(dirname "$lock_file")"
  exec 9>"$lock_file"
  if ! flock -n 9; then
    log "Another disk guard run is already active; exiting."
    exit 0
  fi

  local before_use after_cleanup after_aggressive after_backup_prune
  before_use="$(root_use_percent)"

  log "Starting VPS disk guard (warn=${WARN_USE_PERCENT}%, backup-prune=${BACKUP_PRUNE_USE_PERCENT}%, critical=${CRITICAL_USE_PERCENT}%, dry-run=${DRY_RUN})."
  report_state

  if (( before_use < WARN_USE_PERCENT )); then
    log "Root usage ${before_use}% is below warn threshold ${WARN_USE_PERCENT}%; report-only run."
    exit 0
  fi

  log "Root usage ${before_use}% is at or above warn threshold ${WARN_USE_PERCENT}%; cleanup will run."
  run_conservative_cleanup

  after_cleanup="$(root_use_percent)"
  log "Root usage after conservative cleanup: ${after_cleanup}%."

  after_aggressive="$after_cleanup"
  if (( after_cleanup >= BACKUP_PRUNE_USE_PERCENT )); then
    log "Root usage is at or above backup-prune threshold; aggressive cleanup will run before backup pruning."
    run_aggressive_cleanup
    after_aggressive="$(root_use_percent)"
    log "Root usage after aggressive cleanup: ${after_aggressive}%."
  fi

  if (( after_aggressive >= BACKUP_PRUNE_USE_PERCENT )); then
    log "Root usage is at or above backup-prune threshold; applying deploy rollback backup retention."
    cleanup_old_release_backups
  elif (( after_aggressive >= WARN_USE_PERCENT )); then
    log "Root usage is still above warn threshold, but below backup-prune threshold; rollback backups are preserved."
  fi

  after_backup_prune="$(root_use_percent)"
  log "Final root usage after disk guard actions: ${after_backup_prune}%."
  report_state

  if (( after_backup_prune >= CRITICAL_USE_PERCENT )); then
    log "Root usage remains at or above critical threshold ${CRITICAL_USE_PERCENT}%."
    exit 1
  fi

  if (( after_backup_prune >= WARN_USE_PERCENT )); then
    log "Root usage remains above warn threshold ${WARN_USE_PERCENT}%, but below critical."
  else
    log "Root usage is back below warn threshold."
  fi
}

main

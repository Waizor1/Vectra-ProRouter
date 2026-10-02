#!/bin/sh
set -eu
umask 077
: "${POSTGRES_HOST:?POSTGRES_HOST is required}"
: "${POSTGRES_PORT:?POSTGRES_PORT is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${POSTGRES_USER:?POSTGRES_USER is required}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"
case "$BACKUP_DIR" in /*) ;; *) echo 'Backup directory must be absolute' >&2; exit 1;; esac
case "$BACKUP_DIR" in /|*/../*|*/..|*/./*|*/.|*//* ) echo 'Unsafe backup path' >&2; exit 1;; esac
case "$KEEP_DAYS" in ''|*[!0-9]*) echo 'Invalid retention' >&2; exit 1;; esac
# Refuse symlinks in every ancestor; never chmod an existing directory.
CHECK_PATH="$BACKUP_DIR"
while [ "$CHECK_PATH" != / ]; do
  [ ! -L "$CHECK_PATH" ] || { echo 'Symlink backup path refused' >&2; exit 1; }
  CHECK_PATH=$(dirname "$CHECK_PATH")
done
if [ ! -d "$BACKUP_DIR" ]; then mkdir -m 700 "$BACKUP_DIR"; fi
if [ "$(uname -s)" = Darwin ]; then
  DIR_INFO=$(stat -f '%u:%Lp' "$BACKUP_DIR")
else
  DIR_INFO=$(stat -c '%u:%a' "$BACKUP_DIR")
fi
[ "$DIR_INFO" = "$(id -u):700" ] || { echo 'Existing backup directory must be owned by this user and mode 0700' >&2; exit 1; }
TEMP_DIR=$(mktemp -d "$BACKUP_DIR/.vectra-backup.XXXXXX")
trap 'rm -rf "$TEMP_DIR"' EXIT HUP INT TERM
# Separate commands: POSIX pipeline exit status cannot prove pg_dump succeeded.
pg_dump --host "$POSTGRES_HOST" --port "$POSTGRES_PORT" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --clean --if-exists --no-owner --no-privileges > "$TEMP_DIR/dump.sql"
gzip -9 < "$TEMP_DIR/dump.sql" > "$TEMP_DIR/dump.sql.gz"
chmod 600 "$TEMP_DIR/dump.sql.gz"
TIMESTAMP=$(date -u +%Y%m%dT%H%M%SZ)
OUTPUT_FILE="$BACKUP_DIR/vectra-${TIMESTAMP}-$(basename "$TEMP_DIR").sql.gz"
# Same-filesystem atomic publication; unique name avoids concurrent clobbering.
mv "$TEMP_DIR/dump.sql.gz" "$OUTPUT_FILE"
find "$BACKUP_DIR" -maxdepth 1 -type f -name 'vectra-*.sql.gz' -mtime "+$KEEP_DAYS" -delete
echo "Backup completed: $OUTPUT_FILE"

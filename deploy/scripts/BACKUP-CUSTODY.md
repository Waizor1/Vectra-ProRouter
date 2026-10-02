# PostgreSQL backup custody

Local script creates a new backup directory mode0700 and artifacts0600 with umask077. Existing directories must already be owned by the invoking user and private; the script refuses unsafe modes/symlinks without changing existing permissions. Failed dump/compression never publishes an artifact or success message. Plain intermediate dump stays in private temporary storage and is removed on exit; publication uses same-filesystem rename. Retention defaults14days and applies only matching top-level dump artifacts.

Backups can contain encrypted private Connect data alongside other confidential data. Store them under the existing restricted operator backup custody and existing encrypted-volume/storage policy. Keep the existing VECTRA_SECRETS_KEY outside backup files, DB dumps and backup-directory access; restoration of confidential data requires both the backup and separately controlled key custody. Do not copy/provision keys through this script. This change does not provision backup encryption, alter production permissions or run a real database dump. Operator deployment must supply an already-private mount and verify the existing storage encryption/custody separately.

Verification: python3 deploy/scripts/backup-postgres.test.py uses only temporary synthetic pg_dump/gzip executables; no database contacted.

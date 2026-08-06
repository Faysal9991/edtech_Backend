#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
: "${RESTORE_CONFIRM:?Set RESTORE_CONFIRM=RESTORE_LMS_DATABASE to continue}"
if [ "$RESTORE_CONFIRM" != "RESTORE_LMS_DATABASE" ]; then
  printf '%s\n' "RESTORE_CONFIRM must equal RESTORE_LMS_DATABASE" >&2
  exit 1
fi
backup_file=${1:?usage: restore-postgres.sh path/to/backup.dump}
if [ ! -f "$backup_file" ]; then
  printf '%s\n' "backup file does not exist: $backup_file" >&2
  exit 1
fi
pg_restore --clean --if-exists --no-owner --no-acl --single-transaction --dbname="$DATABASE_URL" "$backup_file"

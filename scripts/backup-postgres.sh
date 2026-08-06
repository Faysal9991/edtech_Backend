#!/bin/sh
set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"
backup_directory=${1:-./backups}
mkdir -p "$backup_directory"
timestamp=$(date -u +%Y%m%dT%H%M%SZ)
destination="$backup_directory/lms-$timestamp.dump"
pg_dump --format=custom --no-owner --no-acl --file="$destination" "$DATABASE_URL"
printf '%s\n' "$destination"

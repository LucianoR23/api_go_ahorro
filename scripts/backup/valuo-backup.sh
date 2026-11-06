#!/usr/bin/env bash
# Encrypted Postgres backups to Cloudflare R2 (plan 0.18).
# Installed as /usr/local/sbin/valuo-backup and run daily as root by valuo-backup.timer.
#
# For every "container:database" in BACKUP_TARGETS:
#   pg_dump (custom format, inside the database container)
#   -> age, encrypted to the public key in /etc/valuo-backup/recipient.txt
#   -> PUT to R2 as postgres/<db>/<db>-<UTC stamp>.dump.age
# The plaintext dump never touches the disk. The private key is not on the server.
# Retention is an R2 lifecycle rule (30 days), so this script never deletes anything.
set -euo pipefail
umask 077

conf=/etc/valuo-backup
# shellcheck source=/dev/null
. "$conf/env" # R2_ENDPOINT R2_BUCKET R2_ACCESS_KEY_ID R2_SECRET_ACCESS_KEY BACKUP_TARGETS

work=$(mktemp -d /var/tmp/valuo-backup.XXXXXX)
trap 'rm -rf "$work"' EXIT
stamp=$(date -u +%Y%m%dT%H%M%SZ)
min_bytes=${BACKUP_MIN_BYTES:-10240}

# s3 <curl args...>: signed request to R2; the credentials go through a config
# file descriptor, never through the process arguments.
s3() {
  curl -sS --fail-with-body --retry 3 --retry-all-errors \
    --aws-sigv4 "aws:amz:auto:s3" \
    -K <(printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY") "$@"
}

rc=0
for target in $BACKUP_TARGETS; do
  container=${target%%:*}
  db=${target#*:}
  file="$work/$db.dump.age"
  key="postgres/$db/$db-$stamp.dump.age"

  if ! docker exec "$container" sh -c 'pg_dump -U "$POSTGRES_USER" -Fc -d "$1"' _ "$db" \
    | age -R "$conf/recipient.txt" -o "$file"; then
    echo "FAIL $db: pg_dump or age failed"
    rc=1
    continue
  fi

  size=$(stat -c %s "$file")
  if [ "$size" -lt "$min_bytes" ]; then
    echo "FAIL $db: dump is only $size bytes (expected at least $min_bytes)"
    rc=1
    continue
  fi

  if s3 -T "$file" "$R2_ENDPOINT/$R2_BUCKET/$key" -o /dev/null; then
    echo "ok $db: $size bytes -> $key"
  else
    echo "FAIL $db: upload to R2 failed"
    rc=1
  fi
  rm -f "$file"
done
exit "$rc"

#!/usr/bin/env bash
# Restore test for the encrypted backups (plan 0.18). Run monthly, interactively, as root:
#   sudo valuo-backup-restore-test
# For every target: takes the newest backup in R2, checks it is recent, decrypts it
# with the private key you paste (kept in RAM under /dev/shm, removed on exit),
# restores it into a throwaway Postgres with no network, and compares row counts of
# every table with the live database. Prints table names and counts only, no data.
set -euo pipefail
umask 077

conf=/etc/valuo-backup
# shellcheck source=/dev/null
. "$conf/env"
image=${RESTORE_IMAGE:-postgres:18.6-alpine3.24} # already on the server (valuo-next-db)
max_age_hours=${RESTORE_MAX_AGE_HOURS:-26}

s3() {
  curl -sS --fail-with-body --retry 3 --retry-all-errors \
    --aws-sigv4 "aws:amz:auto:s3" \
    -K <(printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY") "$@"
}

id_file=$(mktemp /dev/shm/valuo-restore.XXXXXX)
work=$(mktemp -d /var/tmp/valuo-restore.XXXXXX)
cname="valuo-restore-test-$$"
cleanup() {
  rm -f "$id_file"
  rm -rf "$work"
  docker rm -f "$cname" >/dev/null 2>&1 || true
}
trap cleanup EXIT

read -rsp "Paste the age private key (AGE-SECRET-KEY-1...) and press Enter: " secret
echo
printf '%s\n' "$secret" >"$id_file"
unset secret
if [ "$(age-keygen -y "$id_file")" != "$(grep -v '^#' "$conf/recipient.txt")" ]; then
  echo "STOP: that private key does not match /etc/valuo-backup/recipient.txt"
  exit 1
fi
echo "key matches the backup recipient"

docker run -d --rm --pull never --name "$cname" --network none \
  -e POSTGRES_PASSWORD="$(head -c 18 /dev/urandom | base64)" "$image" >/dev/null
for _ in $(seq 1 30); do
  docker exec "$cname" pg_isready -U postgres -q && break
  sleep 1
done

rc=0
now=$(date -u +%s)
for target in $BACKUP_TARGETS; do
  container=${target%%:*}
  db=${target#*:}
  echo "== $db"

  newest=$(s3 "$R2_ENDPOINT/$R2_BUCKET?list-type=2&prefix=postgres/$db/" \
    | grep -o '<Key>[^<]*' | sed 's/<Key>//' | sort | tail -1)
  if [ -z "$newest" ]; then
    echo "FAIL no backup found in R2"
    rc=1
    continue
  fi
  stamp=$(basename "$newest" .dump.age)
  stamp=${stamp##*-}
  taken=$(date -u -d "${stamp:0:4}-${stamp:4:2}-${stamp:6:2} ${stamp:9:2}:${stamp:11:2}:${stamp:13:2}" +%s)
  age_h=$(((now - taken) / 3600))
  echo "newest: $newest (${age_h}h old)"
  if [ "$age_h" -gt "$max_age_hours" ]; then
    echo "FAIL newest backup is older than ${max_age_hours}h: the daily job is not running"
    rc=1
  fi

  s3 -o "$work/$db.dump.age" "$R2_ENDPOINT/$R2_BUCKET/$newest"
  docker exec "$cname" createdb -U postgres "$db"
  # Decrypt straight into pg_restore: the plaintext dump is never written to disk.
  # Roles and extensions of the source server may not exist here; those errors are
  # expected, so they are counted instead of aborting.
  errors=$(age -d -i "$id_file" "$work/$db.dump.age" \
    | docker exec -i "$cname" pg_restore -U postgres -d "$db" --no-owner --no-privileges 2>&1 \
    | grep -c 'error:' || true)
  rm -f "$work/$db.dump.age"
  echo "pg_restore errors: $errors"

  tables=$(docker exec "$cname" psql -U postgres -d "$db" -X -A -t -c \
    "select quote_ident(table_name) from information_schema.tables
     where table_schema = 'public' and table_type = 'BASE TABLE' order by 1;")
  if [ -z "$tables" ]; then
    echo "FAIL no tables restored in schema public"
    rc=1
    continue
  fi
  printf '%-40s %12s %12s\n' table restored live
  for t in $tables; do
    restored=$(docker exec "$cname" psql -U postgres -d "$db" -X -A -t -c "select count(*) from public.$t;")
    live=$(docker exec "$container" sh -c 'psql -U "$POSTGRES_USER" -d "$1" -X -A -t -c "$2"' _ "$db" \
      "select count(*) from public.$t;" 2>/dev/null || echo "?")
    printf '%-40s %12s %12s\n' "$t" "$restored" "$live"
  done
done

[ "$rc" -eq 0 ] && echo "== restore test OK" || echo "== restore test FAILED"
exit "$rc"

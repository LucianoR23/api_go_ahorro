#!/usr/bin/env bash
# One-time setup of the encrypted backups (plan 0.18). Interactive; run on the server:
#   scp -r scripts/backup ubuntu@<server-ip>:valuo-backup
#   ssh ubuntu@<server-ip>
#   sudo bash valuo-backup/install.sh
# Safe to run again: keeps the existing key and asks before replacing the R2 settings.
set -euo pipefail
umask 077
here=$(cd "$(dirname "$0")" && pwd)
conf=/etc/valuo-backup

echo "== 1. age (from the Ubuntu archive)"
DEBIAN_FRONTEND=noninteractive NEEDRESTART_SUSPEND=1 apt-get install -y -qq age >/dev/null
age --version
install -d -m 700 "$conf"

echo "== 2. encryption key"
if [ -s "$conf/recipient.txt" ]; then
  echo "kept the existing public key: $(cat "$conf/recipient.txt")"
else
  key=$(age-keygen 2>/dev/null)
  printf '%s\n' "$key" | age-keygen -y >"$conf/recipient.txt"
  echo
  echo "PRIVATE KEY. Save these 3 lines in your password manager now."
  echo "It is NOT stored on this server; without it no backup can be read."
  echo "-----------------------------------------------------------------"
  printf '%s\n' "$key"
  echo "-----------------------------------------------------------------"
  unset key
  read -rp "Type SAVED once it is in the password manager: " ok
  if [ "$ok" != "SAVED" ]; then
    rm -f "$conf/recipient.txt"
    echo "STOP: nothing saved, run the script again"
    exit 1
  fi
  clear
  echo "public key: $(cat "$conf/recipient.txt")"
fi

echo "== 3. R2 settings"
if [ -s "$conf/env" ] && { read -rp "Keep the existing R2 settings? [Y/n] " keep; [ "${keep:-Y}" != "n" ]; }; then
  echo "kept $conf/env"
else
  default_endpoint=$(docker exec coolify-db psql -U coolify -d coolify -X -A -t \
    -c "select endpoint from s3_storages order by id limit 1;" 2>/dev/null || true)
  read -rp "R2 endpoint [$default_endpoint]: " endpoint
  read -rp "Bucket [valuo-backups]: " bucket
  read -rp "Access Key ID: " key_id
  read -rsp "Secret Access Key (hidden): " secret
  echo
  read -rp "Targets container:db [d8ygplsmw84rc9epykd7vp43:ahorra rao0gzibhse5es9sj7mi5xvm:valuo_next]: " targets
  {
    printf 'R2_ENDPOINT=%q\n' "${endpoint:-$default_endpoint}"
    printf 'R2_BUCKET=%q\n' "${bucket:-valuo-backups}"
    printf 'R2_ACCESS_KEY_ID=%q\n' "$key_id"
    printf 'R2_SECRET_ACCESS_KEY=%q\n' "$secret"
    printf 'BACKUP_TARGETS=%q\n' "${targets:-d8ygplsmw84rc9epykd7vp43:ahorra rao0gzibhse5es9sj7mi5xvm:valuo_next}"
  } >"$conf/env"
  unset secret
  chmod 600 "$conf/env"
fi

echo "== 4. R2 access check (writes and reads a small object under _probe/)"
# shellcheck source=/dev/null
. "$conf/env"
probe="_probe/install-$(date -u +%Y%m%dT%H%M%SZ).txt"
creds() { printf 'user = "%s:%s"\n' "$R2_ACCESS_KEY_ID" "$R2_SECRET_ACCESS_KEY"; }
echo "valuo-backup probe" >/var/tmp/valuo-probe.txt
curl -sS --fail-with-body --aws-sigv4 "aws:amz:auto:s3" -K <(creds) \
  -T /var/tmp/valuo-probe.txt "$R2_ENDPOINT/$R2_BUCKET/$probe" -o /dev/null
got=$(curl -sS --fail-with-body --aws-sigv4 "aws:amz:auto:s3" -K <(creds) "$R2_ENDPOINT/$R2_BUCKET/$probe")
rm -f /var/tmp/valuo-probe.txt
[ "$got" = "valuo-backup probe" ] && echo "R2 write and read ok" || { echo "STOP: R2 read back failed"; exit 1; }

echo "== 5. scripts and systemd timer (daily 04:15 UTC = 01:15 Buenos Aires)"
install -m 755 "$here/valuo-backup.sh" /usr/local/sbin/valuo-backup
install -m 755 "$here/restore-test.sh" /usr/local/sbin/valuo-backup-restore-test
cat >/etc/systemd/system/valuo-backup.service <<'UNIT'
[Unit]
Description=Encrypted Postgres backups to R2 (plan 0.18)
Wants=network-online.target
After=network-online.target docker.service

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/valuo-backup
Nice=10
IOSchedulingClass=idle
UNIT
cat >/etc/systemd/system/valuo-backup.timer <<'UNIT'
[Unit]
Description=Daily encrypted Postgres backups (plan 0.18)

[Timer]
OnCalendar=*-*-* 04:15:00 UTC
RandomizedDelaySec=5m
Persistent=true

[Install]
WantedBy=timers.target
UNIT
chmod 644 /etc/systemd/system/valuo-backup.service /etc/systemd/system/valuo-backup.timer
systemctl daemon-reload
systemctl enable --now valuo-backup.timer
systemctl list-timers valuo-backup.timer --no-pager

echo "== 6. first backup now"
if systemctl start valuo-backup.service; then echo "first backup ok"; else echo "first backup FAILED"; fi
journalctl -u valuo-backup.service -n 20 --no-pager -o cat
echo "== done. Next: sudo valuo-backup-restore-test (paste the private key when asked)"

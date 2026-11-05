# Deploy (Coolify)

The API ships as one Docker image built from `Dockerfile`. Migrations are embedded
in the binary and applied on startup when `MIGRATE_ON_START=true`.

Environments:

| Env | Branch | API domain | Front domain | Database |
|---|---|---|---|---|
| staging | `next` | `https://api-next.valuo.ar` | `https://next.valuo.ar` | `valuo_next` on its own Postgres 18 resource |
| prod (v1, current) | `main` | unchanged | unchanged | prod Postgres, not touched by v2 work |

## 1. Database (once per environment)

Create a Postgres 18 resource in Coolify (image `postgres:18.6-alpine3.24`, no public
port). Postgres 18 images keep data in `/var/lib/postgresql`: make sure the volume is
mounted there, not at `/var/lib/postgresql/data`.

Two roles, least privilege:

- `valuo_migrate` owns the database and the `public` schema. Only migrations use it.
- `valuo_app` is what the API uses at runtime: read/write data, no DDL.

Run as the superuser (`psql -U postgres` in the resource terminal). Generate the
passwords with `openssl rand -hex 24` (hex avoids URL escaping).

```sql
CREATE ROLE valuo_migrate LOGIN PASSWORD '<PASS_MIGRATE>';
CREATE ROLE valuo_app     LOGIN PASSWORD '<PASS_APP>';
CREATE DATABASE valuo_next OWNER valuo_migrate;
REVOKE ALL ON DATABASE valuo_next FROM PUBLIC;
GRANT CONNECT ON DATABASE valuo_next TO valuo_app;
\c valuo_next
GRANT USAGE ON SCHEMA public TO valuo_app;
ALTER DEFAULT PRIVILEGES FOR ROLE valuo_migrate IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO valuo_app;
ALTER DEFAULT PRIVILEGES FOR ROLE valuo_migrate IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO valuo_app;
```

The `pgcrypto` and `citext` extensions are trusted, so `valuo_migrate` creates them
without superuser. Check with `\du`, `\l valuo_next` and `\ddp`.

## 2. Application

- Source: this repo, branch per the table above, build pack Dockerfile.
- Ports exposes: `8080`. Domain: `https://api-...` (with the scheme, so Traefik
  requests a Let's Encrypt certificate).
- Health check: leave Coolify's UI health check off. The image's `HEALTHCHECK`
  (`/health/live`) is the source of truth.
- DNS: an `A` record per host pointing at the server, DNS only (no Cloudflare proxy).
  Create it before the first deploy, or redeploy once it resolves.

## 3. Environment variables (runtime, none are build variables)

| Variable | Value |
|---|---|
| `ENV` | `prod` (Secure `__Host-` refresh cookie, JSON logs) |
| `PORT` | `8080` |
| `LOG_LEVEL` | `info` |
| `TZ` | `America/Argentina/Buenos_Aires` |
| `NO_COLOR` | `1` |
| `DATABASE_URL` | `postgres://valuo_app:<PASS_APP>@<DB_HOST>:5432/valuo_next?sslmode=disable` |
| `MIGRATE_ON_START` | `true` |
| `MIGRATE_DATABASE_URL` | `postgres://valuo_migrate:<PASS_MIGRATE>@<DB_HOST>:5432/valuo_next?sslmode=disable` |
| `JWT_SECRET` / `JWT_REFRESH_SECRET` | two different `openssl rand -hex 32`, never reused across environments |
| `ALLOWED_ORIGINS` / `APP_BASE_URL` | the front's URL |
| `GOOGLE_OAUTH_CLIENT_ID` | same value as the front's `NEXT_PUBLIC_GOOGLE_CLIENT_ID` (empty = `/auth/google` responds 500) |
| `VAPID_PUBLIC_KEY` / `VAPID_PRIVATE_KEY` / `VAPID_SUBJECT` | per environment, from `go run ./cmd/vapidgen`; subject `mailto:...` |
| `RESEND_API_KEY` | sending-only key for the verified domain; empty = no emails |
| `BRAND_NAME` | product name in emails (default `Ahorro`; 1.1 sets `Valuo`) |
| `*_FROM_EMAIL` (4) | sender on the verified domain (default `<BRAND_NAME> <onboarding@resend.dev>`) |
| `SOPORTE_API_URL` / `SOPORTE_API_KEY` | empty = `/support/*` responds 503 |

`<DB_HOST>` is the internal hostname Coolify shows in "Postgres URL (internal)". The
app and the database must be in the same Coolify project/environment to reach it.
`sslmode=disable` is acceptable only because traffic stays on the internal Docker
network and the database has no public port (revisited in plan item 0.20).

## 4. Migrations

On every start with `MIGRATE_ON_START=true` the API, before opening its pool:

1. connects with `MIGRATE_DATABASE_URL`,
2. takes a Postgres advisory lock (concurrent instances wait up to 2 minutes),
3. applies pending migrations and logs `migrations applied from_version=.. to_version=..`.

If a migration fails, or the database is already dirty, the process exits with code 1:
the new container never becomes healthy and Coolify keeps the previous one running.
Fix a dirty database by hand: inspect the failed migration, repair the schema, then
`migrate -path migrations -database "$MIGRATE_DATABASE_URL" force <version>` and
redeploy. Never edit an applied migration; add a new one.

Rollbacks (`down`) are manual and deliberate, never part of a deploy.

## 5. Verify

```bash
curl https://api-next.valuo.ar/health/live    # {"status":"ok"}
curl https://api-next.valuo.ar/health/ready   # {"status":"ok"}: valuo_app reaches the DB
```

Then register a user from the front.

---

## 6. Server hardening (plan 0.20)

One Coolify server (Oracle Cloud, Ubuntu 24.04 aarch64) runs the staging app
and API, the landing and the old production app, **plus other projects of the
owner** (terrua, soporte, portfolio, rentar, beszel). Restarting the proxy or
Docker affects all of them; each step says what it touches. Order matters: inventory first, then the steps that cannot
lock you out, SSH last with a second session open.

Status legend: ☐ to do · ☑ done (date).

### 6.0 Inventory (read only, run first and keep the output)

Script: `valuo_workspace/server-ops/inventory.sh` (outputs are git-ignored).
Findings 2026-11-05: password and keyboard-interactive login already off; root
only by key (`without-password`; keys: Oracle's "login as ubuntu" block and
Coolify's); `MaxAuthTries 6`, `X11Forwarding yes`; unattended-upgrades active;
fail2ban not installed; Docker `json-file` 10m x 3; journald 2.3 GB, no age
limit; Postgres ports published only on 127.0.0.1; Oracle ingress open for 22,
80, 443 **and 3000, 8000, 6001** (nothing listens on 3000; Coolify is used
through its domain); an `apt-get update` held the apt lock.

```bash
lsb_release -ds; uname -m
sudo sshd -T | grep -Ei '^(permitrootlogin|passwordauthentication|kbdinteractiveauthentication|pubkeyauthentication|maxauthtries|x11forwarding) '
ls /etc/ssh/sshd_config.d/
sudo awk '{print $1, $NF}' /root/.ssh/authorized_keys      # key types and comments only
sudo ss -tlnp | awk 'NR==1 || /LISTEN/'
docker ps --format 'table {{.Names}}\t{{.Ports}}'
sudo iptables -S INPUT | head -40; sudo ufw status
cat /etc/apt/apt.conf.d/20auto-upgrades; systemctl is-active unattended-upgrades
systemctl is-active fail2ban
cat /etc/docker/daemon.json 2>/dev/null; docker info --format '{{.LoggingDriver}}'
journalctl --disk-usage; grep -rE '^(MaxRetentionSec|SystemMaxUse)' /etc/systemd/journald.conf /etc/systemd/journald.conf.d/ 2>/dev/null
sudo du -sh /var/lib/docker/containers
pgrep -a 'apt|unattended'
```

Plus, from the Oracle Cloud console: the VCN **Security List / NSG ingress rules**
of the instance, and how you open Coolify (domain or `:8000`).

### 6.1 Cloud firewall (Oracle Security List) ☐

Docker publishes ports through its own iptables chains, which bypass `ufw` and
the host's INPUT rules, so the **cloud firewall is the one that counts**.
Ingress allowed: TCP 22 (SSH), 80 and 443. Nothing else: no 5432 (Postgres),
no 8000/6001/6002 (Coolify) once the panel has its own domain. Remove 3000
first (nothing listens there), then 8000, then 6001, checking the panel (live
deployment logs, terminal) after each; re-adding a rule in the console undoes it. Do not enable
`ufw` on this image: Oracle ships its own iptables rules and Docker manages the
rest; mixing them breaks container networking.

Verify from another machine: `nc -zv <ip> 5432` and `nc -zv <ip> 8000` must fail.

### 6.2 Databases not public ☐

In Coolify, every database resource: **Make it publicly available = off**.
`docker ps` must show no `0.0.0.0:5432` (or any DB port) mapping.

### 6.3 Coolify panel ☐

- Profile → **Two-factor authentication** on (keep the recovery codes offline).
- Panel served on its own domain over HTTPS; then close 8000/6001/6002 (6.1).
- Optional: restrict the panel domain to your IPs (Cloudflare Access or a Traefik IP allowlist).

### 6.4 Automatic security updates ☐

Ubuntu cloud images ship `unattended-upgrades` enabled; the inventory confirms it
(`APT::Periodic::Unattended-Upgrade "1";`). A Coolify "could not get lock
/var/lib/apt/lists" alert while it runs is expected (both use apt at the same
time). Reboots stay manual: `cat /var/run/reboot-required` tells when one is
pending; reboot at a quiet hour (it restarts every container on the server).

### 6.5 fail2ban for SSH ☐

```bash
sudo apt-get install -y fail2ban
printf '[sshd]\nenabled  = true\nbackend  = systemd\nmaxretry = 5\nfindtime = 10m\nbantime  = 1h\n' \
  | sudo tee /etc/fail2ban/jail.d/sshd.local >/dev/null
sudo systemctl enable --now fail2ban
sudo fail2ban-client status sshd
```

### 6.6 Logs kept 7 days ☐

The published privacy policy promises technical logs with IPs are kept **7 days**
(`valuo_web/src/config/legal.ts`). Size caps (`json-file` with `max-size`) do not
guarantee age: a quiet container never rotates and its live file keeps weeks of
lines. Send container logs to journald and let journald expire them by age
(`docker logs` and Coolify's log view keep working):

```bash
sudo mkdir -p /etc/systemd/journald.conf.d
printf '[Journal]\nMaxRetentionSec=7day\nSystemMaxUse=2G\n' \
  | sudo tee /etc/systemd/journald.conf.d/retention.conf >/dev/null
sudo systemctl restart systemd-journald

# Docker: journald driver; live-restore keeps containers running while dockerd
# restarts. Keep the existing default-address-pools (Coolify's networks).
sudo cp -a /etc/docker/daemon.json /etc/docker/daemon.json.bak-0.20
printf '%s\n' '{' \
  '  "log-driver": "journald",' \
  '  "live-restore": true,' \
  '  "default-address-pools": [ {"base":"10.0.0.0/8","size":24} ]' \
  '}' | sudo tee /etc/docker/daemon.json >/dev/null
sudo dockerd --validate --config-file /etc/docker/daemon.json && sudo systemctl restart docker
```

- The new driver applies to containers when they are **recreated**: redeploy each
  app in Coolify (staging app, staging API, landing) and restart the proxy (6.7).
  The old production app keeps its current logs until it is recreated; its old
  `*-json.log` files are the only thing left to purge, with the owner's OK.
- System logs: `/var/log/auth.log` and friends rotate weekly and keep 4 by default;
  set `daily` + `rotate 7` in `/etc/logrotate.d/rsyslog`.
- Traefik access logs stay **off** (the proxy configuration has no `--accesslog`).
- The API must not keep IPs longer either (0.17's audit log decides its retention).

Verify: `docker info --format '{{.LoggingDriver}}'` gives `journald`; after a week,
`journalctl --until '7 days ago' | head` is empty.

### 6.7 Traefik (Coolify proxy) on the current minor ☐

**State 2026-11-05:** the proxy compose uses the floating tag `traefik:v3.6` but
runs v3.6.13 (the image was never pulled again); the v3.6 line got its last
patch (v3.6.25) in August 2026, so it no longer receives fixes. Target:
**`traefik:v3.7.17`** (latest v3.7 patch on Docker Hub, 2026-11-05), pinned.

Steps (Coolify → Servers → localhost → Proxy):

1. Backup the proxy state (certificates live in `acme.json`):
   `sudo cp -a /data/coolify/proxy /data/coolify/proxy.bak-0.20`.
2. Check the dynamic config for BasicAuth with an empty user list (3.7.3):
   `sudo grep -ril basicauth /data/coolify/proxy/dynamic/ || echo none`.
3. In the proxy configuration change `image: 'traefik:v3.6'` to
   `image: 'traefik:v3.7.17'`, **Save**, then **Restart Proxy** (all sites on the
   server drop for a few seconds).
4. Verify: `docker exec coolify-proxy traefik version` shows 3.7.17; the
   Coolify panel loads on its domain; `https://next.valuo.ar`,
   `https://api-next.valuo.ar/health/ready`, `https://preview.valuo.ar` and the
   other projects' sites answer with valid certificates;
   `docker logs --since 5m coolify-proxy` has no errors.
5. Rollback: set the image back to `traefik:v3.6.13`, Save, Restart Proxy
   (or restore the backup folder if certificates were affected).

Notes from the migration guide:

Coolify reports v3.6.13; v3.7 is current and its patches fix several CVEs. The
v3.7 migration notes are mostly Kubernetes (Gateway API, CRDs, Ingress NGINX),
which Coolify does not use (Docker provider). The ones that could matter here:
BasicAuth needs a non-empty user list (3.7.3), StripPrefix answers 400 when the
result has `.`/`..` segments (3.7.3), a `Host` rule of `*` matches every request
(3.7.7), CONNECT and h2c upgrades are rejected (3.7.9 to 3.7.15), more than 500
header values give 431 (3.7.17). None applies to our apps.


### 6.8 SSH: keys only, no password, root only by key ☐ (last; keep a second session open)

Coolify manages the server over SSH **as root with its own key**, so root login
cannot be fully disabled; it stays key-only. On Ubuntu 24.04 the first value
wins across `sshd_config.d/*.conf` (read in name order), so the file must sort
before the cloud image's `60-cloudimg-settings.conf`:

```bash
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\nMaxAuthTries 3\nX11Forwarding no\n' \
  | sudo tee /etc/ssh/sshd_config.d/01-hardening.conf >/dev/null
sudo sshd -t && sudo systemctl reload ssh
```

Before closing the current session: open a **new** terminal and log in again;
check Coolify still reaches the server (Servers → localhost → Validate).
Rollback: `sudo rm /etc/ssh/sshd_config.d/01-hardening.conf && sudo systemctl reload ssh`.

### 6.9 Least-privilege database users ☐

Staging: done (`valuo_migrate` owns the schema, `valuo_app` has DML only; section 1).
Production gets the same split when the API v2 is first deployed there.

### 6.10 Secrets and rotation ☐

Secrets live only in Coolify's environment variables (never in the repo;
gitleaks in CI). Rotation, one at a time:

| Secret | How to rotate | Effect |
|---|---|---|
| `JWT_SECRET` / `JWT_REFRESH_SECRET` | new 32+ char random values, redeploy | everyone logs in again |
| DB passwords (`valuo_app`, `valuo_migrate`) | `ALTER ROLE ... PASSWORD`, update both URLs, redeploy | none if done together |
| `RESEND_API_KEY` | new key in Resend, update, redeploy, revoke the old one | none |
| `SOPORTE_API_KEY` | new key from the support service, update, redeploy | none |
| VAPID keys | avoid: new keys invalidate every push subscription | users enable push again |
| `API_DEPLOY_KEY` (ahorra_app CI) | new read-only deploy key on api_go_ahorro, update the secret, delete the old key | none |

Staging never receives copies of production data.

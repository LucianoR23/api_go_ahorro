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
| `*_FROM_EMAIL` (4) | sender on the verified domain |
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

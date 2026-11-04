# Changelog — v2 (`next` branch)

Entries reference item IDs from `AHORRA_V2_PLAN.md` (workspace repo).

## Unreleased

### 0.15c — Toolchain refresh & vulnerable dependencies
- Go 1.25 (EOL 2026-08-19) → `go 1.27.0` + `toolchain go1.27.3` in `go.mod` (Go auto-downloads the exact toolchain on every machine).
- Docker: build `golang:1.27.3-alpine3.24`, runtime `alpine:3.24.3` (3.20 was EOL).
- Dev DB: `supabase/postgres:17` → official `postgres:18.6-alpine3.24` (same major as staging/prod); new volume `ahorra_pgdata18` (old dev volume left untouched).
- Patched 13 reachable vulnerabilities (govulncheck now clean): pgx v5.12.0 (SQL injection via dollar-quoted placeholders), chi v5.5.0 (RealIP spoofing advisories), x/net v0.62.0 (HTTP/2 DoS), x/text v0.43.0, grpc v1.85.0, plus transitive bumps.
- Note: `middleware.RealIP` is deprecated in chi v5.5 and remains spoofable by design; replaced in 0.6 (`TRUSTED_PROXIES` + `ClientIPFromXFFTrustedProxies`).
- Verified: build, vet, govulncheck, 22 migrations up/down/up on Postgres 18, API boots with `/health/ready` 200, Docker image builds (44 MB).

### 0.15a — Hygiene, no behavior change
- Stop tracking per-machine `.claude/settings.local.json` (git-ignored).
- Document `GOOGLE_OAUTH_CLIENT_ID` in `.env.example` and README.
- Dev defaults match the front's dev port: `ALLOWED_ORIGINS` / `APP_BASE_URL` → `http://localhost:3005` (config defaults + `.env.example`); `.env.example` `DATABASE_URL` uses the compose port 5433.
- README: remove claims about non-existent tests and broken links; fix Docker/soft-delete/Go-version statements; commit convention now English Conventional Commits.

Known, left for later items: `staticcheck` reports 9× S1016 (style) and 4× U1000 unused funcs (`resolveCreditPeriod`, `textPtr`, `boolPtr`, `datePtr`) → linter config in 0.11, dead code in 0.15b.

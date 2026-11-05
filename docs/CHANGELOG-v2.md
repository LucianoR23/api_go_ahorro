# Changelog — v2 (`next` branch)

Entries reference item IDs from `AHORRA_V2_PLAN.md` (workspace repo).

## Unreleased

### 0.21c: households, invites, roles, admin, push and support tests
- `internal/households`: create (defaults: categories and the owner's split rule), list isolation, read/update/delete authorization (owner, member, outsider), the `X-Household-ID` middleware, leaving, removing members, ownership transfer, membership removed on account deletion, soft delete, and the superadmin list/get/restore/purge endpoints.
- Invites: email and link, public preview, register with an invite (joins, email verified, no verification mail), register or accept with someone else's invite, accept while logged in (seeds the split rule), single use, inviting a current member, owner-only management, list pending, revoke, resend (new token, old one dead), expiry, failed delivery still returns a shareable token.
- `internal/push`: VAPID key endpoint, subscribe/upsert/list/unsubscribe/delete, per-user isolation, validation, devices removed on account deletion, the superadmin test push, and real delivery against a local push server with generated VAPID keys (VAPID auth header, 410 removes the subscription).
- `internal/support`: the proxy against a local fake upstream: identity headers and API key (never the user's bearer token), query, JSON and multipart bodies, status/body/`Retry-After` passthrough, 503 when not configured, 502 when the upstream is down.
- `apitest`: `Invite`, `AddMember`, `MakeSuperadmin`, `Res.WantError`; headers passed in a request now replace the default `Content-Type` instead of being added next to it.
- Known bugs pinned (`KnownBug`):
  - 0.6: the push upsert hands an endpoint to whichever user subscribes it last; any URL is accepted as a push endpoint (blind SSRF).
  - 0.1: the support proxy passes an upstream 401 through, which the app reads as an expired session.
  - **New, not in the audit (assigned to 0.8):** an owner who soft-deleted their only household still cannot delete the account, because the owned-households count includes deleted households.
- Coverage: households 79.2%, push 77.6%, support 80.6%; module total 11.6% → 21%+.

### 0.21b: auth and users characterization tests
- `internal/auth/*_test.go` (77 subtests, real router + fresh database through `apitest`). Covered: register (normalization, cookie flags, verification email, Efectivo bootstrap, duplicate email, every validation rule, malformed or unknown JSON), login (anti-enumeration: same body for wrong password and unknown email; Google-only accounts), every way an access token can be missing or invalid (scheme, refresh-as-access, wrong secret, expired, wrong `typ`) on all authenticated auth routes, refresh rotation and cookie clearing, logout, Google sign-in (new user, repeat login, linking, `Usuario` fallback, soft-deleted account, bad or empty token), forgot/reset (single-use, the newer link invalidates the older one, expiry, weak password keeps the token, Google-only account sets a password), change password (also burns pending reset links), email verification and resend, `GET/PATCH/DELETE /me` (soft delete anonymizes and frees the email, owners must transfer first), and the login and forgot-password rate limits.
- `apitest`: `Res.Cookie`, `Res.RefreshCookieValue`, `WithRefreshCookie`, `Server.Login`, `Server.Refresh`.
- Known bugs pinned (`KnownBug`):
  - 0.5: logout, password reset, password change and account deletion leave existing refresh tokens valid; Google auto-links to an account whose email was never verified; changing the email keeps the verified flag.
  - 0.6: a client rotating `X-Forwarded-For` gets around the auth rate limits.
  - 0.1: decoder errors reach the client in English (`json: unknown field ...`).
- Coverage: `internal/auth` + `internal/users` at 77.3% from these tests; module total 4.9% → 11.6%.

### 0.21a — API test harness
- `internal/app`: the wiring (repos, services, handlers, router, background jobs) moved out of `main()` with no behavior change. `app.New(ctx, cfg, pool, logger, opts)` returns the real router (`Router()`) and `Start(ctx)` for the jobs (same order, stopped in reverse). `Options` swaps only external dependencies: email sender, Google verifier, FX HTTP client, auth rate limiter; zero values are production's. `cmd/api/main.go` keeps config, migrations, pool, server and signals.
- `internal/testutil/testdb`: Postgres 18 via testcontainers v0.44.0 (or `TEST_DATABASE_URL`), migrations applied once to a template, each test gets its own database cloned from it. Skips locally without Docker, fails in CI.
- `internal/testutil/apitest`: runs the real router on `httptest` against a fresh database; fake mailer (captures emails, extracts `?token=`), fake Google verifier (same error as the real one), auth rate limits off unless `WithRealRateLimits()`, outbound HTTP blocked. Factories: `Register` (optionally verified), `VerifyEmail`, `CreateHousehold`.
- The migrations integration test now runs on `testdb` (no env var); CI uses testcontainers instead of a Postgres service.
- `github.com/moby/go-archive` v0.2.0 → v0.3.0 (GO-2026-6253, reachable from the test harness through testcontainers).
- **Bug fixed (0.10):** with two instances starting at once, the one waiting for the lock could read the transient dirty flag of the other's in-flight migration before locking and abort startup. The pre-lock read is now only a log hint; the dirty check that counts is the one under the advisory lock. Caught by the concurrent integration test once it ran on testcontainers.

### 0.11 + 0.19 — CI, first unit tests, security scanning
- CI (`.github/workflows/ci.yml`, push to `next` + PRs into it): `go mod verify`, build, vet, golangci-lint v2.15.0, `sqlc diff` (sqlc 1.30.0, the version that generated the code), `go test -race` with a Postgres 18.6 service for the migrations integration test, coverage summary (reported, gate starts in 0.21), govulncheck v1.9.0, gitleaks on full history. Actions pinned by commit SHA.
- Dependabot weekly for gomod, Dockerfile, docker-compose and Actions, targeting `next`.
- `scripts/check.sh`: local mirror of CI. Tools run pinned through `go run pkg@version` (no `tool` directives: golangci-lint advises against them, and this keeps `go.mod` free of tool deps).
- `gofmt` applied to the whole module (separate formatting-only commit).
- `.golangci.yml`: standard linters + gosec + gofmt. Triage of the 85 findings on existing code, each exclusion scoped to existing files and tied to its plan item:
  - REAL BUG (low): `offset` query params in expenses, incomes, settlements and insights handlers only check `n >= 0`; values above 2^31-1 wrap to a negative int32 and the query fails (500). To pin as KnownBug in 0.21.
  - `middleware.RealIP` deprecated/spoofable → 0.6. S1016/QF1008 style and 4 unused funcs → 0.15b. int→int32 conversions bounded by validation/DB checks → reviewed in 0.21.
  - False positives silenced inline with a reason: `.env` file read (G304), refresh cookie Secure only with `ENV=prod` (G124), detached push delivery goroutine (G118). Fake credentials in test URLs (G101) excluded for `_test.go`. Unchecked `Close`/print errors via the `std-error-handling` preset.
- `internal/testutil`: `KnownBug` (passes while a known bug reproduces, fails once fixed), `Location` (Buenos Aires TZ), `FixedClock`.
- Clock seam: `now func() time.Time` in the incomes, insights, recurring expenses and reports workers (default `time.Now`). No other behavior change.
- Characterization tests: `clampDay`, `resolveForClosingMonth`, `buildShares`, `buildInstallments` (expenses); `Convert` (fxrates, warm cache); `recurrenceMatches` + `clampDay` (incomes and recurringexpenses, same table for both copies); `alertText` (insights); worker schedules (`nextTick`, defaults).
- Known bugs pinned (`KnownBug`, fixed by 0.7): `buildShares(0.04, 6 equal members)` gives the last member -0.01; `buildInstallments(0.50, 60 installments)` makes the last installment -0.09.
- Noted, not changed: insight copy formats money as `$1234.50` (not es-AR) and shows the raw goal type (`category_limit`) to users.
- Baseline: total coverage 3.5% (before 0.21). gitleaks: no leaks in any repo's history.

### 0.10 — Migrations on deploy
- New dependency: `github.com/golang-migrate/migrate/v4` v4.20.1 (pgx/v5 driver + `iofs` source). govulncheck: 0 reachable.
- Migrations are embedded in the binary (`migrations/embed.go`); the Dockerfile no longer copies the folder.
- `internal/dbmigrate.Up`: applies pending migrations under a Postgres advisory lock (2 min wait), refuses a dirty database, never logs the password.
- `MIGRATE_ON_START` (default `false`) + `MIGRATE_DATABASE_URL` (required when on, no fallback to `DATABASE_URL`). On failure the API exits 1 before serving, so Coolify keeps the previous container.
- Deploys use two roles: `valuo_migrate` (schema owner, DDL) for migrations and `valuo_app` (DML only) at runtime. New `DEPLOY.md` documents roles, env vars and the dirty-DB procedure.
- Tests: config parsing (`internal/config`), URL/redaction/embedding units, and an integration test (`DBMIGRATE_TEST_DATABASE_URL`, skipped otherwise; testcontainers in 0.11) covering concurrent startup (exactly one instance migrates), idempotent re-run and dirty refusal. Verified against Postgres 18.6 with staging-like roles: app role can read/write but not `CREATE`/`DROP`; Docker image migrates an empty DB and registration returns 201.
- Note: govulncheck reports GO-2026-5932 (`x/crypto/openpgp`, unmaintained) in a required module; not imported by this code. Only fixed in an untagged pseudo-version; revisit when x/crypto tags a release.

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

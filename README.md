# Valuo API

Backend en Go de Valuo: app de gestión de gastos multi-hogar, multi-moneda (ARS / USD / EUR).

## Stack

- **Go** (versión fijada en `go.mod`) con [chi](https://github.com/go-chi/chi) como router HTTP
- **PostgreSQL 15+** (prod: Postgres en Coolify; dev: `docker-compose.yml`)
- **pgx/v5** + [sqlc](https://sqlc.dev) para queries tipadas
- **golang-migrate** para migraciones versionadas
- **JWT** (access 15min + refresh 7 días en HttpOnly cookie)
- **Resend** para emails transaccionales (reportes mensuales)

Sin Redis ni CLI de admin. Todo se configura vía env vars y se despliega como un binario único. Para desarrollo hay un `docker-compose.yml` con Postgres.

## Estructura

```
cmd/api/              entrypoint del server (composition root)
internal/
  auth/               login, register, refresh, JWT, middleware
  balances/           cálculo de deudas entre miembros (read-only)
  categories/         CRUD de categorías por hogar
  categorize/         category suggestion: history, then AI (plan 8.4)
  expenseparse/       natural-language and voice entry: rules, then AI, 24 h cache (plans 3.1, 3.2)
  config/             carga de .env + validación
  creditperiods/      closing_day / due_day overrides por mes y tarjeta
  db/                 pool pgx + helpers de pgtype.Numeric
  db/sqlc/            código generado por sqlc (no editar)
  db/queries/         .sql fuente para sqlc generate
  domain/             structs de dominio (User, Expense, Goal, ...)
  expenses/           core — CRUD gastos + installments + shares
  fxrates/            tasas ARS/USD/EUR (refresh cada 15min vía jobs)
  goals/              metas presupuestarias (category_limit / total_limit / savings)
  households/         hogares + miembros + middleware de tenancy
  httpx/              helpers de JSON + logger dev + banner
  incomes/            ingresos + recurring_incomes
  insights/           generador de insights diarios/semanales
  jobs/               jobs en segundo plano sobre River (cola en Postgres)
  paymethods/         bancos, tarjetas, métodos de pago (scope user)
  recurringexpenses/  plantillas de gastos fijos + generación
  reports/            monthly report + trends + AI export + email mensual
  settlements/        pagos entre miembros (reducen deuda)
  splitrules/         pesos default para dividir gastos compartidos
  users/              repo de users
migrations/           golang-migrate up/down
```

## Quick start (desarrollo)

### Prerrequisitos

- Go (la versión de `go.mod`)
- Docker: `docker compose up -d` levanta Postgres en `localhost:5433`.
- [golang-migrate](https://github.com/golang-migrate/migrate) instalado:
  ```
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```

### Setup

```bash
# 1. Copiar env y ajustar valores
cp .env.example .env

# 2. Generar el secreto JWT (mínimo 32 chars)
openssl rand -hex 32   # → pegar en JWT_SECRET

# 3. Migrar la DB
migrate -path migrations -database "$DATABASE_URL" up

# 4. Correr
go run ./cmd/api
```

El server arranca en `:8080`. `GET /health/live` debería devolver 200.

### Regenerar código sqlc

Si tocás algo en `internal/db/queries/*.sql`:

```bash
sqlc generate
```

El output va a `internal/db/sqlc/`. Está versionado en el repo (no se genera en build).

## Variables de entorno

Ver [`.env.example`](./.env.example). Resumen:

| Variable | Requerida | Descripción |
|---|---|---|
| `PORT` | no (8080) | Puerto HTTP |
| `ENV` | no (dev) | `dev` o `prod`. En `prod`: logs JSON + cookie refresh con Secure=true |
| `LOG_LEVEL` | no (info) | `debug` / `info` / `warn` / `error` |
| `DATABASE_URL` | sí | `postgres://user:pw@host:5432/db?sslmode=...` (en deploys: rol solo DML) |
| `MIGRATE_ON_START` | no (false) | `true` = aplica las migraciones embebidas al arrancar, con advisory lock; si fallan o la DB está dirty, el proceso sale con 1 |
| `MIGRATE_DATABASE_URL` | si `MIGRATE_ON_START=true` | URL del rol dueño del schema (DDL). Sin fallback a `DATABASE_URL` |
| `JWT_SECRET` | sí | mínimo 32 chars |
| `ALLOWED_ORIGINS` | no (localhost:3005) | CORS, separado por coma |
| `RESEND_API_KEY` | no | si falta, el job del email mensual loguea skip y no envía |
| `REPORT_FROM_EMAIL` | no | remitente de reportes (dominio verificado en Resend) |
| `GOOGLE_OAUTH_CLIENT_ID` | no | OAuth web client ID; audiencia de los ID tokens de Google. Sin ella `/auth/google` responde 500 |
| `APP_BASE_URL` | no (localhost:3005) | URL pública del front para los links de los emails |
| `GLITCHTIP_DSN` | no | DSN del proyecto en GlitchTip. Vacía = no reporta errores |
| `GLITCHTIP_ENVIRONMENT` | no (`ENV`) | `staging` / `production` en el dashboard de GlitchTip |
| `COINGECKO_API_KEY` | no | key demo de CoinGecko (gratis). Precio de bitcoin y ether para "Ver en", como mucho una vez por hora. Vacía = sin BTC/ETH |
| `AI_ENABLED` | no (`false`) | prende la IA (plan 8.1). Claude para todo salvo el audio, que va a Gemini |
| `ANTHROPIC_API_KEY` / `GEMINI_API_KEY` | no | keys de los proveedores de IA. Sin ellas las tareas que los usan fallan y la gente carga a mano |
| `AI_DAILY_BUDGET_USD` / `AI_USER_DAILY_USD` / `AI_USER_PER_MINUTE` / `AI_USER_PER_DAY` | no (3 / 0.05 / 10 / 100) | topes de IA: gasto diario total y por persona, pedidos por minuto y por día |
| `AI_ROUTES` / `AI_PRICES` / `AI_DISABLED_TASKS` | no | modelo por tarea, precios por millón de tokens y tareas apagadas. Defaults en `internal/config/env.go` |

## Arquitectura

**Capas por dominio** (ej: `expenses/`):

- `repository.go` — wraps del código sqlc, mapea row → domain
- `service.go` — lógica de negocio (validación, FX, shares, cuotas)
- `handler.go` — HTTP, DTOs, parsing de query params

**Transacciones**: escrituras multi-tabla usan `pgx.BeginFunc`. Ejemplos: crear un gasto con N cuotas y sus shares es una sola tx; crear un hogar semilla las 7 categorías default y el weight=1.0 del owner en la misma tx.

**Errores**: todos los errores del API van por `httpx.WriteError`, que serializa a `{code, message, field?}` con status HTTP mapeado desde el tipo de error (`domain.ErrNotFound` → 404, `ValidationError` → 422, etc.). El frontend mapea por `code`, nunca parsea `message`.

**Jobs en segundo plano** (`internal/jobs`): corren sobre [River](https://riverqueue.com), una cola de jobs en Postgres (tablas `river_*`, migración 000029). Cada réplica levanta un cliente; los jobs periódicos los encola solo el líder, y cada job corre una vez aunque haya N instancias. Los que fallan se reintentan con backoff. Horarios en la zona de la app:

| Job | Cadencia | Qué hace |
|---|---|---|
| `fx_refresh` | 15 min y al arrancar | rates from DolarAPI (every dollar, USDT at the crypto dollar, and the official euro, real, Chilean and Uruguayan peso), Bluelytics as fallback and for the blue euro, CoinGecko for bitcoin and ether at most once an hour (with `COINGECKO_API_KEY`) |
| `recurring_incomes` | diario 00:30 | genera ingresos desde plantillas |
| `recurring_expenses` | diario 00:30 | genera gastos desde plantillas |
| `insights_daily` | diario 01:00 | alerts, card and installment reminders, weekly review (Sundays) and monthly review (the 1st); summaries rewritten by AI when on (plan 8.2) |
| `monthly_report` | día 1 08:00 | envía el resumen del mes anterior por Resend |
| `audit_retention` | diario 03:30 y al arrancar | borra IPs de más de 7 días y eventos de más de 12 meses |
| `waitlist_cleanup` | diario 04:00 y al arrancar | borra altas sin confirmar de más de 30 días |
| `cpi_refresh` | diario 05:00 y al arrancar | IPC del INDEC (CSV oficial; la API de Series de datos.gob.ar como respaldo) para los reportes en pesos de hoy |

Los jobs diarios y el mensual son únicos por día (o mes). Al arrancar, la API encola los días que se perdieron mientras estuvo caída, desde el último job completado: hasta 31 días para los recurrentes, 7 para insights y solo el último mes para el email. Los jobs completados se guardan 62 días; sin historial no se recupera nada. Los errores de un hogar se loguean y no frenan al siguiente.

## Endpoints

La referencia de endpoints está en los handlers (`internal/*/handler.go`); un resumen vive en la auditoría del workspace (`ahorra-current-state.md` §5).

## Deploy

Se despliega en Coolify desde el `Dockerfile`. Pasos, roles de DB y variables: [`DEPLOY.md`](./DEPLOY.md). Las migraciones van embebidas en el binario y se aplican al arrancar con `MIGRATE_ON_START=true`.

La imagen Docker es multi-stage (alpine build + alpine runtime) y pesa ~45MB. Expose `:8080`. Healthcheck incluido en `/health/live`.

```bash
docker build -t valuo-api .
docker run --rm -p 8080:8080 --env-file .env valuo-api
```

## Convenciones

- **Dates**: `YYYY-MM-DD` en request/response. Timestamps en ISO-8601 UTC.
- **Money**: `float64` con 2 decimales. Los campos `amount_base` ya están convertidos a la moneda base del hogar, que siempre es ARS (plan 5.5; denormalizado). Los reportes aceptan `currency` en USD, EUR, BRL, CLP, UYU, USDT, BTC o ETH solo para mostrar (plan 5.6).
- **IDs**: UUIDs v4 en todas las tablas.
- **Soft deletes**: `users` y `households` usan `deleted_at`; el resto se borra físicamente.
- **Paginación**: offset/limit simple. Migrar a keyset cuando crezca el volumen.

## Testing

```bash
./scripts/check.sh     # lo mismo que corre CI (Git Bash): build, vet, lint, sqlc diff, tests, govulncheck, gitleaks
go test ./...          # solo tests
go test -race ./...    # con detector de races (no soportado en Windows ARM64; CI lo corre)
```

- CI: `.github/workflows/ci.yml` en cada push a `next` y en cada PR hacia `next`. Un ítem no está terminado con CI en rojo.
- Cobertura: `scripts/coverage.sh` mide la cobertura entre paquetes de `internal/` (sin sqlc ni testutil) y falla si baja del piso de `.coverage-floor` o si el piso baja respecto de `next`. Línea base y huecos conocidos: `docs/testing/coverage-baseline.md`. Si un PR sube la cobertura, subí el piso en el mismo PR.
- Lint: golangci-lint v2 con `.golangci.yml` (gofmt, staticcheck, gosec, errcheck...). Las exclusiones existentes apuntan al ítem del plan que las elimina; no se agregan exclusiones para código nuevo.
- Bugs conocidos de la auditoría: `testutil.KnownBug(t, "<ítem>", ...)` pasa mientras el bug se reproduce y falla cuando se arregla, para que el ítem que lo arregla lo convierta en test normal.
- Tiempo determinístico: los jobs tienen un clock inyectable (`now`); los tests usan `testutil.Location` (`America/Argentina/Buenos_Aires`) y `testutil.FixedClock`.
- Tests de integración: `internal/testutil/testdb` levanta Postgres 18 con testcontainers (necesita Docker) y le da a cada test su propia DB migrada, clonada de un template. Con `TEST_DATABASE_URL` (URL de superusuario) usa ese server en vez del contenedor. Sin Docker ni esa variable, se saltean en `go test` local; en CI y en `./scripts/check.sh` (`TESTDB_REQUIRED=1`) fallan, así un check verde siempre corrió los tests de integración. `check.sh` levanta un único Postgres efímero con `docker run` y lo comparte vía `TEST_DATABASE_URL` (en Docker Desktop para Windows, que cada paquete levante su propio contenedor con testcontainers falla de forma intermitente).
- Tests HTTP: `internal/testutil/apitest` corre el router real (`internal/app`) con fakes de email, Google y rate limit; las llamadas HTTP salientes están bloqueadas y bcrypt usa el costo mínimo (producción sigue en 12). Se usa desde paquetes de test externos (`package foo_test`).

## Contribuir

- No correr `sqlc generate` sin avisar — el código generado está versionado.
- Una migración = un cambio atómico. Up + Down reversibles.
- Commits en inglés con Conventional Commits y el ID del ítem (`fix(expenses): ... [0.1]`).

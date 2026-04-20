# Ahorra API

Backend en Go para Ahorra — app de gestión de gastos multi-hogar, multi-moneda (ARS / USD / EUR).

## Stack

- **Go 1.26** con [chi](https://github.com/go-chi/chi) como router HTTP
- **PostgreSQL 15+** (corre contra Supabase o Postgres vanilla)
- **pgx/v5** + [sqlc](https://sqlc.dev) para queries tipadas
- **golang-migrate** para migraciones versionadas
- **JWT** (access 15min + refresh 7 días en HttpOnly cookie)
- **Resend** para emails transaccionales (reportes mensuales)

Sin Docker local, sin Redis, sin CLI de admin. Todo se configura vía env vars y se despliega como un binario único.

## Estructura

```
cmd/api/              entrypoint del server (composition root)
internal/
  auth/               login, register, refresh, JWT, middleware
  balances/           cálculo de deudas entre miembros (read-only)
  categories/         CRUD de categorías por hogar
  config/             carga de .env + validación
  creditperiods/      closing_day / due_day overrides por mes y tarjeta
  db/                 pool pgx + helpers de pgtype.Numeric
  db/sqlc/            código generado por sqlc (no editar)
  db/queries/         .sql fuente para sqlc generate
  domain/             structs de dominio (User, Expense, Goal, ...)
  expenses/           core — CRUD gastos + installments + shares
  fxrates/            tasas ARS/USD/EUR + worker de refresh cada 15min
  goals/              metas presupuestarias (category_limit / total_limit / savings)
  households/         hogares + miembros + middleware de tenancy
  httpx/              helpers de JSON + logger dev + banner
  incomes/            ingresos + recurring_incomes
  insights/           generador de insights diarios/semanales + worker
  paymethods/         bancos, tarjetas, métodos de pago (scope user)
  recurringexpenses/  plantillas de gastos fijos + worker de generación
  reports/            monthly report + trends + AI export + worker email
  settlements/        pagos entre miembros (reducen deuda)
  splitrules/         pesos default para dividir gastos compartidos
  users/              repo de users
migrations/           golang-migrate up/down
```

## Quick start (desarrollo)

### Prerrequisitos

- Go 1.26+
- Postgres corriendo localmente (puerto 5432) o una URL de Supabase/cloud.
- [golang-migrate](https://github.com/golang-migrate/migrate) instalado:
  ```
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
  ```

### Setup

```bash
# 1. Copiar env y ajustar valores
cp .env.example .env

# 2. Generar secretos JWT (mínimo 32 chars cada uno)
openssl rand -hex 32   # → pegar en JWT_SECRET
openssl rand -hex 32   # → pegar en JWT_REFRESH_SECRET

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
| `DATABASE_URL` | sí | `postgres://user:pw@host:5432/db?sslmode=...` |
| `JWT_SECRET` | sí | mínimo 32 chars |
| `JWT_REFRESH_SECRET` | sí | mínimo 32 chars, distinto del anterior |
| `ALLOWED_ORIGINS` | no (localhost:3000) | CORS, separado por coma |
| `RESEND_API_KEY` | no | si falta, el worker de reports loguea skip y no envía |
| `REPORT_FROM_EMAIL` | no | remitente de reportes (dominio verificado en Resend) |

## Arquitectura

**Capas por dominio** (ej: `expenses/`):

- `repository.go` — wraps del código sqlc, mapea row → domain
- `service.go` — lógica de negocio (validación, FX, shares, cuotas)
- `handler.go` — HTTP, DTOs, parsing de query params
- `*_test.go` — tests unitarios del service con mocks

**Transacciones**: escrituras multi-tabla usan `pgx.BeginFunc`. Ejemplos: crear un gasto con N cuotas y sus shares es una sola tx; crear un hogar semilla las 7 categorías default y el weight=1.0 del owner en la misma tx.

**Errores**: todos los errores del API van por `httpx.WriteError`, que serializa a `{code, message, field?}` con status HTTP mapeado desde el tipo de error (`domain.ErrNotFound` → 404, `ValidationError` → 422, etc.). El frontend mapea por `code`, nunca parsea `message`.

**Workers**: goroutines que corren cada N tiempo, con catch-up al arrancar:

| Worker | Cadencia | Qué hace |
|---|---|---|
| fxrates | 15 min | refresh tasas ARS/USD/EUR desde bluelytics |
| incomes recurrentes | diario 00:30 | genera ingresos desde plantillas |
| expenses recurrentes | diario 00:30 | genera gastos desde plantillas |
| insights | diario 01:00 | arma daily_summary + alerts + weekly (domingos) |
| reports email | día 1 mes 08:00 | envía resumen del mes anterior por Resend |

Todos catchean errores per-hogar para no trabar al siguiente.

## Endpoints

Ver [`../API_REFERENCE.md`](../API_REFERENCE.md) para la referencia completa con request/response de cada endpoint.

## Deploy

Ver [`../COOLIFY_DEPLOY.md`](../COOLIFY_DEPLOY.md) para la guía paso a paso de despliegue en Coolify.

La imagen Docker es multi-stage (alpine build + alpine runtime) y pesa ~20MB. Expose `:8080`. Healthcheck incluido en `/health/live`.

```bash
docker build -t ahorra-api .
docker run --rm -p 8080:8080 --env-file .env ahorra-api
```

## Convenciones

- **Dates**: `YYYY-MM-DD` en request/response. Timestamps en ISO-8601 UTC.
- **Money**: `float64` con 2 decimales. Los campos `amount_base` ya están convertidos a la moneda base del hogar (denormalizado).
- **IDs**: UUIDs v4 en todas las tablas.
- **Soft deletes**: no se usan. Borrar es borrar.
- **Paginación**: offset/limit simple. Migrar a keyset cuando crezca el volumen.

## Testing

```bash
go test ./...          # todos los packages
go test -race ./...    # con detector de races (workers)
go vet ./...
```

Los tests de service usan mocks de repo; los de repo tocan una DB real (configurable por `TEST_DATABASE_URL`).

## Contribuir

- No correr `sqlc generate` sin avisar — el código generado está versionado.
- Una migración = un cambio atómico. Up + Down reversibles.
- Commits en español, concisos, con contexto en el body (`git commit -m -m`).

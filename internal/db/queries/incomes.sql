-- ===================== incomes =====================

-- name: CreateIncome :one
INSERT INTO incomes (
    household_id, received_by, payment_method_id,
    amount, currency, amount_base, base_currency, rate_used, rate_at,
    source, description, received_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetIncomeByID :one
SELECT * FROM incomes WHERE id = $1;

-- name: ListIncomesByHousehold :many
-- Filtros opcionales: receivedBy, paymentMethodId, source, desde/hasta.
SELECT *
FROM incomes
WHERE household_id = $1
  AND (sqlc.narg(received_by)::uuid IS NULL OR received_by = sqlc.narg(received_by)::uuid)
  AND (sqlc.narg(payment_method_id)::uuid IS NULL OR payment_method_id = sqlc.narg(payment_method_id)::uuid)
  AND (sqlc.narg(source)::text IS NULL OR source = sqlc.narg(source)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR received_at >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR received_at <= sqlc.narg(to_date)::date)
ORDER BY received_at DESC, created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountIncomesByHousehold :one
SELECT COUNT(*)::bigint
FROM incomes
WHERE household_id = $1
  AND (sqlc.narg(received_by)::uuid IS NULL OR received_by = sqlc.narg(received_by)::uuid)
  AND (sqlc.narg(payment_method_id)::uuid IS NULL OR payment_method_id = sqlc.narg(payment_method_id)::uuid)
  AND (sqlc.narg(source)::text IS NULL OR source = sqlc.narg(source)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR received_at >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR received_at <= sqlc.narg(to_date)::date);

-- name: UpdateIncome :one
-- Solo editamos lo "meta": source/description/received_at. El amount/currency
-- quedan fijos — si está mal, borrar y recrear (misma regla que expenses).
UPDATE incomes
SET source      = $2,
    description = $3,
    received_at = $4
WHERE id = $1
RETURNING *;

-- name: DeleteIncome :exec
DELETE FROM incomes WHERE id = $1;

-- name: SumIncomesByHouseholdInRange :one
-- Para /totals/income: suma amount_base de todos los ingresos del hogar
-- entre received_at >= from y received_at <= to. COALESCE a 0 si no hay
-- filas (evita NULL en el tipo Numeric).
SELECT COALESCE(SUM(amount_base), 0)::numeric AS total_base
FROM incomes
WHERE household_id = $1
  AND received_at >= $2::date
  AND received_at <= $3::date;

-- ===================== recurring_incomes =====================

-- name: CreateRecurringIncome :one
INSERT INTO recurring_incomes (
    household_id, received_by, payment_method_id,
    amount, currency, description, source,
    frequency, day_of_month, day_of_week, month_of_year,
    is_active, starts_at, ends_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
RETURNING *;

-- name: GetRecurringIncomeByID :one
SELECT * FROM recurring_incomes WHERE id = $1;

-- name: ListRecurringIncomesByHousehold :many
SELECT * FROM recurring_incomes
WHERE household_id = $1
ORDER BY is_active DESC, created_at DESC;

-- name: ListActiveRecurringIncomes :many
-- Lo usa el worker cada tick: todas las plantillas activas cuyo rango
-- (starts_at/ends_at) cubre la fecha target. El filtro fino de "toca hoy
-- según frequency/day_of_*" se resuelve en Go para no complicar la query.
SELECT * FROM recurring_incomes
WHERE is_active = true
  AND starts_at <= $1::date
  AND (ends_at IS NULL OR ends_at >= $1::date);

-- name: UpdateRecurringIncome :one
UPDATE recurring_incomes
SET amount        = $2,
    currency      = $3,
    description   = $4,
    source        = $5,
    frequency     = $6,
    day_of_month  = $7,
    day_of_week   = $8,
    month_of_year = $9,
    ends_at       = $10,
    payment_method_id = $11
WHERE id = $1
RETURNING *;

-- name: SetRecurringIncomeActive :exec
UPDATE recurring_incomes SET is_active = $2 WHERE id = $1;

-- name: MarkRecurringIncomeGenerated :exec
-- Lo llama el worker después de crear el income real. Marca last_generated
-- para que el próximo tick del mismo día no vuelva a crear.
UPDATE recurring_incomes SET last_generated = $2::date WHERE id = $1;

-- name: DeleteRecurringIncome :exec
DELETE FROM recurring_incomes WHERE id = $1;

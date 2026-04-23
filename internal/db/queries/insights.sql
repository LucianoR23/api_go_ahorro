-- ===================== daily_insights =====================

-- name: CreateDailyInsight :one
-- ON CONFLICT DO NOTHING: si ya existe un insight del mismo (household, user,
-- date, type) — o del mismo (hh, user, type, ref_id) cuando ref_id no es NULL
-- — lo dejamos intacto. El RETURNING puede ser vacío: el caller lo interpreta
-- como "ya existía, skip". Sin target en ON CONFLICT: dispara contra cualquier
-- UNIQUE (los dos índices parciales del migration 000020).
INSERT INTO daily_insights (
    household_id, user_id, insight_date, insight_type,
    title, body, severity, metadata, ref_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetDailyInsightByID :one
SELECT * FROM daily_insights WHERE id = $1;

-- name: ListDailyInsightsByHousehold :many
-- Filtros opcionales: user_id (null = insights del hogar; uuid = de ese user),
-- unread_only, rango de fechas, tipo.
SELECT *
FROM daily_insights
WHERE household_id = $1
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(only_unread)::boolean IS NULL OR is_read = NOT sqlc.narg(only_unread)::boolean)
  AND (sqlc.narg(from_date)::date IS NULL OR insight_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR insight_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(insight_type)::text IS NULL OR insight_type = sqlc.narg(insight_type)::text)
ORDER BY insight_date DESC, created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountUnreadInsightsByHousehold :one
SELECT COUNT(*)::bigint
FROM daily_insights
WHERE household_id = $1
  AND is_read = false
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid);

-- name: MarkDailyInsightRead :exec
UPDATE daily_insights SET is_read = true WHERE id = $1;

-- name: MarkAllInsightsReadByHousehold :exec
UPDATE daily_insights
SET is_read = true
WHERE household_id = $1
  AND is_read = false
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid);

-- name: DeleteDailyInsight :exec
DELETE FROM daily_insights WHERE id = $1;

-- ===================== agregaciones para generación =====================

-- name: SumExpensesSpentAtRange :one
-- Gasto real (spent_at) del hogar en un rango. Base currency.
SELECT COALESCE(SUM(amount_base), 0)::numeric AS total_base
FROM expenses
WHERE household_id = $1
  AND spent_at >= $2::date
  AND spent_at <= $3::date;

-- name: CountExpensesSpentAtRange :one
-- Cuántas transacciones hubo en el rango. Distinct categorías también.
SELECT
    COUNT(*)::bigint AS total_count,
    COUNT(DISTINCT category_id)::bigint AS distinct_categories
FROM expenses
WHERE household_id = $1
  AND spent_at >= $2::date
  AND spent_at <= $3::date;

-- name: SumInstallmentsDueInRange :one
-- Cuotas pendientes de pago cuyo due_date cae en el rango. Solo cuenta lo
-- que efectivamente "viene a cobrar": is_paid = false. Los gastos en efectivo/
-- débito nacen con is_paid = true y quedan excluidos. Las cuotas de crédito
-- futuras tienen due_date; si por algún motivo falta, caemos a billing_date.
SELECT COALESCE(SUM(i.installment_amount_base), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE e.household_id = $1
  AND i.is_paid = false
  AND COALESCE(i.due_date, i.billing_date) >= $2::date
  AND COALESCE(i.due_date, i.billing_date) <= $3::date;

-- name: TopCategorySpentAtRange :one
-- Categoría con más gasto en el rango (spent_at). Devuelve también el total.
-- Si no hay gastos, no devuelve filas (el caller lo maneja como "sin datos").
SELECT e.category_id, COALESCE(SUM(e.amount_base), 0)::numeric AS total_base
FROM expenses e
WHERE e.household_id = $1
  AND e.spent_at >= $2::date
  AND e.spent_at <= $3::date
  AND e.category_id IS NOT NULL
GROUP BY e.category_id
ORDER BY total_base DESC
LIMIT 1;

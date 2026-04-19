-- ===================== recurring_expenses =====================

-- name: CreateRecurringExpense :one
INSERT INTO recurring_expenses (
    household_id, created_by, category_id, payment_method_id,
    amount, currency, description, installments, is_shared,
    frequency, day_of_month, day_of_week, month_of_year,
    is_active, starts_at, ends_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING *;

-- name: GetRecurringExpenseByID :one
SELECT * FROM recurring_expenses WHERE id = $1;

-- name: ListRecurringExpensesByHousehold :many
SELECT * FROM recurring_expenses
WHERE household_id = $1
ORDER BY is_active DESC, created_at DESC;

-- name: ListActiveRecurringExpenses :many
-- Lo usa el worker: plantillas activas cuyo rango cubre `date`. Filtro fino
-- por frequency/day_of_* se resuelve en Go (mismo patrón que recurring_incomes).
SELECT * FROM recurring_expenses
WHERE is_active = true
  AND starts_at <= $1::date
  AND (ends_at IS NULL OR ends_at >= $1::date);

-- name: UpdateRecurringExpense :one
UPDATE recurring_expenses
SET amount            = $2,
    currency          = $3,
    description       = $4,
    installments      = $5,
    is_shared         = $6,
    frequency         = $7,
    day_of_month      = $8,
    day_of_week       = $9,
    month_of_year     = $10,
    ends_at           = $11,
    category_id       = $12,
    payment_method_id = $13
WHERE id = $1
RETURNING *;

-- name: SetRecurringExpenseActive :exec
UPDATE recurring_expenses SET is_active = $2 WHERE id = $1;

-- name: MarkRecurringExpenseGenerated :exec
UPDATE recurring_expenses SET last_generated = $2::date WHERE id = $1;

-- name: DeleteRecurringExpense :exec
DELETE FROM recurring_expenses WHERE id = $1;

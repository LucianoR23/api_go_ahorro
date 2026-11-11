-- ===================== recurring_expenses =====================

-- name: CreateRecurringExpense :one
INSERT INTO recurring_expenses (
    household_id, created_by, category_id, payment_method_id,
    amount, currency, description, installments, is_shared,
    frequency, day_of_month, day_of_week, month_of_year,
    is_active, starts_at, ends_at,
    amount_is_variable, alert_threshold_pct
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
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
SET amount              = $2,
    currency            = $3,
    description         = $4,
    installments        = $5,
    is_shared           = $6,
    frequency           = $7,
    day_of_month        = $8,
    day_of_week         = $9,
    month_of_year       = $10,
    ends_at             = $11,
    category_id         = $12,
    payment_method_id   = $13,
    amount_is_variable  = $14,
    alert_threshold_pct = $15
WHERE id = $1
RETURNING *;

-- name: UpdateRecurringExpenseLastAmount :exec
-- Cache: el service lo llama al confirmar un draft para que la próxima vez
-- que se liste la serie aparezca el monto real más reciente sin recomputar.
UPDATE recurring_expenses
SET last_amount = $2,
    last_confirmed_at = NOW()
WHERE id = $1;

-- name: SetRecurringExpenseActive :exec
UPDATE recurring_expenses SET is_active = $2 WHERE id = $1;

-- name: MarkRecurringExpenseGenerated :exec
UPDATE recurring_expenses SET last_generated = $2::date WHERE id = $1;

-- name: DeleteRecurringExpense :exec
DELETE FROM recurring_expenses WHERE id = $1;

-- ===================== suggestions (plan 7.3) =====================

-- name: ListRecurringSuggestionCandidates :many
-- Manual one-payment expenses of a space in a window, oldest first. The
-- grouping into "repeats every month" happens in Go.
SELECT description, currency, amount, spent_at, category_id, payment_method_id
FROM expenses
WHERE household_id = $1
  AND recurring_expense_id IS NULL
  AND status = 'confirmed'
  AND installments = 1
  AND spent_at >= sqlc.arg(from_date)::date
  AND spent_at < sqlc.arg(to_date)::date
ORDER BY spent_at, created_at;

-- name: ListRecurringSuggestionDismissals :many
SELECT key FROM recurring_suggestion_dismissals WHERE household_id = $1;

-- name: DismissRecurringSuggestion :exec
INSERT INTO recurring_suggestion_dismissals (household_id, key, dismissed_by)
VALUES ($1, $2, $3)
ON CONFLICT (household_id, key) DO NOTHING;

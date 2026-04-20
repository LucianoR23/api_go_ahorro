-- name: CreateExpense :one
INSERT INTO expenses (
    household_id, created_by, category_id, payment_method_id,
    amount, currency, amount_base, base_currency, rate_used, rate_at,
    description, spent_at, installments, is_shared, recurring_expense_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
RETURNING *;

-- name: GetExpenseByID :one
SELECT * FROM expenses WHERE id = $1;

-- name: ListExpensesByHousehold :many
-- Filtros opcionales: categoryId, paymentMethodId, desde/hasta (fechas).
-- Paginación por offset/limit simple. Cuando el volumen crezca, migrar a keyset.
SELECT *
FROM expenses
WHERE household_id = $1
  AND (sqlc.narg(category_id)::uuid IS NULL OR category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(payment_method_id)::uuid IS NULL OR payment_method_id = sqlc.narg(payment_method_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR spent_at >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR spent_at <= sqlc.narg(to_date)::date)
ORDER BY spent_at DESC, created_at DESC
LIMIT $2 OFFSET $3;

-- name: CountExpensesByHousehold :one
SELECT COUNT(*)::bigint
FROM expenses
WHERE household_id = $1
  AND (sqlc.narg(category_id)::uuid IS NULL OR category_id = sqlc.narg(category_id)::uuid)
  AND (sqlc.narg(payment_method_id)::uuid IS NULL OR payment_method_id = sqlc.narg(payment_method_id)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR spent_at >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR spent_at <= sqlc.narg(to_date)::date);

-- name: UpdateExpense :one
-- Solo campos editables: description, spent_at, category_id.
-- amount/currency/installments NO se editan (borrar y recrear).
UPDATE expenses
SET description = $2,
    spent_at = $3,
    category_id = $4,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteExpense :exec
DELETE FROM expenses WHERE id = $1;

-- ===== installments =====

-- name: CreateInstallment :one
INSERT INTO expense_installments (
    expense_id, installment_number,
    installment_amount, installment_amount_base,
    billing_date, due_date, is_paid, paid_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListInstallmentsByExpense :many
SELECT * FROM expense_installments
WHERE expense_id = $1
ORDER BY installment_number ASC;

-- name: GetInstallmentByExpenseAndNumber :one
SELECT * FROM expense_installments
WHERE expense_id = $1 AND installment_number = $2;

-- name: UpdateInstallmentDates :one
UPDATE expense_installments
SET billing_date = $2,
    due_date = $3,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: SetInstallmentPaid :one
UPDATE expense_installments
SET is_paid = $2,
    paid_at = CASE WHEN $2 THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- ===== shares =====

-- name: CreateInstallmentShare :exec
INSERT INTO expense_installment_shares (installment_id, user_id, amount_base_owed)
VALUES ($1, $2, $3);

-- name: ListSharesByInstallment :many
SELECT * FROM expense_installment_shares
WHERE installment_id = $1;

-- name: ListSharesByExpense :many
SELECT s.installment_id, s.user_id, s.amount_base_owed
FROM expense_installment_shares s
JOIN expense_installments i ON i.id = s.installment_id
WHERE i.expense_id = $1
ORDER BY i.installment_number ASC;

-- name: SumCardInstallmentsByPeriod :many
-- Installments of a card per closing month and currency, between two
-- billing dates (inclusive). The card's expenses can live in several
-- households (Mi plata and Hogar): a card has one owner and one bill.
-- Drafts (variable recurring expenses waiting for their amount) stay out
-- until confirmed.
SELECT to_char(ei.billing_date, 'YYYY-MM')::text AS period_ym,
       e.currency,
       SUM(ei.installment_amount)::numeric AS total,
       COUNT(*)::int AS installment_count
FROM expense_installments ei
JOIN expenses e ON e.id = ei.expense_id
WHERE e.payment_method_id = sqlc.arg(payment_method_id)
  AND ei.billing_date BETWEEN sqlc.arg(from_date) AND sqlc.arg(to_date)
  AND e.status = 'confirmed'
GROUP BY 1, 2
ORDER BY 1, 2;

-- name: ListCardStatementInstallments :many
SELECT ei.id AS installment_id,
       ei.expense_id,
       e.household_id,
       e.category_id,
       e.description,
       e.spent_at,
       ei.installment_number,
       e.installments AS total_installments,
       ei.installment_amount,
       e.currency,
       ei.is_paid
FROM expense_installments ei
JOIN expenses e ON e.id = ei.expense_id
WHERE e.payment_method_id = sqlc.arg(payment_method_id)
  AND ei.billing_date BETWEEN sqlc.arg(from_date) AND sqlc.arg(to_date)
  AND e.status = 'confirmed'
ORDER BY e.spent_at, e.created_at, ei.installment_number;

-- name: SetCardStatementInstallmentsPaid :execrows
-- Marks every installment of a statement paid or unpaid at once.
UPDATE expense_installments ei
SET is_paid = sqlc.arg(is_paid),
    paid_at = CASE WHEN sqlc.arg(is_paid)::bool THEN NOW() ELSE NULL END,
    updated_at = NOW()
FROM expenses e
WHERE e.id = ei.expense_id
  AND e.payment_method_id = sqlc.arg(payment_method_id)
  AND ei.billing_date BETWEEN sqlc.arg(from_date) AND sqlc.arg(to_date)
  AND e.status = 'confirmed'
  AND ei.is_paid <> sqlc.arg(is_paid);

-- name: ListCardStatementRecords :many
SELECT * FROM credit_card_statements
WHERE credit_card_id = sqlc.arg(credit_card_id)
  AND period_ym BETWEEN sqlc.arg(from_ym)::text AND sqlc.arg(to_ym)::text;

-- name: EnsureCardStatement :exec
INSERT INTO credit_card_statements (credit_card_id, period_ym)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: SetCardStatementMinimum :exec
UPDATE credit_card_statements
SET minimum_payment = sqlc.narg(minimum_payment), updated_at = NOW()
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: SetCardStatementPayment :exec
UPDATE credit_card_statements
SET paid_at = sqlc.narg(paid_at),
    paid_amount = sqlc.narg(paid_amount),
    paid_currency = sqlc.narg(paid_currency),
    updated_at = NOW()
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: SumCardAdjustmentsByPeriod :many
SELECT period_ym,
       currency,
       SUM(CASE WHEN kind = 'credit' THEN -amount ELSE amount END)::numeric AS total
FROM credit_card_statement_adjustments
WHERE credit_card_id = sqlc.arg(credit_card_id)
  AND period_ym BETWEEN sqlc.arg(from_ym)::text AND sqlc.arg(to_ym)::text
GROUP BY 1, 2;

-- name: ListCardStatementAdjustments :many
SELECT * FROM credit_card_statement_adjustments
WHERE credit_card_id = $1 AND period_ym = $2
ORDER BY created_at, id;

-- name: CreateCardStatementAdjustment :one
INSERT INTO credit_card_statement_adjustments (credit_card_id, period_ym, kind, description, amount, currency)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateCardStatementAdjustment :one
UPDATE credit_card_statement_adjustments
SET kind = $4, description = $5, amount = $6, currency = $7, updated_at = NOW()
WHERE id = $1 AND credit_card_id = $2 AND period_ym = $3
RETURNING *;

-- name: DeleteCardStatementAdjustment :execrows
DELETE FROM credit_card_statement_adjustments
WHERE id = $1 AND credit_card_id = $2 AND period_ym = $3;

-- name: ListCreditCardPeriodsInRange :many
SELECT * FROM credit_card_periods
WHERE credit_card_id = sqlc.arg(credit_card_id)
  AND period_ym BETWEEN sqlc.arg(from_ym)::text AND sqlc.arg(to_ym)::text;

-- name: ListCardInstallmentPlans :many
-- "Cuotas en curso" (plan 4.2): confirmed expenses of a card in two or more
-- installments with an installment billed on or after from_date. next_* is
-- the first of those installments; last_billing_date is when the plan ends.
SELECT e.id AS expense_id,
       e.household_id,
       e.category_id,
       e.description,
       e.currency,
       e.installments AS total_installments,
       nxt.installment_number AS next_installment_number,
       nxt.installment_amount AS next_installment_amount,
       lst.billing_date AS last_billing_date
FROM expenses e
JOIN LATERAL (
    SELECT ei.installment_number, ei.installment_amount
    FROM expense_installments ei
    WHERE ei.expense_id = e.id AND ei.billing_date >= sqlc.arg(from_date)
    ORDER BY ei.installment_number
    LIMIT 1
) nxt ON true
JOIN LATERAL (
    SELECT MAX(ei.billing_date)::date AS billing_date
    FROM expense_installments ei
    WHERE ei.expense_id = e.id
) lst ON true
WHERE e.payment_method_id = sqlc.arg(payment_method_id)
  AND e.installments > 1
  AND e.status = 'confirmed'
ORDER BY lst.billing_date, e.spent_at, e.id;

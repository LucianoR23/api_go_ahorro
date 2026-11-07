-- name: CreateSettlement :one
INSERT INTO settlement_payments (
    household_id, from_user, to_user, amount_base, base_currency, note, paid_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSettlementByID :one
SELECT * FROM settlement_payments WHERE id = $1;

-- name: DeleteSettlement :exec
DELETE FROM settlement_payments WHERE id = $1;

-- ===== balances (on-demand) =====

-- name: BalanceOwedBetween :one
-- Devuelve cuánto debe `from_user` a `to_user` en el hogar, calculado on-demand.
-- Fórmula (ver sección 6 del plan):
--   owed_by_from = SUM(shares.amount_base_owed) sobre expenses confirmados creados
--                  por to_user con installment.billing_date <= today y share.user_id = from_user
--                  (today comes from the app timezone, not the DB's CURRENT_DATE; drafts
--                  are not debts until confirmed, plan 0.4)
--   owed_by_to   = análoga en sentido inverso
--   settled_fwd  = SUM(settlements from_user → to_user)
--   settled_bwd  = SUM(settlements to_user → from_user)
--   balance      = owed_by_from - owed_by_to - settled_fwd + settled_bwd
-- > 0 → from_user debe ese monto a to_user
-- < 0 → to_user debe a from_user (el signo lo resuelve el service)
SELECT
    COALESCE((
        SELECT SUM(s.amount_base_owed)::numeric
        FROM expense_installment_shares s
        JOIN expense_installments i ON i.id = s.installment_id
        JOIN expenses e ON e.id = i.expense_id
        WHERE i.household_id = $1
          AND e.created_by = sqlc.arg(to_user)::uuid
          AND s.user_id = sqlc.arg(from_user)::uuid
          AND e.status = 'confirmed'
          AND i.billing_date <= sqlc.arg(today)::date
    ), 0)::numeric AS owed_by_from,
    COALESCE((
        SELECT SUM(s.amount_base_owed)::numeric
        FROM expense_installment_shares s
        JOIN expense_installments i ON i.id = s.installment_id
        JOIN expenses e ON e.id = i.expense_id
        WHERE i.household_id = $1
          AND e.created_by = sqlc.arg(from_user)::uuid
          AND s.user_id = sqlc.arg(to_user)::uuid
          AND e.status = 'confirmed'
          AND i.billing_date <= sqlc.arg(today)::date
    ), 0)::numeric AS owed_by_to,
    COALESCE((
        SELECT SUM(amount_base)::numeric
        FROM settlement_payments
        WHERE household_id = $1
          AND from_user = sqlc.arg(from_user)::uuid
          AND to_user = sqlc.arg(to_user)::uuid
    ), 0)::numeric AS settled_fwd,
    COALESCE((
        SELECT SUM(amount_base)::numeric
        FROM settlement_payments
        WHERE household_id = $1
          AND from_user = sqlc.arg(to_user)::uuid
          AND to_user = sqlc.arg(from_user)::uuid
    ), 0)::numeric AS settled_bwd;

-- name: BalanceMatrixByHousehold :many
-- Para la vista de matriz completa del hogar. Devuelve una fila por cada
-- par (debtor, creditor) con shares billed y settlements ya agregados.
-- El service combina las direcciones y netea.
SELECT
    e.household_id,
    s.user_id      AS debtor_id,
    e.created_by   AS creditor_id,
    SUM(s.amount_base_owed)::numeric AS billed_owed
FROM expense_installment_shares s
JOIN expense_installments i ON i.id = s.installment_id
JOIN expenses e ON e.id = i.expense_id
WHERE i.household_id = $1
  AND e.status = 'confirmed'
  AND i.billing_date <= sqlc.arg(today)::date
  AND s.user_id <> e.created_by
GROUP BY e.household_id, s.user_id, e.created_by;

-- name: SettlementsByHouseholdAggregated :many
-- Agrega settlements por par (from, to) para la matriz.
SELECT
    household_id,
    from_user,
    to_user,
    SUM(amount_base)::numeric AS paid_total
FROM settlement_payments
WHERE household_id = $1
GROUP BY household_id, from_user, to_user;


-- name: LockHouseholdForSettlement :exec
-- Serializes settlements per household (plan 0.8): the debt check and the
-- insert run while this lock is held, so two payments cannot both pass the
-- check. NO KEY UPDATE does not block inserts that reference the household.
SELECT id FROM households WHERE id = $1 FOR NO KEY UPDATE;

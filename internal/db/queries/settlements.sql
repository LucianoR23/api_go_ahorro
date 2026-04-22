-- name: CreateSettlement :one
INSERT INTO settlement_payments (
    household_id, from_user, to_user, amount_base, base_currency, note, paid_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetSettlementByID :one
SELECT * FROM settlement_payments WHERE id = $1;

-- name: ListSettlementsByHousehold :many
-- Filtros opcionales: from_user, to_user (para ver pagos entre dos miembros
-- específicos), desde/hasta. Paginación por offset/limit.
SELECT *
FROM settlement_payments
WHERE household_id = $1
  AND (sqlc.narg(from_user)::uuid IS NULL OR from_user = sqlc.narg(from_user)::uuid)
  AND (sqlc.narg(to_user)::uuid IS NULL OR to_user = sqlc.narg(to_user)::uuid)
  AND (sqlc.narg(from_date)::date IS NULL OR paid_at >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR paid_at <= sqlc.narg(to_date)::date)
ORDER BY paid_at DESC, created_at DESC
LIMIT $2 OFFSET $3;

-- name: DeleteSettlement :exec
DELETE FROM settlement_payments WHERE id = $1;

-- ===== balances (on-demand) =====

-- name: BalanceOwedBetween :one
-- Devuelve cuánto debe `from_user` a `to_user` en el hogar, calculado on-demand.
-- Fórmula (ver sección 6 del plan):
--   owed_by_from = SUM(shares.amount_base_owed) sobre expenses creados por to_user
--                  con installment.billing_date <= CURRENT_DATE y share.user_id = from_user
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
        WHERE e.household_id = $1
          AND e.created_by = sqlc.arg(to_user)::uuid
          AND s.user_id = sqlc.arg(from_user)::uuid
          AND i.billing_date <= CURRENT_DATE
    ), 0)::numeric AS owed_by_from,
    COALESCE((
        SELECT SUM(s.amount_base_owed)::numeric
        FROM expense_installment_shares s
        JOIN expense_installments i ON i.id = s.installment_id
        JOIN expenses e ON e.id = i.expense_id
        WHERE e.household_id = $1
          AND e.created_by = sqlc.arg(from_user)::uuid
          AND s.user_id = sqlc.arg(to_user)::uuid
          AND i.billing_date <= CURRENT_DATE
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
WHERE e.household_id = $1
  AND i.billing_date <= CURRENT_DATE
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

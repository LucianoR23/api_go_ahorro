-- ===================== budget_goals =====================

-- name: CreateBudgetGoal :one
INSERT INTO budget_goals (
    household_id, scope, user_id, category_id,
    goal_type, target_amount, currency, period, is_active
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetBudgetGoalByID :one
SELECT * FROM budget_goals WHERE id = $1;

-- name: ListBudgetGoalsByHousehold :many
-- Filtros opcionales: scope, user_id (cuando scope='user'), is_active.
SELECT * FROM budget_goals
WHERE household_id = $1
  AND (sqlc.narg(scope)::text IS NULL OR scope = sqlc.narg(scope)::text)
  AND (sqlc.narg(user_id)::uuid IS NULL OR user_id = sqlc.narg(user_id)::uuid)
  AND (sqlc.narg(only_active)::boolean IS NULL OR is_active = sqlc.narg(only_active)::boolean)
ORDER BY is_active DESC, created_at DESC;

-- name: UpdateBudgetGoal :one
-- No dejamos cambiar scope/user_id/goal_type — si hay que migrar, borrar y crear.
UPDATE budget_goals
SET category_id   = $2,
    target_amount = $3,
    currency      = $4,
    period        = $5
WHERE id = $1
RETURNING *;

-- name: SetBudgetGoalActive :exec
UPDATE budget_goals SET is_active = $2 WHERE id = $1;

-- name: DeleteBudgetGoal :exec
DELETE FROM budget_goals WHERE id = $1;

-- ===================== progress helpers =====================

-- name: SumInstallmentsForHouseholdGoal :one
-- Suma base de cuotas del hogar dentro del período, filtrando por categoría
-- opcional. Usa COALESCE(due_date, billing_date): crédito cuenta cuando vence,
-- el resto cuando se pagó (billing_date = spent_at).
SELECT COALESCE(SUM(i.installment_amount_base), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE e.household_id = $1
  AND (sqlc.narg(category_id)::uuid IS NULL OR e.category_id = sqlc.narg(category_id)::uuid)
  AND COALESCE(i.due_date, i.billing_date) >= $2::date
  AND COALESCE(i.due_date, i.billing_date) <= $3::date;

-- name: SumInstallmentsForUserGoal :one
-- Suma la porción del usuario en cuotas del hogar dentro del período.
-- Para gastos compartidos: usa expense_installment_shares.
-- Para gastos NO compartidos (is_shared=false): cuenta el total si el usuario
-- es created_by, porque no hay filas de shares.
SELECT (
    COALESCE((
        SELECT SUM(s.amount_base_owed)
        FROM expense_installment_shares s
        JOIN expense_installments i ON i.id = s.installment_id
        JOIN expenses e ON e.id = i.expense_id
        WHERE e.household_id = $1
          AND e.is_shared = true
          AND s.user_id = $2
          AND (sqlc.narg(category_id)::uuid IS NULL OR e.category_id = sqlc.narg(category_id)::uuid)
          AND COALESCE(i.due_date, i.billing_date) >= $3::date
          AND COALESCE(i.due_date, i.billing_date) <= $4::date
    ), 0)
    +
    COALESCE((
        SELECT SUM(i.installment_amount_base)
        FROM expense_installments i
        JOIN expenses e ON e.id = i.expense_id
        WHERE e.household_id = $1
          AND e.is_shared = false
          AND e.created_by = $2
          AND (sqlc.narg(category_id)::uuid IS NULL OR e.category_id = sqlc.narg(category_id)::uuid)
          AND COALESCE(i.due_date, i.billing_date) >= $3::date
          AND COALESCE(i.due_date, i.billing_date) <= $4::date
    ), 0)
)::numeric AS total_base;

-- name: SumIncomesByUserInRange :one
-- Para savings scope=user: total de ingresos de un usuario en el período.
SELECT COALESCE(SUM(amount_base), 0)::numeric AS total_base
FROM incomes
WHERE household_id = $1
  AND received_by = $2
  AND received_at >= $3::date
  AND received_at <= $4::date;

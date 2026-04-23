-- ===================== reports: triada spent/billed/due =====================

-- name: SumExpensesSpentAtForReport :one
-- "spent_this_month": lo que se compró/decidió gastar en el rango.
SELECT COALESCE(SUM(amount_base), 0)::numeric AS total_base
FROM expenses
WHERE household_id = $1
  AND spent_at >= $2::date
  AND spent_at <= $3::date;

-- name: SumInstallmentsBilledForReport :one
-- "billed_this_month": lo que apareció en resúmenes de tarjeta en el rango
-- (billing_date). Para no-crédito es igual a spent_at (la query no distingue).
SELECT COALESCE(SUM(i.installment_amount_base), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE e.household_id = $1
  AND i.billing_date >= $2::date
  AND i.billing_date <= $3::date;

-- name: SumInstallmentsDueForReport :one
-- "due_this_month": lo que hay que pagar en el rango (due_date). Para
-- no-crédito due_date es NULL, usamos COALESCE con billing_date.
SELECT COALESCE(SUM(i.installment_amount_base), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE e.household_id = $1
  AND COALESCE(i.due_date, i.billing_date) >= $2::date
  AND COALESCE(i.due_date, i.billing_date) <= $3::date;

-- ===================== breakdown por categoría =====================

-- name: SumExpensesByCategoryInRange :many
-- Para el resumen mensual: total por categoría usando spent_at.
-- Incluye categoría NULL (gastos sin categorizar). El JOIN es LEFT.
SELECT
    e.category_id,
    COALESCE(c.name, '')::text AS category_name,
    COALESCE(SUM(e.amount_base), 0)::numeric AS total_base,
    COUNT(*)::bigint AS tx_count
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
WHERE e.household_id = $1
  AND e.spent_at >= $2::date
  AND e.spent_at <= $3::date
GROUP BY e.category_id, c.name
ORDER BY total_base DESC;

-- ===================== fijos vs variables =====================

-- name: SumExpensesFixedVariableInRange :one
-- Split por origen: gastos con recurring_expense_id NOT NULL son "fijos"
-- (los generó el worker de CP9); el resto son "variables" (entrada manual).
SELECT
    COALESCE(SUM(CASE WHEN recurring_expense_id IS NOT NULL THEN amount_base ELSE 0 END), 0)::numeric AS fixed_total,
    COALESCE(SUM(CASE WHEN recurring_expense_id IS NULL THEN amount_base ELSE 0 END), 0)::numeric AS variable_total,
    COUNT(*) FILTER (WHERE recurring_expense_id IS NOT NULL)::bigint AS fixed_count,
    COUNT(*) FILTER (WHERE recurring_expense_id IS NULL)::bigint AS variable_count
FROM expenses
WHERE household_id = $1
  AND spent_at >= $2::date
  AND spent_at <= $3::date;

-- ===================== trends por mes =====================

-- name: SumExpensesSpentAtByMonth :many
-- Para gráfico de trends: total por mes en los últimos N meses.
-- El caller pasa from/to calculados (start del mes más viejo ~ end del actual).
-- date_trunc normaliza al primer día del mes.
SELECT
    date_trunc('month', spent_at)::date AS month,
    COALESCE(SUM(amount_base), 0)::numeric AS total_base,
    COUNT(*)::bigint AS tx_count
FROM expenses
WHERE household_id = $1
  AND spent_at >= $2::date
  AND spent_at <= $3::date
GROUP BY month
ORDER BY month ASC;

-- name: SumInstallmentsDueByMonth :many
-- Mismo concepto pero por due_date — lo que hay que pagar por mes.
SELECT
    date_trunc('month', COALESCE(i.due_date, i.billing_date))::date AS month,
    COALESCE(SUM(i.installment_amount_base), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE e.household_id = $1
  AND COALESCE(i.due_date, i.billing_date) >= $2::date
  AND COALESCE(i.due_date, i.billing_date) <= $3::date
GROUP BY month
ORDER BY month ASC;

-- name: SumIncomesReceivedByMonth :many
-- Ingresos por mes para comparar contra gastos en trends.
SELECT
    date_trunc('month', received_at)::date AS month,
    COALESCE(SUM(amount_base), 0)::numeric AS total_base
FROM incomes
WHERE household_id = $1
  AND received_at >= $2::date
  AND received_at <= $3::date
GROUP BY month
ORDER BY month ASC;

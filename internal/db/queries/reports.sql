-- Every amount goes through report_amount (plan 5.3): pesos when usd_source
-- is NULL, dollars otherwise. Dollars use the dollar of each movement's day
-- (an installment, its purchase day) unless rate_day fixes one day (today).

-- ===================== reports: triada spent/billed/due =====================

-- name: SumExpensesSpentAtForReport :one
-- "spent_this_month": lo que se compró/decidió gastar en el rango.
SELECT COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM expenses
WHERE household_id = sqlc.arg(household_id)
  AND status = 'confirmed'
  AND spent_at >= sqlc.arg(from_date)::date
  AND spent_at <= sqlc.arg(to_date)::date;

-- name: SumInstallmentsBilledForReport :one
-- "billed_this_month": lo que apareció en resúmenes de tarjeta en el rango
-- (billing_date). Para no-crédito es igual a spent_at (la query no distingue).
SELECT COALESCE(SUM(report_amount(i.installment_amount_base, e.currency, i.installment_amount, COALESCE(sqlc.narg(rate_day)::date, e.spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE i.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND i.billing_date >= sqlc.arg(from_date)::date
  AND i.billing_date <= sqlc.arg(to_date)::date;

-- name: SumInstallmentsDueForReport :one
-- "due_this_month": lo que hay que pagar en el rango (due_date). Para
-- no-crédito due_date es NULL, usamos COALESCE con billing_date.
SELECT COALESCE(SUM(report_amount(i.installment_amount_base, e.currency, i.installment_amount, COALESCE(sqlc.narg(rate_day)::date, e.spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE i.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND COALESCE(i.due_date, i.billing_date) >= sqlc.arg(from_date)::date
  AND COALESCE(i.due_date, i.billing_date) <= sqlc.arg(to_date)::date;

-- ===================== breakdown por categoría =====================

-- name: SumExpensesByCategoryInRange :many
-- Para el resumen mensual: total por categoría usando spent_at.
-- Incluye categoría NULL (gastos sin categorizar). El JOIN es LEFT.
-- A subcategory adds up in its parent (plan 1.4): Luz counts as Servicios.
SELECT
    top.id AS category_id,
    COALESCE(top.name, '')::text AS category_name,
    COALESCE(SUM(report_amount(e.amount_base, e.currency, e.amount, COALESCE(sqlc.narg(rate_day)::date, e.spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base,
    COUNT(*)::bigint AS tx_count
FROM expenses e
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN categories top ON top.id = COALESCE(c.parent_id, c.id)
WHERE e.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND e.spent_at >= sqlc.arg(from_date)::date
  AND e.spent_at <= sqlc.arg(to_date)::date
GROUP BY top.id, top.name
ORDER BY total_base DESC;

-- name: SumExpensesByPayerInRange :many
-- Inicio · Hogar (plan 1.10b): what each member paid in the range, by
-- spent_at. Whoever loads an expense is the one who paid it.
SELECT
    e.created_by AS user_id,
    COALESCE(SUM(report_amount(e.amount_base, e.currency, e.amount, COALESCE(sqlc.narg(rate_day)::date, e.spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM expenses e
WHERE e.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND e.spent_at >= sqlc.arg(from_date)::date
  AND e.spent_at <= sqlc.arg(to_date)::date
GROUP BY e.created_by
ORDER BY total_base DESC;

-- ===================== fijos vs variables =====================

-- name: SumExpensesFixedVariableInRange :one
-- Split por origen: gastos con recurring_expense_id NOT NULL son "fijos"
-- (los generó el worker de CP9); el resto son "variables" (entrada manual).
SELECT
    COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)) FILTER (WHERE recurring_expense_id IS NOT NULL), 0)::numeric AS fixed_total,
    COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)) FILTER (WHERE recurring_expense_id IS NULL), 0)::numeric AS variable_total,
    COUNT(*) FILTER (WHERE recurring_expense_id IS NOT NULL)::bigint AS fixed_count,
    COUNT(*) FILTER (WHERE recurring_expense_id IS NULL)::bigint AS variable_count
FROM expenses
WHERE household_id = sqlc.arg(household_id)
  AND status = 'confirmed'
  AND spent_at >= sqlc.arg(from_date)::date
  AND spent_at <= sqlc.arg(to_date)::date;

-- ===================== trends por mes =====================

-- name: SumExpensesSpentAtByMonth :many
-- Para gráfico de trends: total por mes en los últimos N meses.
-- El caller pasa from/to calculados (start del mes más viejo ~ end del actual).
-- date_trunc normaliza al primer día del mes.
SELECT
    date_trunc('month', spent_at)::date AS month,
    COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base,
    COUNT(*)::bigint AS tx_count
FROM expenses
WHERE household_id = sqlc.arg(household_id)
  AND status = 'confirmed'
  AND spent_at >= sqlc.arg(from_date)::date
  AND spent_at <= sqlc.arg(to_date)::date
GROUP BY month
ORDER BY month ASC;

-- name: SumInstallmentsDueByMonth :many
-- Mismo concepto pero por due_date — lo que hay que pagar por mes.
SELECT
    date_trunc('month', COALESCE(i.due_date, i.billing_date))::date AS month,
    COALESCE(SUM(report_amount(i.installment_amount_base, e.currency, i.installment_amount, COALESCE(sqlc.narg(rate_day)::date, e.spent_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM expense_installments i
JOIN expenses e ON e.id = i.expense_id
WHERE i.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND COALESCE(i.due_date, i.billing_date) >= sqlc.arg(from_date)::date
  AND COALESCE(i.due_date, i.billing_date) <= sqlc.arg(to_date)::date
GROUP BY month
ORDER BY month ASC;

-- name: SumIncomesReceivedByMonth :many
-- Ingresos por mes para comparar contra gastos en trends.
SELECT
    date_trunc('month', received_at)::date AS month,
    COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, received_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM incomes
WHERE household_id = sqlc.arg(household_id)
  AND received_at >= sqlc.arg(from_date)::date
  AND received_at <= sqlc.arg(to_date)::date
GROUP BY month
ORDER BY month ASC;

-- name: SumIncomesForReport :one
-- Incomes received in a range (Inicio, plan 5.3), like /totals/income.
SELECT COALESCE(SUM(report_amount(amount_base, currency, amount, COALESCE(sqlc.narg(rate_day)::date, received_at), sqlc.narg(usd_source)::text, sqlc.arg(tz)::text)), 0)::numeric AS total_base
FROM incomes
WHERE household_id = sqlc.arg(household_id)
  AND received_at >= sqlc.arg(from_date)::date
  AND received_at <= sqlc.arg(to_date)::date;

-- name: ReportUSDRate :one
-- The dollar a report in dollars uses on a day; NULL without quotes.
SELECT fx_usd_rate(sqlc.arg(usd_source)::text, sqlc.arg(day)::date, sqlc.arg(tz)::text)::numeric AS rate;

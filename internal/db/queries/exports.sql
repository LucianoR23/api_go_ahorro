-- Spreadsheet exports (plan 11.1). Oldest first, like a ledger.
-- A payment method's name is shown only to its owner: the app never shows a
-- member's cards to the others, so the export doesn't either.

-- name: ListExpensesForExport :many
-- Confirmed expenses in [from_date, to_date] with the same filters as
-- GET /expenses. q must come escaped for LIKE (backslash escape).
SELECT
    e.spent_at,
    e.description,
    e.amount,
    e.currency,
    e.amount_base,
    e.rate_used,
    e.installments,
    e.is_shared,
    (e.recurring_expense_id IS NOT NULL)::boolean AS is_fixed,
    c.name  AS category_name,
    pc.name AS parent_category_name,
    COALESCE(CASE WHEN pm.owner_user_id = sqlc.arg(viewer_id)::uuid THEN pm.name END, '')::text AS payment_method_name,
    u.first_name AS created_by_name
FROM expenses e
JOIN payment_methods pm ON pm.id = e.payment_method_id
JOIN users u ON u.id = e.created_by
LEFT JOIN categories c ON c.id = e.category_id
LEFT JOIN categories pc ON pc.id = c.parent_id
WHERE e.household_id = sqlc.arg(household_id)
  AND e.status = 'confirmed'
  AND e.spent_at BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
  AND (sqlc.narg(category_id)::uuid IS NULL
       OR e.category_id IN (SELECT id FROM categories
                            WHERE id = sqlc.narg(category_id)::uuid OR parent_id = sqlc.narg(category_id)::uuid))
  AND (sqlc.narg(payment_method_id)::uuid IS NULL OR e.payment_method_id = sqlc.narg(payment_method_id)::uuid)
  AND (sqlc.narg(fixed)::boolean IS NULL OR (e.recurring_expense_id IS NOT NULL) = sqlc.narg(fixed)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR e.description ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY e.spent_at, e.created_at
LIMIT sqlc.arg(row_limit);

-- name: ListIncomesForExport :many
SELECT
    i.received_at,
    i.description,
    i.source,
    i.amount,
    i.currency,
    i.amount_base,
    i.rate_used,
    COALESCE(CASE WHEN pm.owner_user_id = sqlc.arg(viewer_id)::uuid THEN pm.name END, '')::text AS payment_method_name,
    u.first_name AS received_by_name
FROM incomes i
JOIN users u ON u.id = i.received_by
LEFT JOIN payment_methods pm ON pm.id = i.payment_method_id
WHERE i.household_id = sqlc.arg(household_id)
  AND i.received_at BETWEEN sqlc.arg(from_date)::date AND sqlc.arg(to_date)::date
ORDER BY i.received_at, i.created_at
LIMIT sqlc.arg(row_limit);

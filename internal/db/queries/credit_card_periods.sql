-- name: UpsertCreditCardPeriod :one
INSERT INTO credit_card_periods (credit_card_id, period_ym, closing_date, due_date)
VALUES ($1, $2, $3, $4)
ON CONFLICT (credit_card_id, period_ym) DO UPDATE
SET closing_date = EXCLUDED.closing_date,
    due_date = EXCLUDED.due_date,
    updated_at = NOW()
RETURNING *;

-- name: GetCreditCardPeriod :one
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: ListCreditCardPeriods :many
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1
ORDER BY period_ym DESC;

-- name: DeleteCreditCardPeriod :exec
DELETE FROM credit_card_periods
WHERE credit_card_id = $1 AND period_ym = $2;

-- name: GetLatestCreditCardPeriod :one
SELECT * FROM credit_card_periods
WHERE credit_card_id = $1
ORDER BY period_ym DESC
LIMIT 1;
